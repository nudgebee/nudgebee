package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"nudgebee/relay-server/pkg/cache"
	"nudgebee/relay-server/pkg/utils"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// AgentType constants for agent type filtering.
const (
	AgentTypeK8s   = "k8s"
	AgentTypeProxy = "proxy"
)

// WorkspaceConfigValue is a single key/value pair from an integration's config.
type WorkspaceConfigValue struct {
	Name  string
	Value string
}

// AgentDatasource describes a datasource reported by a proxy agent for auto-registration.
type AgentDatasource struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ProxyType string `json:"proxy_type"`
	Name      string `json:"name"`
}

// ProxyDatasource holds the datasource config for pushing to proxy agents on connect.
type ProxyDatasource struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	ProxyType        string            `json:"proxy_type"`
	Name             string            `json:"name,omitempty"`
	Config           map[string]any    `json:"config"`
	Credentials      map[string]string `json:"credentials,omitempty"`
	CredentialSource string            `json:"credential_source"`
	CredentialRef    string            `json:"credential_ref,omitempty"`
}

// AgentStore defines what handlers need from the DB layer.
type AgentStore interface {
	// ValidateAgent authenticates an agent and returns its account, type and
	// agent.id. The agent id identifies one agent among several of the same
	// type on an account (e.g. two proxy agents).
	ValidateAgent(ctx context.Context, accessKey, secret string) (ok bool, accountID, agentType, agentID string, err error)
	IsWSEnabled(ctx context.Context, accountID, agentType string) (bool, string, error)
	IsAgentConnected(ctx context.Context, accountID, agentType string) (bool, error)
	GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusAdditionalLabel string, err error)
	// AccountTenant returns the tenant that owns accountID ("" when the
	// account does not exist). It reads the same cached row as GetAgentStatus,
	// so calling both costs one DB query at most.
	AccountTenant(ctx context.Context, accountID, agentType string) (string, error)
	UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) (transitioned bool, tenantID string, err error)
	TouchRelaySession(ctx context.Context, accountID, agentType string) error
	UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error
	UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error
	// UpsertAgentDatasources registers the reporting agent's inventory and
	// removes integrations that agent (agentID) owned but no longer reports.
	UpsertAgentDatasources(ctx context.Context, accountID, agentType, agentID string, datasources []AgentDatasource) error
	// UpdateDatasourceMetadata returns the metadata keys whose UPDATE
	// matched zero rows, so the caller can retry a metadata message that
	// raced the matching datasource_inventory INSERT (see #38718), and
	// whether any integration's labels actually changed.
	UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) (notFound []string, labelsChanged bool, err error)
	// GetDatasourceAgentID resolves which agent owns datasourceKey on the
	// account, via the per-agent claims UpsertAgentDatasources writes.
	// It returns "" when the owner is unknown or ambiguous.
	GetDatasourceAgentID(ctx context.Context, accountID, datasourceKey string) (string, error)
	QueryProxyDatasources(ctx context.Context, accountID string) ([]ProxyDatasource, error)
	// GetWorkspaceToolConfig returns integration config values for the given account + integration type.
	// If configName is non-empty, only configs whose name matches (case-insensitive) are returned.
	GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]WorkspaceConfigValue, error)
}

// pgStore is a PostgreSQL implementation of AgentStore.
type pgStore struct {
	db            *sqlx.DB
	encryptionKey string

	agentCache sync.Map    // map[accessKey]agentCacheEntry
	wsCache    cache.Cache // shared cache (Redis or in-memory) for agent connection status

	// ownerCache maps accountID+"\x00"+datasourceKey → ownerCacheEntry, so
	// routing a proxy request does not cost a DB round-trip per call.
	ownerCache sync.Map
}

type agentCacheEntry struct {
	Valid     bool
	AccountID string
	AgentType string
	AgentID   string
	Expires   time.Time
}

type ownerCacheEntry struct {
	AgentID string
	Expires time.Time
}

type wsCacheEntry struct {
	Allowed          bool           `json:"allowed"`
	Fallback         string         `json:"fallback"`
	Status           string         `json:"status"`
	ConnectionStatus map[string]any `json:"connection_status"`
	// TenantID is the tenant owning the account ("" when the account does not
	// exist). Nil means the entry was cached by a relay version that did not
	// record the tenant; such entries are treated as a cache miss.
	TenantID *string `json:"tenant_id,omitempty"`
}

const (
	agentCacheTTL          = 30 * time.Minute
	wsCacheTTL             = 30 * time.Minute
	wsNotConnectedCacheTTL = 2 * time.Minute
	// ownerCacheTTL bounds how long a datasource → agent routing answer is
	// reused. Ownership only moves when another agent reports a datasource of
	// the same name, so staleness costs at most this long of misrouting.
	ownerCacheTTL = 30 * time.Second
)

// NewPostgresStore opens the DB and returns a pgStore.
func NewPostgresStore(
	dsn, encryptionKey string,
	maxOpenConns, maxIdleConns int,
	connMaxLifetime time.Duration,
	wsCache cache.Cache,
) (AgentStore, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}
	return &pgStore{db: db, encryptionKey: encryptionKey, wsCache: wsCache}, nil
}

