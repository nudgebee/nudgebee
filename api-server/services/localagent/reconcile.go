// Package localagent adopts the credential the server Helm chart generates for
// the in-cluster agent bundled with the server, so a first-time install does not
// require a second, manual agent installation.
//
// The chart generates the access key and secret once and hands the same values
// to both the agent (as NUDGEBEE_AUTH_SECRET_KEY) and this server (as
// LOCAL_AGENT_ACCESS_KEY / LOCAL_AGENT_ACCESS_SECRET). All this package does is
// make the database agree: it creates the cloud account and agent rows that the
// collector and relay authenticate the agent against. The agent already holds
// its credential from the moment its pod starts, so there is nothing to order
// between the two.
//
// It is inert when the environment variables are unset, which is every install
// that does not bundle the agent.
package localagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// Reconcile resolves the tenant and an owning user itself, then ensures the
// bundled agent's rows exist. This is the services-server boot path: on an
// upgrade the tenant already exists and this completes the registration; on a
// fresh install there is no tenant yet, so it no-ops and ReconcileForTenant
// picks it up when the first login creates one.
func Reconcile(ctx context.Context, logger *slog.Logger) error {
	if !configured() {
		return nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("local agent: database unavailable: %w", err)
	}

	// Mirrors license.SingleTenantBootstrap, which resolves the same tenant for
	// the login path — the two must agree or the agent lands in a tenant the
	// admin never joins.
	var tenantID string
	err = dbms.Db.QueryRowContext(ctx, "SELECT id::text FROM tenant ORDER BY created_at LIMIT 1").Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		logger.Info("local agent: no tenant yet, deferring registration to first login")
		return nil
	}
	if err != nil {
		return fmt.Errorf("local agent: tenant lookup failed: %w", err)
	}

	// cloud_accounts.created_by/updated_by are NOT NULL FKs to users(id), so the
	// account needs an owner. Any member of the tenant satisfies the constraint;
	// the oldest is the closest thing to "whoever set this up".
	var userID string
	err = dbms.Db.QueryRowContext(ctx, `
		SELECT u.id::text FROM users u
		JOIN tenant_users tu ON tu."user" = u.id
		WHERE tu.tenant = $1
		ORDER BY u.created_at LIMIT 1`, tenantID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		logger.Info("local agent: tenant has no users yet, deferring registration to first login", "tenant", tenantID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("local agent: user lookup failed: %w", err)
	}

	return reconcile(ctx, logger, dbms, tenantID, userID)
}

// ReconcileForTenant ensures the bundled agent's rows exist for a tenant and
// owner the caller already knows. This is the first-login path: it runs from
// OnboardUser once the tenant is created, which on a fresh install is the first
// moment both a tenant id and a user id exist.
func ReconcileForTenant(ctx context.Context, logger *slog.Logger, tenantID, userID string) error {
	if !configured() {
		return nil
	}
	if tenantID == "" || userID == "" {
		return fmt.Errorf("local agent: refusing to register without both a tenant and an owner (tenant=%q user=%q)", tenantID, userID)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("local agent: database unavailable: %w", err)
	}
	return reconcile(ctx, logger, dbms, tenantID, userID)
}

func configured() bool {
	return config.Config.LocalAgentAccessKey != "" && config.Config.LocalAgentAccessSecret != ""
}

// clusterName is the account name the bundled agent registers under, and the
// key this whole reconcile is idempotent on.
func clusterName() string {
	name := strings.TrimSpace(config.Config.LocalAgentClusterName)
	if name == "" {
		return "in-cluster"
	}
	return name
}

// validateClusterName rejects names the database or the product would reject
// later, while the operator can still act on the message. The length floor is
// the cloud_accounts account_name_check constraint; the ceiling and character
// set are what the UI enforces on a hand-created account.
// It normalizes before checking so the outcome does not depend on the caller
// having trimmed — otherwise a padded reserved name is reported as a character
// problem, which sends the operator looking in the wrong place.
func validateClusterName(name string) error {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) <= 3 {
		return fmt.Errorf("local agent: cluster name %q is too short (needs more than 3 characters), set agent.clusterName", name)
	}
	if strings.EqualFold(trimmed, "Demo") {
		return fmt.Errorf("local agent: cluster name %q is reserved, set agent.clusterName to something else", name)
	}
	if !common.IsValidK8sAccountName(trimmed) {
		return fmt.Errorf("local agent: cluster name %q is not a valid account name (max 40 characters, no special characters), set agent.clusterName", name)
	}
	return nil
}

