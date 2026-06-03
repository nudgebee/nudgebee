package core

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/jmoiron/sqlx"
)

// integrationKBRow is the row shape returned by the integration-KB eligibility
// query — confluence (always when enabled) and servicenow (when the
// sync_knowledge_base config flag is true). Shared by the periodic tick
// (syncIntegrationKBs) and the per-account fast path
// (EnsureIntegrationKBsForAccount).
type integrationKBRow struct {
	IntegrationID   string `db:"integration_id"`
	IntegrationType string `db:"integration_type"`
	CloudAccountID  string `db:"cloud_account_id"`
	TenantID        string `db:"tenant_id"`
	IntegrationName string `db:"integration_name"`
}

// StartIntegrationKBSync starts a background goroutine that periodically syncs integration KB entries
func StartIntegrationKBSync(ctx context.Context) {
	syncInterval := time.Duration(config.Config.KBSyncIntervalMinutes) * time.Minute
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	slog.Info("kb_sync: initialized", "interval_minutes", config.Config.KBSyncIntervalMinutes)

	// Run immediately on startup
	syncIntegrationKBs()

	for {
		select {
		case <-ctx.Done():
			slog.Info("kb_sync: stopping integration KB sync")
			return
		case <-ticker.C:
			syncIntegrationKBs()
		}
	}
}

// syncIntegrationKBs checks all integrations and creates KB entries if they don't exist
func syncIntegrationKBs() {
	slog.Info("kb_sync: starting integration KB sync")

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb_sync: failed to get database manager", "error", err)
		return
	}

	// Query for all integrations (cloud account specific and tenant level).
	// integration_name comes from integrations.name — the column api-server
	// writes the user-typed display name to (integration_config.go:419/443).
	// It is intentionally NOT mirrored into integration_config_values, so a
	// LEFT JOIN to icv_name there always returns NULL → KB names degraded to
	// "<type>_<id8>" (e.g. "confluence_a1b2c3d4") instead of the real name.
	query := `
		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ica.cloud_account_id,
			ca.tenant as tenant_id,
			COALESCE(NULLIF(i.name, ''), i.type) as integration_name
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		JOIN cloud_accounts ca ON ica.cloud_account_id = ca.id
		WHERE i.type IN ('confluence') AND i.status = 'enabled'

		UNION ALL

		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ca.id as cloud_account_id,
			i.tenant_id as tenant_id,
			COALESCE(NULLIF(i.name, ''), i.type) as integration_name
		FROM integrations i
		JOIN integration_config_values icv_sync
			ON i.id = icv_sync.integration_id
		INNER JOIN cloud_accounts ca ON i.tenant_id = ca.tenant
		WHERE i.type = 'servicenow'
		  AND i.status = 'enabled'
		  AND ca.status = 'active'
		  AND icv_sync.name = 'sync_knowledge_base' AND icv_sync.value = 'true'
	`

	var integrations []integrationKBRow
	err = dbms.Db.Select(&integrations, query)
	if err != nil {
		slog.Error("kb_sync: failed to fetch integrations", "error", err)
		return
	}

	slog.Info("kb_sync: found integrations", "count", len(integrations))

	created := 0
	skipped := 0
	errors := 0

	for _, integration := range integrations {
		// Per-iteration deadline so one hung integration cannot stall the
		// entire periodic tick. 30s covers DB checks/inserts + the rag-server
		// scrape-kick HTTP call; rag-server runs the scrape asynchronously.
		iterCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		switch ensureKBForIntegration(iterCtx, dbms, integration) {
		case kbEnsureCreated:
			created++
		case kbEnsureSkipped:
			skipped++
		case kbEnsureErrored:
			errors++
		}
		cancel()
	}

	// Reconcile: the forward pass above only inserts. Archive integration KBs
	// whose integration is no longer enabled+syncing, and reactivate ones whose
	// integration has returned — otherwise a disabled integration leaves its KB
	// stranded as active.
	eligibleIDs := make([]string, 0, len(integrations))
	for _, integration := range integrations {
		eligibleIDs = append(eligibleIDs, integration.IntegrationID)
	}
	archived, reactivated, recovered := reconcileIntegrationKBs(dbms, eligibleIDs)

	slog.Info("kb_sync: completed sync",
		"total", len(integrations),
		"created", created,
		"skipped", skipped,
		"errors", errors,
		"archived", archived,
		"reactivated", reactivated,
		"recovered", recovered)
}