func (p *pgStore) ValidateAgent(ctx context.Context, accessKey, secret string) (bool, string, string, string, error) {
	// 1) cache hit?
	if v, ok := p.agentCache.Load(accessKey); ok {
		ace := v.(agentCacheEntry)
		if time.Now().Before(ace.Expires) {
			return ace.Valid, ace.AccountID, ace.AgentType, ace.AgentID, nil
		}
		p.agentCache.Delete(accessKey)
	}

	// 2) query DB
	var row struct {
		AgentID      string         `db:"id"`
		AccountID    string         `db:"cloud_account_id"`
		AgentType    string         `db:"type"`
		SecretHashed sql.NullString `db:"access_secret_v2"`
	}
	err := p.db.GetContext(ctx, &row, `
        SELECT id::text AS id, cloud_account_id, "type", access_secret_v2
          FROM agent
         WHERE access_key = $1
    `, accessKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// cache failure short‐term
			p.agentCache.Store(accessKey, agentCacheEntry{Expires: time.Now().Add(1 * time.Minute)})
			return false, "", "", "", nil
		}
		return false, "", "", "", err
	}

	// 3) verify secret (v2 bcrypt only — legacy v1 AES path was deleted in B3
	// after DB confirmed no active agents on the legacy path)
	ok := false
	if row.SecretHashed.Valid {
		if bcrypt.CompareHashAndPassword([]byte(row.SecretHashed.String), []byte(secret)) == nil {
			ok = true
		}
	}
	if !ok {
		// cache failure short‐term
		p.agentCache.Store(accessKey, agentCacheEntry{Expires: time.Now().Add(1 * time.Minute)})
		return false, "", "", "", nil
	}

	// 4) cache success
	p.agentCache.Store(accessKey, agentCacheEntry{
		Valid:     true,
		AccountID: row.AccountID,
		AgentType: row.AgentType,
		AgentID:   row.AgentID,
		Expires:   time.Now().Add(agentCacheTTL),
	})
	return true, row.AccountID, row.AgentType, row.AgentID, nil
}

// wsCacheKey builds a composite cache key for account + agent type.
func wsCacheKey(accountID, agentType string) string {
	return accountID + ":" + agentType
}

// getWSEntry does the common cache check + DB query + cache store.
func (p *pgStore) getWSEntry(ctx context.Context, accountID, agentType string) (*wsCacheEntry, error) {
	cacheKey := wsCacheKey(accountID, agentType)

	// 1) Try cache
	if data, ok := p.wsCache.Get(ctx, cacheKey); ok {
		var wce wsCacheEntry
		if err := json.Unmarshal(data, &wce); err == nil && wce.TenantID != nil {
			return &wce, nil
		}
	}

	// 2) Query the agent row and the account's tenant in one go. Driving from
	// cloud_accounts keeps the tenant even when the account has no agent row.
	var row struct {
		ConnStatus sql.NullString `db:"connection_status"`
		Status     sql.NullString `db:"status"`
		Tenant     sql.NullString `db:"tenant"`
	}
	err := p.db.GetContext(ctx, &row, `
        SELECT a.connection_status, a.status, ca.tenant::text AS tenant
          FROM cloud_accounts ca
          LEFT JOIN agent a
            ON a.cloud_account_id = ca.id
           AND a."type" = $2
         WHERE ca.id = $1
    `, accountID, agentType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// 3) Build the entry
	entry := wsCacheEntry{
		Allowed:          true, // default
		Fallback:         "",   // default
		Status:           "NOT_CONNECTED",
		ConnectionStatus: map[string]any{},
		TenantID:         &row.Tenant.String,
	}

	if row.Status.Valid {
		entry.Status = row.Status.String
	}

	if row.ConnStatus.Valid {
		var csMap map[string]any
		if jerr := json.Unmarshal([]byte(row.ConnStatus.String), &csMap); jerr == nil {
			if val, ok := csMap["agentWSEnabled"].(bool); ok {
				entry.Allowed = val
			}
			if urlVal, ok := csMap["agentUrl"].(string); ok {
				entry.Fallback = urlVal
			}
			entry.ConnectionStatus = csMap
		}
	}

	// 4) Cache with appropriate TTL based on status
	ttl := wsCacheTTL
	if entry.Status != "CONNECTED" {
		ttl = wsNotConnectedCacheTTL
	}
	if data, err := json.Marshal(entry); err != nil {
		slog.Warn("failed to marshal wsCacheEntry for caching", "key", cacheKey, "error", err)
	} else if err := p.wsCache.Set(ctx, cacheKey, data, ttl); err != nil {
		slog.Warn("failed to set wsCacheEntry in cache", "key", cacheKey, "error", err)
	}
	return &entry, nil
}

// IsWSEnabled now just calls getWSEntry and returns the two fields.
func (p *pgStore) IsWSEnabled(ctx context.Context, accountID, agentType string) (bool, string, error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		// no rows → fall back to defaults baked into entry
		if errors.Is(err, sql.ErrNoRows) {
			return true, "", nil
		}
		return false, "", err
	}
	return entry.Allowed, entry.Fallback, nil
}

// IsAgentConnected likewise calls getWSEntry and inspects Status.
func (p *pgStore) IsAgentConnected(ctx context.Context, accountID, agentType string) (bool, error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return entry.Status == "CONNECTED", nil
}

// GetAgentStatus returns all agent status info in a single call to avoid double DB queries
func (p *pgStore) GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusAdditionalLabel string, err error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, true, "", "", nil // Default: disconnected but WS enabled
		}
		return false, false, "", "", err
	}

	if entry.ConnectionStatus["prometheusAdditionalLabels"] != nil {
		switch labels := entry.ConnectionStatus["prometheusAdditionalLabels"].(type) {
		case string:
			prometheusAdditionalLabel = labels
		case map[string]any:
			sb := strings.Builder{}
			for k, v := range labels {
				sb.WriteString(k)
				sb.WriteString("=")
				fmt.Fprintf(&sb, "\"%v\"", v)
				sb.WriteString(",")
			}
			if sb.Len() > 0 {
				prometheusAdditionalLabel = sb.String()
			}
		}
		if prometheusAdditionalLabel != "" {
			prometheusAdditionalLabel = strings.TrimSuffix(prometheusAdditionalLabel, ",")
		}
	}

	return entry.Status == "CONNECTED", entry.Allowed, entry.Fallback, prometheusAdditionalLabel, nil
}