// reconcile ensures both rows exist: it creates whichever of the account and
// the agent credential is missing, and leaves anything already there untouched.
//
// Create-only, never update. RegenerateAgentKeys stays the only writer of an
// existing credential, so a deliberate "Renew token" from the UI is never
// silently reverted by a background process. The cost is that a database
// restored to a point after such a rotation will not self-heal — the operator
// realigns the Helm values by hand.
//
// "Ensure both" rather than "return early if the account exists": the agent row
// can be missing while the account is present, and that state is unauthenticable
// and otherwise permanent.
func reconcile(ctx context.Context, logger *slog.Logger, dbms *database.DatabaseManager, tenantID, userID string) error {
	name := clusterName()
	if err := validateClusterName(name); err != nil {
		return err
	}

	hashedSecret, err := common.HashPassword(config.Config.LocalAgentAccessSecret)
	if err != nil {
		return fmt.Errorf("local agent: failed to hash the access secret: %w", err)
	}

	// BeginTxx, not Beginx: the transaction has to observe the caller's context
	// so a cancelled boot or a hung database releases the connection instead of
	// holding it open.
	tx, err := dbms.Db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("local agent: failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ON CONFLICT rather than SELECT-then-INSERT: services-server can run more
	// than one replica, and they all reach this on boot at the same time.
	var accountID string
	created := true
	err = tx.QueryRowContext(ctx, `
		INSERT INTO cloud_accounts
			(cloud_provider, account_name, account_type, tenant, created_by, updated_by, status, agent_access_key, access_secret_v2)
		VALUES ('K8s', $1, 'kubernetes', $2, $3, $3, 'active', $4, $5)
		ON CONFLICT (tenant, account_name) DO NOTHING
		RETURNING id::text`,
		name, tenantID, userID, config.Config.LocalAgentAccessKey, hashedSecret).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		// The account is already there. Resolve its id and carry on to the agent
		// insert rather than returning here: the agent row can be missing even
		// though the account exists — the insert below is ON CONFLICT DO NOTHING,
		// so a conflicting access_key elsewhere leaves the account committed with
		// no credential, and the agent could never authenticate. Continuing is
		// still create-only; the agent insert never overwrites an existing row.
		created = false
		err = tx.QueryRowContext(ctx,
			`SELECT id::text FROM cloud_accounts WHERE tenant = $1 AND account_name = $2`,
			tenantID, name).Scan(&accountID)
		if errors.Is(err, sql.ErrNoRows) {
			// DO NOTHING conflicted against a row another replica has inserted but
			// not yet committed. That replica owns the registration; let it finish.
			logger.Info("local agent: another replica is registering this cluster, standing down", "cluster", name, "tenant", tenantID)
			return nil
		}
		if err != nil {
			return fmt.Errorf("local agent: failed to resolve the existing cloud account: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("local agent: failed to create the cloud account: %w", err)
	}

	// access_secret_v2 holds the bcrypt hash; the relay and collector both
	// bcrypt-compare the secret the agent presents against it. ON CONFLICT DO
	// NOTHING is what keeps this create-only — an existing credential, including
	// one rotated from the UI, is left exactly as it is.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO agent (cloud_account_id, tenant, access_key, access_secret_v2, status, type)
		VALUES ($1, $2, $3, $4, 'NOT_CONNECTED', 'k8s')
		ON CONFLICT DO NOTHING`,
		accountID, tenantID, config.Config.LocalAgentAccessKey, hashedSecret)
	if err != nil {
		return fmt.Errorf("local agent: failed to create the agent credential: %w", err)
	}
	agentRows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("local agent: failed to read the agent insert result: %w", err)
	}

	// Nothing to do and nothing done: the account and its credential were both
	// already in place. Commit anyway so the read is closed out cleanly.
	if !created && agentRows == 0 {
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("local agent: failed to commit: %w", err)
		}
		committed = true
		logger.Info("local agent: already registered, leaving it alone", "cluster", name, "tenant", tenantID)
		return nil
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("local agent: failed to commit: %w", err)
	}
	committed = true

	// The security context caches the tenant's account ids in Redis for 30
	// minutes, and Redis outlives a services-server restart. Without this the
	// cluster stays invisible to sessions that are already cached.
	if err := security.InvalidateCacheForTenant(tenantID); err != nil {
		logger.Error("local agent: registered, but failed to invalidate the tenant cache — the cluster may not appear for up to 30 minutes",
			"error", err, "tenant", tenantID)
	}

	if created {
		logger.Info("local agent: registered the in-cluster agent", "cluster", name, "account", accountID, "tenant", tenantID)
	} else {
		logger.Info("local agent: the cluster existed without a credential, added one", "cluster", name, "account", accountID, "tenant", tenantID)
	}
	return nil
}