// reconcileIntegrationKBs archives integration KBs whose integration is no
// longer in the eligible set, reactivates archived KBs whose integration has
// returned, and recovers KBs of any type stuck in 'processing'. eligibleIDs is
// the current set of enabled+syncing integration IDs; an empty slice skips the
// archive/reactivate passes — an empty result is treated as a transient gap,
// not a genuine "all integrations removed".
func reconcileIntegrationKBs(dbms *common.DatabaseManager, eligibleIDs []string) (archived, reactivated, recovered int) {
	// Archive / reactivate only when the eligible set is non-empty. An empty
	// set means the integrations query returned zero rows — far more likely a
	// transient gap (mid-migration, a brief disable, a JOIN hiccup) than every
	// integration genuinely removed. Archiving on an empty set would wipe every
	// integration KB across all tenants, so skip both passes instead.
	if len(eligibleIDs) > 0 {
		// Archive integration KBs whose integration dropped out of the eligible set.
		archiveQ, archiveArgs, err := sqlx.In(`UPDATE llm_knowledgebases SET status = 'archived', updated_at = NOW()
			WHERE kb_type = 'integration' AND status != 'archived' AND integration_id NOT IN (?)`, eligibleIDs)
		if err != nil {
			slog.Error("kb_sync: failed to build archive query", "error", err)
		} else if res, err := dbms.Db.Exec(dbms.Db.Rebind(archiveQ), archiveArgs...); err != nil {
			slog.Error("kb_sync: failed to archive stale integration KBs", "error", err)
		} else {
			n, _ := res.RowsAffected()
			archived = int(n)
		}

		// Reactivate archived KBs whose integration is eligible again.
		reactQ, reactArgs, err := sqlx.In(`UPDATE llm_knowledgebases SET status = 'active', updated_at = NOW()
			WHERE kb_type = 'integration' AND status = 'archived' AND integration_id IN (?)`, eligibleIDs)
		if err != nil {
			slog.Error("kb_sync: failed to build reactivate query", "error", err)
		} else if res, err := dbms.Db.Exec(dbms.Db.Rebind(reactQ), reactArgs...); err != nil {
			slog.Error("kb_sync: failed to reactivate integration KBs", "error", err)
		} else {
			n, _ := res.RowsAffected()
			reactivated = int(n)
		}
	}

	// Recover KBs stuck in 'processing'. A retrigger (and manual create/update)
	// sets 'processing' and relies on the rag-server scrape to flip it to
	// active/error; if either server restarts mid-scrape nothing clears it.
	// Scoped to every kb_type — manual KBs no longer carry a local status flip
	// either. Anything 'processing' past the staleness window is a failed load.
	if res, err := dbms.Db.Exec(`UPDATE llm_knowledgebases SET status = 'error', updated_at = NOW()
		WHERE status = 'processing'
		  AND updated_at < NOW() - make_interval(mins => $1)`,
		config.Config.KBProcessingStaleMinutes); err != nil {
		slog.Error("kb_sync: failed to recover stuck KBs", "error", err)
	} else {
		n, _ := res.RowsAffected()
		recovered = int(n)
	}

	return archived, reactivated, recovered
}

// kbEnsureResult is the per-integration outcome returned by ensureKBForIntegration.
type kbEnsureResult int

const (
	kbEnsureCreated kbEnsureResult = iota // inserted a new llm_knowledgebases row + kicked indexing
	kbEnsureSkipped                       // KB already existed (pre-check or ON CONFLICT)
	kbEnsureErrored                       // DB error reading/writing — logged inside
)