// AccountTenant returns the tenant owning accountID, "" when the account does not exist.
func (p *pgStore) AccountTenant(ctx context.Context, accountID, agentType string) (string, error) {
	entry, err := p.getWSEntry(ctx, accountID, agentType)
	if err != nil {
		return "", err
	}
	return *entry.TenantID, nil
}

// UpdateRelayConnectionStatus updates the relayConnection field in the agent's connection_status JSON.
// sessionStart is the time this session began. When disconnecting, the update is skipped if a newer
// session has already connected (i.e. last_connected_at > sessionStart), preventing stale sessions
// from clobbering the status of active ones.
//
// It returns whether the agent's status actually transitioned (old status != new status) and the
// agent's tenant id. transitioned is true only when a row was updated AND its status changed, so
// callers can audit genuine connect/disconnect transitions without emitting on every reconnect,
// pod restart, or guarded stale-session cleanup.
func (p *pgStore) UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) (transitioned bool, tenantID string, err error) {
	// Update the connection_status JSON, agent status, and last_connected_at
	status := "NOT_CONNECTED"
	if relayConnected {
		status = "CONNECTED"
	}

	// A row-locked CTE reads the prior status so we can both gate the disconnect
	// (newer-session guard) and report whether the status changed, in one
	// statement. RETURNING surfaces the pre-update status and the tenant.
	var query string
	var args []any
	if relayConnected {
		// On connect: always update.
		query = `
			WITH prev AS (
				SELECT id, status FROM agent
				WHERE cloud_account_id = $1 AND "type" = $3
				FOR UPDATE
			)
			UPDATE agent
			SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('relayConnection', $2::boolean),
			    status = $4,
			    last_connected_at = NOW()
			FROM prev
			WHERE agent.id = prev.id
			RETURNING prev.status AS old_status, agent.tenant AS tenant
		`
		args = []any{accountID, relayConnected, agentType, status}
	} else {
		// On disconnect: only update if no newer session has connected since this one started.
		// This prevents a stale session's cleanup from overwriting a newer session's CONNECTED status.
		query = `
			WITH prev AS (
				SELECT id, status, last_connected_at FROM agent
				WHERE cloud_account_id = $1 AND "type" = $3
				FOR UPDATE
			)
			UPDATE agent
			SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('relayConnection', $2::boolean),
			    status = $4,
			    last_connected_at = NOW()
			FROM prev
			WHERE agent.id = prev.id
			  AND (prev.last_connected_at IS NULL OR prev.last_connected_at <= $5)
			RETURNING prev.status AS old_status, agent.tenant AS tenant
		`
		args = []any{accountID, relayConnected, agentType, status, sessionStart}
	}

	var oldStatus, tenant sql.NullString
	scanErr := p.db.QueryRowxContext(ctx, query, args...).Scan(&oldStatus, &tenant)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			// No row updated: either no matching agent, or the disconnect guard
			// skipped a stale session. Neither an error nor a transition.
			return false, "", nil
		}
		return false, "", fmt.Errorf("failed to update relay connection status for account %s (type=%s): %w", accountID, agentType, scanErr)
	}

	// Invalidate cache after successful DB write to prevent a concurrent read
	// from repopulating stale data between Delete and the update.
	if cerr := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); cerr != nil {
		slog.Warn("failed to invalidate wsCache after status update", "account_id", accountID, "agent_type", agentType, "error", cerr)
	}

	return oldStatus.String != status, tenant.String, nil
}

// TouchRelaySession records that this agent's websocket session was still
// alive just now, and re-asserts CONNECTED.
//
// It writes connection_status.relayLastSeenAt rather than last_connected_at
// on purpose. last_connected_at identifies the *session* — the disconnect
// branch of UpdateRelayConnectionStatus compares it against sessionStart to
// tell a stale session's cleanup from a live one's — so moving it forward
// mid-session would make every session look newer than itself and silently
// skip its own disconnect write.
//
// Re-asserting the status makes the relay self-healing: before this, a
// connect write lost to a DB blip left the account NOT_CONNECTED with a
// perfectly good socket, and nothing repaired it until the agent happened to
// reconnect. api-server's "Agent Status Check" cron reads relayLastSeenAt
// alongside last_connected_at, so this is also what stops it retiring a live
// long-lived session (nudgebee/nudgebee#36114).
func (p *pgStore) TouchRelaySession(ctx context.Context, accountID, agentType string) error {
	const query = `
		UPDATE agent
		SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('relayLastSeenAt', to_jsonb(NOW())),
		    status = 'CONNECTED'
		WHERE cloud_account_id = $1 AND "type" = $2
	`
	if _, err := p.db.ExecContext(ctx, query, accountID, agentType); err != nil {
		return fmt.Errorf("failed to touch relay session for account %s (type=%s): %w", accountID, agentType, err)
	}

	// The status we just asserted is cached; drop it so a stale NOT_CONNECTED
	// cannot keep failing requests for the rest of the TTL.
	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after relay session touch", "account_id", accountID, "agent_type", agentType, "error", err)
	}
	return nil
}

// UpdateAgentVersion persists the running agent build info to the agent row.
// - version is written to the first-class agent.version column (already surfaced via RPC).
// - commit/buildTime/protocolVersion nest under connection_status.agentBuild.
// Empty fields are preserved so partial reports (e.g. health ticks that only carry version+commit)
// don't clobber values already recorded from the initial greeting.
func (p *pgStore) UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error {
	if version == "" && commit == "" && buildTime == "" && protocolVersion == "" {
		return nil
	}

	build := map[string]string{}
	if commit != "" {
		build["commit"] = commit
	}
	if buildTime != "" {
		build["buildTime"] = buildTime
	}
	if protocolVersion != "" {
		build["protocolVersion"] = protocolVersion
	}

	buildJSON, err := json.Marshal(build)
	if err != nil {
		return fmt.Errorf("failed to marshal agent build info: %w", err)
	}

	// The trailing AND clause short-circuits the UPDATE when nothing changed
	// (same version, same build fields). Reconnects on network flaps are common
	// and would otherwise rewrite the row with identical data on every session,
	// producing dead tuples and WAL for no reason.
	query := `
		UPDATE agent
		SET version = CASE WHEN $2 <> '' THEN $2 ELSE version END,
		    connection_status = COALESCE(connection_status, '{}'::jsonb)
		                        || jsonb_build_object(
		                             'agentBuild',
		                             COALESCE(connection_status->'agentBuild', '{}'::jsonb) || $3::jsonb
		                           )
		WHERE cloud_account_id = $1 AND "type" = $4
		  AND (
		        ($2 <> '' AND version IS DISTINCT FROM $2)
		     OR NOT (COALESCE(connection_status->'agentBuild', '{}'::jsonb) @> $3::jsonb)
		      )
	`
	if _, err := p.db.ExecContext(ctx, query, accountID, version, string(buildJSON), agentType); err != nil {
		return fmt.Errorf("failed to update agent version for account %s (type=%s): %w", accountID, agentType, err)
	}

	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after agent version update", "account_id", accountID, "agent_type", agentType, "error", err)
	}
	return nil
}

// UpdateDatasourceHealth merges per-datasource health status into the agent's connection_status JSONB.
// The datasources map is keyed by datasource ID with status objects as values.
// Example: {"ds-uuid-1": {"type": "postgresql", "status": "healthy", "last_check": "..."}}
func (p *pgStore) UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error {
	dsJSON, err := json.Marshal(datasources)
	if err != nil {
		return fmt.Errorf("failed to marshal datasource health: %w", err)
	}

	query := `
		UPDATE agent
		SET connection_status = COALESCE(connection_status, '{}'::jsonb) || jsonb_build_object('datasources', $2::jsonb),
		    last_connected_at = NOW()
		WHERE cloud_account_id = $1 AND "type" = $3
	`

	_, err = p.db.ExecContext(ctx, query, accountID, string(dsJSON), agentType)
	if err != nil {
		return fmt.Errorf("failed to update datasource health for account %s (type=%s): %w", accountID, agentType, err)
	}

	// Invalidate cache after successful DB write
	if err := p.wsCache.Delete(ctx, wsCacheKey(accountID, agentType)); err != nil {
		slog.Warn("failed to invalidate wsCache after datasource health update", "account_id", accountID, "agent_type", agentType, "error", err)
	}

	return nil
}

// defaultDiscoveryTargetSQL gives a discovery integration a
// link_role='discovery_target' row pointing at the agent's own account, but
// only when it has no discovery_target row yet — so a target a user set via
// integrations_upsert_discovery_target is never clobbered on the next agent
// reconnect. Args: $1 integration_id, $2 cloud_account_id, $3 tenant_id.
//
// WHERE NOT EXISTS is the guard against a differently-scoped user target;
// ON CONFLICT DO NOTHING covers the narrower race where two concurrent
// callers (rapid reconnect, multiple relay instances) both pass the guard
// and insert the identical own-account tuple — without it, the loser gets a
// spurious unique-constraint error.
const defaultDiscoveryTargetSQL = `
	INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role)
	SELECT $1, $2, $3, 'discovery_target'
	WHERE NOT EXISTS (
		SELECT 1 FROM integrations_cloud_accounts
		WHERE integration_id = $1 AND link_role = 'discovery_target'
	)
	ON CONFLICT (integration_id, cloud_account_id, tenant_id, link_role) DO NOTHING`

// Ownership claims. Each agent that reports an agent-sourced integration
// holds one integration_config_values row on it, named
// agentOwnerConfigPrefix+<agent.id> with the agent id as its value.
//
// Several claims may coexist, because integrations are unique on
// (source, type, name, tenant_id): two agents reporting a datasource with the
// same type and name share one integration row. A single owner field would
// make the last reporter the owner, and that agent dropping the datasource
// would delete it out from under the other one. With per-agent claims, an
// agent's stale cleanup only releases its own claim, and the integration is
// deleted only once no claims remain.
//
// Claims live in integration_config_values rather than a new table so this
// needs no migration.
const agentOwnerConfigPrefix = "agent_owner:"

func agentOwnerConfigName(agentID string) string { return agentOwnerConfigPrefix + agentID }

// claimAgentDatasourceSQL records the reporting agent's claim on an
// integration. It returns a row only when the claim is new, carrying the other
// agents that already claim it (comma-separated, empty when none), so a shared
// datasource can be logged. Args: $1 integration_id, $2 claim name,
// $3 agent id.
const claimAgentDatasourceSQL = `
	INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at)
	VALUES (gen_random_uuid(), $1, $2, $3, false, NOW(), NOW())
	ON CONFLICT (integration_id, name) DO NOTHING
	RETURNING COALESCE((
		SELECT string_agg(value, ',') FROM integration_config_values
		WHERE integration_id = $1 AND starts_with(name::text, 'agent_owner:') AND value <> $3
	), '')`