// ensureKBForIntegration creates the llm_knowledgebases row for a single
// integration if one does not already exist, and on a fresh insert kicks
// rag-server's indexing endpoint. Idempotent — safe to call repeatedly
// (existing-row pre-check + ON CONFLICT DO NOTHING handle races). Shared by
// the periodic tick (syncIntegrationKBs) and the per-account fast path
// (EnsureIntegrationKBsForAccount).
func ensureKBForIntegration(ctx context.Context, dbms *common.DatabaseManager, integration integrationKBRow) kbEnsureResult {
	// Fail-fast: the upstream SELECT joins on NOT NULL columns so these
	// should already be non-empty, but guard explicitly so a future code
	// path that builds an integrationKBRow by hand can't silently widen the
	// UPDATE / SELECT below into a cross-row scan.
	if integration.IntegrationID == "" || integration.CloudAccountID == "" {
		slog.Error("kb_sync: refusing to ensure KB with empty identifiers",
			"integration_id", integration.IntegrationID,
			"cloud_account_id", integration.CloudAccountID)
		return kbEnsureErrored
	}
	// Check if KB entry already exists for this integration
	var existingCount int
	checkQuery := `
		SELECT COUNT(*) FROM llm_knowledgebases
		WHERE integration_id = $1
		AND account_id = $2
	`
	if err := dbms.Db.GetContext(ctx, &existingCount, checkQuery, integration.IntegrationID, integration.CloudAccountID); err != nil {
		slog.Error("kb_sync: failed to check existing KB",
			"integration_id", integration.IntegrationID,
			"error", err)
		return kbEnsureErrored
	}

	// KB display name is the user-typed integration name verbatim. Uniqueness
	// of the underlying row is enforced upstream by two DB constraints, so the
	// name itself does not need to be unique:
	//   1. integrations: UNIQUE (source, type, name, tenant_id)  — V567
	//   2. llm_knowledgebases: UNIQUE (account_id, integration_id) — V622
	// Computed before the existing-row branch so an integration rename
	// refreshes the existing KB's display name in the same call.
	kbName := integration.IntegrationName
	kbDescription := "Knowledge Base for integration: " + integration.IntegrationName

	if existingCount > 0 {
		// Refresh display name + description in case the user renamed the
		// integration since the KB row was created. Without this the KB
		// list UI would keep showing the old name forever — the create
		// path is the only writer of llm_knowledgebases.name today, and
		// ON CONFLICT DO NOTHING above means a re-INSERT never refreshes
		// it. The `WHERE name != $1 OR description != $2` guard makes the
		// UPDATE a no-op (zero rows affected) on every cache-invalidation
		// where the name has not actually changed, so updated_at does not
		// churn.
		// `IS DISTINCT FROM` (not `!=`) so a hypothetical NULL value in
		// name/description doesn't make the comparison evaluate to NULL and
		// silently skip the refresh. The INSERT path never writes NULL
		// today, but defensive against schema or column-default changes.
		res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases
			SET name = $1, description = $2, updated_at = NOW()
			WHERE integration_id = $3
			  AND account_id = $4
			  AND (name IS DISTINCT FROM $1 OR description IS DISTINCT FROM $2)`,
			kbName, kbDescription, integration.IntegrationID, integration.CloudAccountID)
		if err != nil {
			// Not fatal — name refresh is best-effort. The KB stays
			// usable; only the display label is stale.
			slog.Warn("kb_sync: failed to refresh KB display name",
				"integration_id", integration.IntegrationID,
				"error", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			slog.Info("kb_sync: refreshed KB display name after integration rename",
				"integration_id", integration.IntegrationID,
				"integration_name", integration.IntegrationName,
				"kb_name", kbName)
		}
		slog.Debug("kb_sync: KB already exists",
			"integration_id", integration.IntegrationID,
			"integration_name", integration.IntegrationName)
		return kbEnsureSkipped
	}

	// Create KB entry for this integration using INSERT ... ON CONFLICT DO NOTHING
	// to handle race conditions and ensure idempotency. New rows start as
	// 'processing' — the rag-server scrape flips them to 'active' once the
	// integration's documents are indexed.
	insertQuery := `
		INSERT INTO llm_knowledgebases
		(id, tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes, status, kb_type, kb_source, integration_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, '', 'text', $5, 0, 'processing', 'integration', $6, $7, NOW(), NOW())
		ON CONFLICT (account_id, integration_id) WHERE integration_id IS NOT NULL DO NOTHING
	`

	result, err := dbms.Db.ExecContext(ctx, insertQuery,
		integration.TenantID,
		integration.CloudAccountID,
		kbName,
		kbDescription,
		integration.IntegrationType+"_"+integration.IntegrationID+"_kb.txt",
		integration.IntegrationType,
		integration.IntegrationID,
	)
	if err != nil {
		slog.Error("kb_sync: failed to create KB entry",
			"integration_id", integration.IntegrationID,
			"integration_name", integration.IntegrationName,
			"error", err)
		return kbEnsureErrored
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// ON CONFLICT DO NOTHING — another goroutine won the race.
		slog.Debug("kb_sync: KB already exists (conflict handled)",
			"integration_id", integration.IntegrationID,
			"integration_name", integration.IntegrationName)
		return kbEnsureSkipped
	}

	slog.Info("kb_sync: created KB entry",
		"integration_id", integration.IntegrationID,
		"integration_name", integration.IntegrationName,
		"integration_type", integration.IntegrationType,
		"kb_name", kbName)

	// Kick initial indexing so the new KB does not sit idle until the next
	// scrape cycle. Best-effort — the periodic scrape is the safety net if
	// this call fails. The ctx deadline bounds the rag-server HTTP call so
	// the MQ-consumer-dispatched goroutine (cache_invalidation_mq.go) can
	// never leak on a slow/hung rag-server.
	if kickErr := triggerIntegrationKBSync(ctx, integration.CloudAccountID, integration.IntegrationID, integration.IntegrationType, "system"); kickErr != nil {
		slog.Warn("kb_sync: failed to kick initial indexing",
			"integration_id", integration.IntegrationID, "error", kickErr)
	}
	return kbEnsureCreated
}

// EnsureIntegrationKBsForAccount runs the integration-KB sync scoped to a
// single cloud account. Called from the cache-invalidation consumer
// immediately after an integration mutation (create / status change /
// delete) so the KB list reflects the new state within seconds instead of
// waiting up to KBSyncIntervalMinutes (default 30) for the next periodic
// tick.
//
// Two passes over the account's integrations:
//  1. Ensure — create llm_knowledgebases rows for currently-eligible
//     integrations that don't yet have one. Idempotent (per-integration
//     pre-check + ON CONFLICT DO NOTHING).
//  2. Archive / reactivate — drive llm_knowledgebases.status to match
//     reality for this account: archive rows whose integration is no
//     longer eligible (disabled, deleted, or sync_knowledge_base flipped
//     off), reactivate rows whose integration has returned. Scoped to
//     account_id, so this does NOT race the periodic tick's full-table
//     reconcileIntegrationKBs — the two passes can interleave safely
//     because they only ever agree on the same account's rows.
//
// The periodic tick (syncIntegrationKBs + reconcileIntegrationKBs) remains
// the safety net for the "all integrations removed across every tenant"
// case and for the 'processing' staleness recovery, both of which are
// intentionally global.
func EnsureIntegrationKBsForAccount(accountId string) {
	if accountId == "" {
		return
	}

	// Bound the whole fast path: the DB query + N×ensureKBForIntegration
	// (which itself does a DB read, a DB insert, and a rag-server HTTP POST)
	// + the archive/reactivate UPDATEs. MQ consumer dispatches us in a
	// goroutine; without this deadline a hung rag-server would leak a
	// goroutine per cache-invalidation message.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb_sync: failed to get database manager",
			"account_id", accountId, "error", err)
		return
	}

	// Same eligibility predicates as syncIntegrationKBs, scoped to one
	// cloud_account_id. Branch 1 (confluence) joins via the
	// integrations_cloud_accounts mapping; branch 2 (servicenow) joins via
	// tenant and filters cloud_accounts.id directly. integration_name reads
	// from the canonical integrations.name column (see syncIntegrationKBs
	// comment for why icv_name is the wrong place).
	query := `
		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ica.cloud_account_id,
			ca.tenant as tenant_id,
			COALESCE(NULLIF(i.name, ''), i.type) as integration_name
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		JOIN cloud_accounts ca ON ica.cloud_account_id = ca.id
		WHERE i.type IN ('confluence')
		  AND i.status = 'enabled'
		  AND ica.cloud_account_id = $1

		UNION ALL

		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ca.id as cloud_account_id,
			i.tenant_id as tenant_id,
			COALESCE(NULLIF(i.name, ''), i.type) as integration_name
		FROM integrations i
		JOIN integration_config_values icv_sync
			ON i.id = icv_sync.integration_id
		INNER JOIN cloud_accounts ca ON i.tenant_id = ca.tenant
		WHERE i.type = 'servicenow'
		  AND i.status = 'enabled'
		  AND ca.status = 'active'
		  AND icv_sync.name = 'sync_knowledge_base' AND icv_sync.value = 'true'
		  AND ca.id = $1
	`

	var integrations []integrationKBRow
	if err := dbms.Db.SelectContext(ctx, &integrations, query, accountId); err != nil {
		slog.Error("kb_sync: per-account fetch failed",
			"account_id", accountId, "error", err)
		return
	}

	slog.Info("kb_sync: per-account fast path",
		"account_id", accountId, "found", len(integrations))

	// Ensure pass — create KB rows for currently-eligible integrations.
	created, skipped, errors := 0, 0, 0
	for _, integration := range integrations {
		switch ensureKBForIntegration(ctx, dbms, integration) {
		case kbEnsureCreated:
			created++
		case kbEnsureSkipped:
			skipped++
		case kbEnsureErrored:
			errors++
		}
	}

	// Archive / reactivate pass — drive existing rows for this account to
	// match the eligibility set. Unlike the full-table reconcile in the
	// periodic tick, an empty eligible set here is a legitimate state (the
	// user disabled or deleted every KB-syncing integration on this
	// account) — archive all of this account's integration KBs in that
	// case. Scoping by account_id keeps both branches race-free against
	// the periodic tick's broader UPDATEs.
	eligibleIDs := make([]string, 0, len(integrations))
	for _, integration := range integrations {
		eligibleIDs = append(eligibleIDs, integration.IntegrationID)
	}
	archived, reactivated := archiveReactivateAccountKBs(ctx, dbms, accountId, eligibleIDs)

	slog.Info("kb_sync: per-account fast path complete",
		"account_id", accountId,
		"created", created,
		"skipped", skipped,
		"errors", errors,
		"archived", archived,
		"reactivated", reactivated)
}

// archiveReactivateAccountKBs mirrors reconcileIntegrationKBs's archive +
// reactivate logic, scoped to a single account_id so the fast path can run
// after a per-account mutation without racing the periodic tick's
// full-table pass.
//
// Empty eligibleIDs is the "no KB-syncing integrations remain for this
// account" case (e.g. last integration disabled/deleted) — legitimate and
// triggers a blanket archive of this account's integration KBs. The
// periodic tick treats an empty result very differently because there it
// would imply "all integrations gone across every tenant", a much louder
// signal that's almost always a transient gap.
func archiveReactivateAccountKBs(ctx context.Context, dbms *common.DatabaseManager, accountId string, eligibleIDs []string) (archived, reactivated int) {
	if len(eligibleIDs) == 0 {
		// Archive every non-archived integration KB for this account — the
		// fast path was triggered by a confirmed mutation and the eligibility
		// query came back empty, so the account genuinely has no
		// KB-syncing integrations left. No reactivate candidates.
		res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases
			SET status = 'archived', updated_at = NOW()
			WHERE kb_type = 'integration'
			  AND account_id = $1
			  AND status != 'archived'`, accountId)
		if err != nil {
			slog.Error("kb_sync: failed to archive stale integration KBs (empty eligible set)",
				"account_id", accountId, "error", err)
			return 0, 0
		}
		n, _ := res.RowsAffected()
		return int(n), 0
	}

	// Archive integration KBs whose integration dropped out of the eligible
	// set (disable / delete / sync_knowledge_base flipped off).
	archiveQ, archiveArgs, err := sqlx.In(`UPDATE llm_knowledgebases
		SET status = 'archived', updated_at = NOW()
		WHERE kb_type = 'integration'
		  AND account_id = ?
		  AND status != 'archived'
		  AND integration_id NOT IN (?)`, accountId, eligibleIDs)
	if err != nil {
		slog.Error("kb_sync: failed to build per-account archive query",
			"account_id", accountId, "error", err)
	} else if res, err := dbms.Db.ExecContext(ctx, dbms.Db.Rebind(archiveQ), archiveArgs...); err != nil {
		slog.Error("kb_sync: failed to archive stale integration KBs",
			"account_id", accountId, "error", err)
	} else {
		n, _ := res.RowsAffected()
		archived = int(n)
	}

	// Reactivate archived KBs whose integration returned (re-enabled, or
	// sync_knowledge_base flipped back on).
	reactQ, reactArgs, err := sqlx.In(`UPDATE llm_knowledgebases
		SET status = 'active', updated_at = NOW()
		WHERE kb_type = 'integration'
		  AND account_id = ?
		  AND status = 'archived'
		  AND integration_id IN (?)`, accountId, eligibleIDs)
	if err != nil {
		slog.Error("kb_sync: failed to build per-account reactivate query",
			"account_id", accountId, "error", err)
	} else if res, err := dbms.Db.ExecContext(ctx, dbms.Db.Rebind(reactQ), reactArgs...); err != nil {
		slog.Error("kb_sync: failed to reactivate integration KBs",
			"account_id", accountId, "error", err)
	} else {
		n, _ := res.RowsAffected()
		reactivated = int(n)
	}
	return archived, reactivated
}