// staleAgentDatasourcesSQL lists the agent-sourced integrations on an account
// that the reporting agent may release:
//   - those it has a claim on ($3 = its claim name), and
//   - when it is the only proxy agent on the account ($4), unclaimed legacy
//     rows written before claims existed. Only rows the relay itself
//     created qualify (a datasource_key config value, or a proxy-only type
//     without one), so k8s-collector's own source='agent' integrations
//     (prometheus, loki, ...) on the same account are never touched.
//
// Integrations claimed only by other agents are never candidates.
// Args: $1 account, $2 tenant, $3 claim name, $4 sole proxy agent.
const staleAgentDatasourcesSQL = `
	SELECT DISTINCT i.id::text, i.name::text, i.type::text
	FROM integrations i
	JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
	WHERE ica.cloud_account_id = $1
	AND i.tenant_id = $2
	AND i.source = 'agent'
	AND (
		EXISTS (
			SELECT 1 FROM integration_config_values c
			WHERE c.integration_id = i.id AND c.name = $3
		)
		OR (
			$4::boolean
			AND NOT EXISTS (
				SELECT 1 FROM integration_config_values c
				WHERE c.integration_id = i.id AND starts_with(c.name::text, 'agent_owner:')
			)
			AND (
				i.type IN ('mongodb_proxy', 'kafka_proxy')
				OR EXISTS (
					SELECT 1 FROM integration_config_values d
					WHERE d.integration_id = i.id AND d.name = 'datasource_key'
				)
			)
		)
	)`

// soleProxyAgentSQL reports whether agentID is the only proxy agent on the
// account. Args: $1 account, $2 agent id.
const soleProxyAgentSQL = `
	SELECT COALESCE(COUNT(*) = 1 AND BOOL_AND(id::text = $2), false)
	FROM agent
	WHERE cloud_account_id = $1 AND "type" = 'proxy'`

// remainingClaimsSQL counts the claims left on an integration after one was
// released, and how many of them belong to agents on the given account.
// Claims of agents that no longer exist do not count, so a deleted agent
// cannot pin an integration forever. Args: $1 integration_id, $2 account.
const remainingClaimsSQL = `
	SELECT COUNT(*) AS remaining,
	       COUNT(*) FILTER (WHERE a.cloud_account_id::text = $2) AS on_account
	FROM integration_config_values c
	JOIN agent a ON a.id::text = c.value
	WHERE c.integration_id = $1
	AND c.name::text = 'agent_owner:' || c.value`

// datasourceOwnerSQL resolves a datasource_key on an account to the proxy
// agents on that account that claim it. Claims by agents on other accounts
// are ignored, so a config value can never route a request to another
// account's agent. LIMIT 2 is enough to tell "one owner" from "ambiguous".
// Args: $1 account, $2 datasource key.
const datasourceOwnerSQL = `
	SELECT DISTINCT c.value
	FROM integration_config_values dk
	JOIN integration_config_values c ON c.integration_id = dk.integration_id AND c.name::text = 'agent_owner:' || c.value
	JOIN integrations_cloud_accounts ica ON ica.integration_id = dk.integration_id AND ica.cloud_account_id = $1
	JOIN agent a ON a.id::text = c.value AND a.cloud_account_id = $1 AND a."type" = 'proxy'
	WHERE dk.name = 'datasource_key' AND dk.value = $2
	LIMIT 2`

// agentTenant looks up the reporting agent's tenant. By agent id when known:
// an account can have several agents of the same type, and the (account, type)
// lookup then picks an arbitrary one of them.
func (p *pgStore) agentTenant(ctx context.Context, accountID, agentType, agentID string) (string, error) {
	var tenantID string
	var err error
	if agentID != "" {
		err = p.db.GetContext(ctx, &tenantID, `SELECT tenant FROM agent WHERE id = $1`, agentID)
	} else {
		err = p.db.GetContext(ctx, &tenantID, `
			SELECT tenant FROM agent WHERE cloud_account_id = $1 AND "type" = $2
		`, accountID, agentType)
	}
	if err != nil {
		return "", fmt.Errorf("failed to get tenant for account %s: %w", accountID, err)
	}
	return tenantID, nil
}

// UpsertAgentDatasources auto-registers locally configured datasources as integrations.
// This follows the same pattern as k8s-collector's _upsert_integration in telemetry_handler.py.
//
// Ownership: the reporting agent (agentID) records a claim on every
// integration it upserts (see agentOwnerConfigPrefix). When two agents report
// a datasource with the same type and name they share one integration row and
// both hold claims; that is logged as a warning, since requests for it can no
// longer be routed to a single agent (GetDatasourceAgentID reports it as
// ambiguous, and it goes to the shared queue). Rows without claims (written
// before claims existed) are claimed by the first agent that reports them.
func (p *pgStore) UpsertAgentDatasources(ctx context.Context, accountID, agentType, agentID string, datasources []AgentDatasource) error {
	if len(datasources) == 0 {
		return nil
	}

	tenantID, err := p.agentTenant(ctx, accountID, agentType, agentID)
	if err != nil {
		return err
	}

	// Track upserted integration IDs so we can clean up stale ones afterwards.
	upsertedIDs := make(map[string]bool)

	var errs []error
	for _, ds := range datasources {
		// Map proxy type to integration type name
		integrationType := proxyTypeToIntegrationType(ds.ProxyType, ds.Type)
		if integrationType == "" {
			continue
		}

		// Upsert integration
		var integrationID string
		err := p.db.GetContext(ctx, &integrationID, `
			INSERT INTO integrations (id, tenant_id, type, source, name, status, created_at, updated_at, labels)
			VALUES (gen_random_uuid(), $1, $2, 'agent', $3, 'enabled', NOW(), NOW(), '{}')
			ON CONFLICT (source, type, name, tenant_id) DO UPDATE
			SET status = 'enabled', updated_at = NOW()
			RETURNING id
		`, tenantID, integrationType, ds.Name)
		if err != nil {
			slog.Error("failed to upsert integration, skipping datasource", "datasource", ds.Name, "type", integrationType, "err", err)
			errs = append(errs, fmt.Errorf("datasource %s: %w", ds.Name, err))
			continue
		}

		upsertedIDs[integrationID] = true

		// Upsert integration-account mapping. link_role='own' marks this as
		// the account the agent itself runs in, distinct from a
		// 'discovery_target' row a discovery integration may separately be
		// given to name the cloud account whose network it scans (see
		// vmpackage.ListDiscoveryDatasources) — both can coexist for the
		// same integration_id/cloud_account_id under different roles.
		_, err = p.db.ExecContext(ctx, `
			INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role)
			VALUES ($1, $2, $3, 'own')
			ON CONFLICT (integration_id, cloud_account_id, tenant_id, link_role) DO NOTHING
		`, integrationID, accountID, tenantID)
		if err != nil {
			slog.Error("failed to upsert integration mapping, skipping datasource", "datasource", ds.Name, "err", err)
			errs = append(errs, fmt.Errorf("datasource %s mapping: %w", ds.Name, err))
			continue
		}

		// Record the reporting agent's claim (see the doc comment).
		if agentID != "" {
			var others string
			cerr := p.db.GetContext(ctx, &others, claimAgentDatasourceSQL, integrationID, agentOwnerConfigName(agentID), agentID)
			switch {
			case errors.Is(cerr, sql.ErrNoRows):
				// Already claimed by this agent; nothing written.
			case cerr != nil:
				slog.Error("failed to record datasource owner agent", "datasource", ds.Name, "integration_id", integrationID, "err", cerr)
				errs = append(errs, fmt.Errorf("datasource %s owner: %w", ds.Name, cerr))
			default:
				p.forgetAccountOwners(accountID)
				if others != "" {
					slog.Warn("agent datasource is also reported by other agents (same type+name); requests for it go to the shared queue",
						"datasource", ds.Name, "type", integrationType, "integration_id", integrationID,
						"other_agent_ids", others, "agent_id", agentID, "account_id", accountID)
				}
			}
		}

		// A discovery integration is only scannable once it is associated with
		// the account whose network it covers (link_role='discovery_target' —
		// see vmpackage.ListDiscoveryDatasourcesForAccount, the manual
		// account-scan trigger). For a self-hosted forager that runs in and
		// scans the same account, default that association to the agent's own
		// account so the "Scan account" button works without a separate
		// integrations_upsert_discovery_target call. Only when no target row
		// exists yet: a user who has pointed this datasource at a different
		// cloud account via that RPC must never be overwritten from here, and
		// this runs on every datasource_inventory message (every reconnect).
		if integrationType == "discovery" {
			res, terr := p.db.ExecContext(ctx, defaultDiscoveryTargetSQL, integrationID, accountID, tenantID)
			if terr != nil {
				slog.Error("failed to set default discovery target, skipping", "datasource", ds.Name, "err", terr)
				errs = append(errs, fmt.Errorf("datasource %s discovery target: %w", ds.Name, terr))
			} else if n, _ := res.RowsAffected(); n > 0 {
				slog.Info("defaulted discovery target to agent's own account", "datasource", ds.Name, "account_id", accountID)
			}
		}

		// For dual-mode types (db-proxy, redis-proxy), set config values
		// so the llm-server and runbook-server can route requests to the correct agent.
		if isDualModeProxy(ds.ProxyType) {
			for _, cv := range []struct{ name, value string }{
				{"connection_mode", "vm_agent"},
				{"agent_type", agentType},
				{"datasource_key", ds.ID},
			} {
				_, err = p.db.ExecContext(ctx, `
					INSERT INTO integration_config_values (id, integration_id, name, value, is_encrypted, created_at, updated_at)
					VALUES (gen_random_uuid(), $1, $2, $3, false, NOW(), NOW())
					ON CONFLICT (integration_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
				`, integrationID, cv.name, cv.value)
				if err != nil {
					slog.Error("failed to upsert config value, skipping", "config", cv.name, "datasource", ds.Name, "err", err)
				}
			}
		}
	}

	// Remove stale integrations this agent owned that are no longer in its
	// inventory. This prevents orphaned entries from accumulating when
	// customers rename or remove local datasources. Without a reporting agent
	// id there is no safe scope — another agent's integrations are
	// indistinguishable from this one's — so nothing is removed.
	if agentID == "" {
		slog.Warn("skipping stale agent datasource cleanup: reporting agent id unknown", "account_id", accountID, "agent_type", agentType)
	} else if err := p.removeStaleAgentDatasources(ctx, accountID, tenantID, agentID, upsertedIDs); err != nil {
		slog.Error("failed to remove stale agent datasources", "account_id", accountID, "agent_id", agentID, "err", err)
	}

	return errors.Join(errs...)
}

// removeStaleAgentDatasources releases this agent's claim on every
// integration on the account it no longer reports (see
// staleAgentDatasourcesSQL for which rows qualify), deleting an integration
// only once no claims remain. See releaseAgentDatasource.
func (p *pgStore) removeStaleAgentDatasources(ctx context.Context, accountID, tenantID, agentID string, currentIDs map[string]bool) error {
	var sole bool
	if err := p.db.GetContext(ctx, &sole, soleProxyAgentSQL, accountID, agentID); err != nil {
		// Not fatal: only legacy unclaimed rows depend on it.
		slog.Warn("failed to check for other proxy agents, not adopting unclaimed rows", "account_id", accountID, "err", err)
		sole = false
	}

	// Collect first, release after: releasing while the result set is open
	// holds a second pool connection per stale row for no reason.
	stale, err := p.listStaleAgentDatasources(ctx, accountID, tenantID, agentID, sole, currentIDs)
	if err != nil {
		return err
	}

	var errs []error
	for _, r := range stale {
		deleted, err := p.releaseAgentDatasource(ctx, r.id, accountID, agentID)
		if err != nil {
			slog.Error("failed to release stale agent datasource", "integration_id", r.id, "name", r.name, "err", err)
			errs = append(errs, err)
			continue
		}
		if deleted {
			slog.Info("removed stale agent datasource", "integration_id", r.id, "name", r.name, "type", r.intType, "account_id", accountID, "agent_id", agentID)
		} else {
			slog.Info("released agent claim on datasource still reported by other agents", "integration_id", r.id, "name", r.name, "type", r.intType, "account_id", accountID, "agent_id", agentID)
		}
	}
	if len(stale) > 0 {
		p.forgetAccountOwners(accountID)
	}
	return errors.Join(errs...)
}

// releaseAgentDatasource drops agentID's claim on one integration, in one
// transaction holding the integration row lock (which a concurrent
// UpsertAgentDatasources of the same row also takes), so a claim added
// concurrently by another agent is never deleted along with the row.
//
//   - No claims left: the integration and its config values are deleted
//     (integrations_cloud_accounts cascades).
//   - Claims left, none by an agent on accountID: only the 'own' link to
//     accountID is removed; the remaining claimants keep the row.
//   - Claims left by another agent on accountID: the row and link stay.
func (p *pgStore) releaseAgentDatasource(ctx context.Context, integrationID, accountID, agentID string) (deleted bool, err error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var locked string
	if err = tx.GetContext(ctx, &locked, `SELECT id::text FROM integrations WHERE id = $1 FOR UPDATE`, integrationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
			return false, tx.Commit()
		}
		return false, fmt.Errorf("lock integration: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM integration_config_values WHERE integration_id = $1 AND name = $2`,
		integrationID, agentOwnerConfigName(agentID)); err != nil {
		return false, fmt.Errorf("delete claim: %w", err)
	}

	var remaining struct {
		Remaining int `db:"remaining"`
		OnAccount int `db:"on_account"`
	}
	if err = tx.GetContext(ctx, &remaining, remainingClaimsSQL, integrationID, accountID); err != nil {
		return false, fmt.Errorf("count remaining claims: %w", err)
	}

	switch {
	case remaining.Remaining == 0:
		// Delete config values first (no cascade FK)
		if _, err = tx.ExecContext(ctx, `DELETE FROM integration_config_values WHERE integration_id = $1`, integrationID); err != nil {
			return false, fmt.Errorf("delete config values: %w", err)
		}
		// Delete integration (cascades to integrations_cloud_accounts)
		if _, err = tx.ExecContext(ctx, `DELETE FROM integrations WHERE id = $1`, integrationID); err != nil {
			return false, fmt.Errorf("delete integration: %w", err)
		}
		deleted = true
	case remaining.OnAccount == 0:
		if _, err = tx.ExecContext(ctx, `
			DELETE FROM integrations_cloud_accounts
			WHERE integration_id = $1 AND cloud_account_id = $2 AND link_role = 'own'
		`, integrationID, accountID); err != nil {
			return false, fmt.Errorf("delete account link: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return deleted, nil
}

type staleAgentDatasource struct {
	id, name, intType string
}

func (p *pgStore) listStaleAgentDatasources(ctx context.Context, accountID, tenantID, agentID string, soleProxyAgent bool, currentIDs map[string]bool) ([]staleAgentDatasource, error) {
	rows, err := p.db.QueryxContext(ctx, staleAgentDatasourcesSQL, accountID, tenantID, agentOwnerConfigName(agentID), soleProxyAgent)
	if err != nil {
		return nil, fmt.Errorf("query stale agent datasources: %w", err)
	}
	defer rows.Close() // nolint:errcheck

	var stale []staleAgentDatasource
	for rows.Next() {
		var r staleAgentDatasource
		if err := rows.Scan(&r.id, &r.name, &r.intType); err != nil {
			continue
		}
		if currentIDs[r.id] {
			continue
		}
		stale = append(stale, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale agent datasources: %w", err)
	}
	return stale, nil
}

func ownerCacheKey(accountID, datasourceKey string) string {
	return accountID + "\x00" + datasourceKey
}

// forgetAccountOwners drops this replica's cached routing answers for an
// account after its claims changed. Other replicas catch up within
// ownerCacheTTL.
func (p *pgStore) forgetAccountOwners(accountID string) {
	prefix := accountID + "\x00"
	p.ownerCache.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), prefix) {
			p.ownerCache.Delete(k)
		}
		return true
	})
}

// GetDatasourceAgentID resolves the proxy agent on the account that claims
// datasourceKey. "" means the owner is unknown (legacy rows without claims,
// or a datasource that was never agent-reported) or ambiguous (more than one
// agent on the account claims it), and the caller must fall back to the
// shared per-account queue.
func (p *pgStore) GetDatasourceAgentID(ctx context.Context, accountID, datasourceKey string) (string, error) {
	if accountID == "" || datasourceKey == "" {
		return "", nil
	}
	key := ownerCacheKey(accountID, datasourceKey)
	if v, ok := p.ownerCache.Load(key); ok {
		e := v.(ownerCacheEntry)
		if time.Now().Before(e.Expires) {
			return e.AgentID, nil
		}
		p.ownerCache.Delete(key)
	}

	var owners []string
	if err := p.db.SelectContext(ctx, &owners, datasourceOwnerSQL, accountID, datasourceKey); err != nil {
		return "", fmt.Errorf("resolve datasource owner for account %s: %w", accountID, err)
	}
	owner := ""
	switch len(owners) {
	case 1:
		owner = owners[0]
	case 0:
	default:
		slog.Warn("datasource is claimed by more than one agent on the account, routing to the shared queue",
			"account_id", accountID, "datasource_key", datasourceKey, "agent_ids", owners)
	}
	p.ownerCache.Store(key, ownerCacheEntry{AgentID: owner, Expires: time.Now().Add(ownerCacheTTL)})
	return owner, nil
}

// updateDatasourceLabelsSQL writes an integration's labels only when they
// differ, and reports both how many rows matched and how many changed: the
// caller retries on zero matches (the row does not exist yet) and invalidates
// downstream caches only on a real change. Args: $1 labels JSON, $2 name,
// $3 tenant.
const updateDatasourceLabelsSQL = `
	WITH target AS (
		SELECT id, labels::jsonb AS old_labels FROM integrations i
		WHERE i.source = 'agent' AND i.tenant_id = $3
		AND (
			EXISTS (
				SELECT 1 FROM integration_config_values v
				WHERE v.integration_id = i.id AND v.name = 'datasource_key' AND v.value = $4
			)
			OR i.name = $2
		)
	), upd AS (
		UPDATE integrations i
		SET labels = $1::jsonb, updated_at = NOW()
		FROM target t
		WHERE i.id = t.id AND t.old_labels IS DISTINCT FROM $1::jsonb
		RETURNING i.id
	)
	SELECT (SELECT count(*) FROM target) AS matched, (SELECT count(*) FROM upd) AS changed`

// UpdateDatasourceMetadata persists version and metadata from proxy agent datasources
// into the integrations.labels JSONB column.
//
// Returns the metadata keys (dsID, not the derived dsName) whose UPDATE
// matched zero rows — the integrations row for that datasource doesn't
// exist yet, most often because this metadata message raced the
// corresponding datasource_inventory message on first connect (see
// register.go's dispatch and #38718). The caller retries those keys; a
// later datasource_inventory INSERT never overwrites labels on conflict
// (see UpsertAgentDatasources), so once the row exists a retried UPDATE
// here still lands correctly.
//
// labelsChanged reports whether any row's labels actually changed. Identical
// labels are not rewritten, so a reconnect that re-sends unchanged metadata
// no longer bumps updated_at.
func (p *pgStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) ([]string, bool, error) {
	if len(metadata) == 0 {
		return nil, false, nil
	}

	// Look up tenant_id from agent table
	var tenantID string
	err := p.db.GetContext(ctx, &tenantID, `
		SELECT tenant FROM agent WHERE cloud_account_id = $1 AND "type" = $2
	`, accountID, agentType)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get tenant for account %s: %w", accountID, err)
	}

	var notFound []string
	changed := false
	for dsID, meta := range metadata {
		// Rows are matched by datasource_key (the dsID) where the relay wrote
		// one, which is the only reliable key: forager names need not equal
		// the ID minus its prefix (the discovery ssh sibling is ID
		// "local:x:ssh", name "x-ssh"), and cloud-pushed IDs are UUIDs.
		// The derived name remains the fallback for types without a key
		// (e.g. "local:my-postgres" → "my-postgres").
		dsName := dsID
		if idx := strings.Index(dsID, ":"); idx >= 0 {
			dsName = dsID[idx+1:]
		}

		metaJSON, err := json.Marshal(meta)
		if err != nil {
			continue
		}

		var matched, updated int64
		if err := p.db.QueryRowxContext(ctx, updateDatasourceLabelsSQL, string(metaJSON), dsName, tenantID, dsID).Scan(&matched, &updated); err != nil {
			return notFound, changed, fmt.Errorf("failed to update metadata for datasource %s: %w", dsName, err)
		}
		if matched == 0 {
			notFound = append(notFound, dsID)
		}
		if updated > 0 {
			changed = true
		}
	}

	return notFound, changed, nil
}

// proxyTypeToIntegrationType maps proxy type + datasource type to the integration type name
// used in the integrations table. For dual-mode types (db-proxy, redis-proxy) it returns
// the actual datasource type (e.g. "postgresql", "mysql", "redis") so they appear alongside
// UI-configured integrations of the same type.
func proxyTypeToIntegrationType(proxyType, dsType string) string {
	switch proxyType {
	case "db-proxy":
		// Use actual DB type so it matches the registered integration type
		// (e.g. "postgresql", "mysql", "clickhouse", "mssql", "oracle")
		if dsType != "" {
			return dsType
		}
		return ""
	case "redis-proxy":
		return "redis"
	case "http-proxy":
		// Use actual datasource type if provided (e.g. "elastic_search", "rabbitmq", "prometheus")
		// so it matches the registered integration type.
		if dsType != "" && dsType != "http" {
			return dsType
		}
		return ""
	case "mongo-proxy":
		return "mongodb_proxy"
	case "kafka-proxy":
		return "kafka_proxy"
	case "ssh-proxy":
		return "ssh"
	case "mcp-proxy":
		return "mcp"
	case "discovery-proxy":
		return "discovery"
	default:
		return ""
	}
}

// GetWorkspaceToolConfig fetches integration_config_values for the given account and
// integration type. Uses the same tables as UpsertAgentDatasources.
// configName filters to a specific integration by name (case-insensitive); empty means any.
func (p *pgStore) GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]WorkspaceConfigValue, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT icv.name, icv.value, icv.is_encrypted
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE ica.cloud_account_id = $1
		  AND i.type = $2
		  AND i.status = 'enabled'
		  AND ($3 = '' OR lower(i.name) = lower($3))
	`, accountID, integrationType, configName)
	if err != nil {
		return nil, fmt.Errorf("GetWorkspaceToolConfig: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []WorkspaceConfigValue
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return nil, fmt.Errorf("GetWorkspaceToolConfig scan: %w", err)
		}
		if isEncrypted && p.encryptionKey != "" {
			decrypted, derr := utils.Decrypt(p.encryptionKey, value)
			if derr != nil {
				slog.Error("GetWorkspaceToolConfig: failed to decrypt config value, skipping", "name", name, "err", derr)
				continue // do not expose encrypted ciphertext as plaintext
			}
			value = decrypted
		}
		result = append(result, WorkspaceConfigValue{Name: name, Value: value})
	}
	return result, rows.Err()
}

// isDualModeProxy returns true for proxy types that should have connection_mode,
// agent_type, and datasource_key config values set so the platform can route
// requests through the proxy agent.
func isDualModeProxy(proxyType string) bool {
	switch proxyType {
	case "db-proxy", "redis-proxy", "http-proxy", "ssh-proxy", "mcp-proxy", "discovery-proxy":
		return true
	default:
		return false
	}
}
