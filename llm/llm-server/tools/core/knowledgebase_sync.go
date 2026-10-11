package core

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/lib/pq"
)

// integrationKBRow is the row shape returned by the integration-KB eligibility
// query: one row per integration whose knowledge should be indexed — confluence
// (enabled and linked to at least one account) and servicenow (enabled, with the
// sync_knowledge_base config flag on). Shared by the periodic tick
// (syncIntegrationKBs) and the per-tenant fast path
// (EnsureIntegrationKBsForAccount).
//
// Each integration has ONE knowledge base: an organisation row (account_id NULL,
// #40605) that every account of the tenant sees unless it is switched off for
// that account or restricted to selected accounts (llm_kb_account_overrides).
type integrationKBRow struct {
	IntegrationID   string `db:"integration_id"`
	IntegrationType string `db:"integration_type"`
	TenantID        string `db:"tenant_id"`
	IntegrationName string `db:"integration_name"`
}

// eligibleIntegrationsQuery lists the integrations that should have a knowledge
// base, optionally limited to one tenant ($1 = ” means every tenant).
//
// Confluence integrations carry no tenant of their own (integrations.tenant_id
// is not populated for them), so the tenant comes from a linked account; an
// integration with no linked account has nowhere to scrape from and is not
// eligible. An integration belongs to one tenant, so its linked accounts share
// it; the ORDER BY only makes the pick deterministic. integration_name comes from integrations.name — the column
// api-server writes the user-typed display name to (integration_config.go);
// it is intentionally NOT mirrored into integration_config_values.
const eligibleIntegrationsQuery = `
	(SELECT DISTINCT ON (i.id)
		i.id as integration_id,
		i.type as integration_type,
		ca.tenant as tenant_id,
		COALESCE(NULLIF(i.name, ''), i.type) as integration_name
	FROM integrations i
	JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
	JOIN cloud_accounts ca ON ica.cloud_account_id = ca.id
	WHERE i.type IN ('confluence') AND i.status = 'enabled'
	  AND ($1 = '' OR ca.tenant::text = $1)
	ORDER BY i.id, ca.tenant)

	UNION ALL

	SELECT
		i.id as integration_id,
		i.type as integration_type,
		i.tenant_id as tenant_id,
		COALESCE(NULLIF(i.name, ''), i.type) as integration_name
	FROM integrations i
	JOIN integration_config_values icv_sync ON i.id = icv_sync.integration_id
	WHERE i.type = 'servicenow'
	  AND i.status = 'enabled'
	  AND icv_sync.name = 'sync_knowledge_base' AND icv_sync.value = 'true'
	  AND EXISTS (SELECT 1 FROM cloud_accounts ca WHERE ca.tenant = i.tenant_id AND ca.status = 'active')
	  AND ($1 = '' OR i.tenant_id::text = $1)
`

// StartIntegrationKBSync starts a background goroutine that periodically syncs integration KB entries
func StartIntegrationKBSync(ctx context.Context) {
	syncInterval := time.Duration(config.Config.KBSyncIntervalMinutes) * time.Minute
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	slog.Info("kb_sync: initialized", "interval_minutes", config.Config.KBSyncIntervalMinutes)

	// Run immediately on startup
	syncIntegrationKBs(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("kb_sync: stopping integration KB sync")
			return
		case <-ticker.C:
			syncIntegrationKBs(ctx)
		}
	}
}

// syncIntegrationKBs ensures every eligible integration has its organisation
// knowledge base and archives the ones whose integration is no longer eligible.
func syncIntegrationKBs(ctx context.Context) {
	slog.Info("kb_sync: starting integration KB sync")

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb_sync: failed to get database manager", "error", err)
		return
	}

	var integrations []integrationKBRow
	if err := dbms.Db.SelectContext(ctx, &integrations, eligibleIntegrationsQuery, ""); err != nil {
		slog.Error("kb_sync: failed to fetch integrations", "error", err)
		return
	}

	slog.Info("kb_sync: found integrations", "count", len(integrations))

	created, skipped, errored := ensureIntegrationKBs(ctx, dbms, integrations)

	// Reconcile: the forward pass above only inserts. Archive organisation rows
	// whose integration is no longer enabled+syncing+linked and reactivate the
	// ones that returned — otherwise a disabled integration leaves its KB
	// stranded as active. An empty eligible set across every tenant is far more
	// likely a transient gap than every integration genuinely removed, so it
	// skips the archive/reactivate passes.
	archived, reactivated := 0, 0
	if len(integrations) > 0 {
		archived, reactivated = reconcileIntegrationKBs(ctx, dbms, integrationIDs(integrations), "")
	}
	recovered := recoverStuckKBs(ctx, dbms)
	invalidateKBCachesAfterStatusChange(archived+reactivated+recovered, "reconcile")

	slog.Info("kb_sync: completed sync",
		"total", len(integrations),
		"created", created,
		"skipped", skipped,
		"errors", errored,
		"archived", archived,
		"reactivated", reactivated,
		"recovered", recovered)
}

func ensureIntegrationKBs(ctx context.Context, dbms *common.DatabaseManager, integrations []integrationKBRow) (created, skipped, errored int) {
	for _, integration := range integrations {
		// Per-iteration deadline so one hung integration cannot stall the
		// whole pass. 30s covers DB checks/inserts + the rag-server
		// scrape-kick HTTP call; rag-server runs the scrape asynchronously.
		iterCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		switch ensureKBForIntegration(iterCtx, dbms, integration) {
		case kbEnsureCreated:
			created++
		case kbEnsureSkipped:
			skipped++
		case kbEnsureErrored:
			errored++
		}
		cancel()
	}
	return created, skipped, errored
}

func integrationIDs(rows []integrationKBRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.IntegrationID)
	}
	return ids
}

// invalidateKBCachesAfterStatusChange clears the KB caches wholesale after a
// bulk archive/reactivate. Those statements flip `status` on rows the caller
// never enumerates, and both caches are status-blind — a cached body keeps
// being served for an archived KB, and a cached menu keeps hiding a
// reactivated one. Clearing is coarse but these transitions only happen when
// an integration is enabled or disabled; the entries are rebuilt from the DB
// on next use.
func invalidateKBCachesAfterStatusChange(changed int, reason string) {
	if changed == 0 {
		return
	}
	if err := common.CacheClear(CacheNamespaceLlmSkillContent); err != nil {
		slog.Error("kb_sync: failed to clear skill content cache", "error", err, "reason", reason)
	}
	if err := common.CacheClear(CacheNamespaceLlmKbMapping); err != nil {
		slog.Error("kb_sync: failed to clear KB mapping cache", "error", err, "reason", reason)
	}
}

// reconcileIntegrationKBs archives the organisation rows of integrations that
// are not in eligibleIDs and reactivates the archived ones that are. tenantID
// limits both passes to one tenant; empty means every tenant.
//
// Per-account integration rows (the pre-#40605 shape) are left alone: the V1013
// migration folds them into the organisation rows and copies their status, so
// archiving them here — possibly before the migration has run — would make the
// folded rows start out archived. Every reader ignores them, so one an older
// llm-server re-creates during a rolling deploy is harmless.
func reconcileIntegrationKBs(ctx context.Context, dbms *common.DatabaseManager, eligibleIDs []string, tenantID string) (archived, reactivated int) {
	if res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases kb SET status = 'archived', updated_at = NOW()
		WHERE kb.kb_type = 'integration' AND kb.account_id IS NULL AND kb.status != 'archived'
		  AND ($2 = '' OR kb.tenant_id::text = $2)
		  AND NOT (kb.integration_id = ANY($1::uuid[]))`,
		pq.Array(eligibleIDs), tenantID); err != nil {
		slog.Error("kb_sync: failed to archive stale integration KBs", "error", err, "tenant_id", tenantID)
	} else {
		n, _ := res.RowsAffected()
		archived = int(n)
	}

	if res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases kb SET status = 'active', updated_at = NOW()
		WHERE kb.kb_type = 'integration' AND kb.account_id IS NULL AND kb.status = 'archived'
		  AND ($2 = '' OR kb.tenant_id::text = $2)
		  AND kb.integration_id = ANY($1::uuid[])`,
		pq.Array(eligibleIDs), tenantID); err != nil {
		slog.Error("kb_sync: failed to reactivate integration KBs", "error", err, "tenant_id", tenantID)
	} else {
		n, _ := res.RowsAffected()
		reactivated = int(n)
	}

	return archived, reactivated
}

// recoverStuckKBs fails KBs stuck in 'processing'. A retrigger (and manual
// create/update) sets 'processing' and relies on the rag-server scrape to flip
// it to active/error; if either server restarts mid-scrape nothing clears it.
// Scoped to every kb_type. Anything 'processing' past the staleness window is a
// failed load.
func recoverStuckKBs(ctx context.Context, dbms *common.DatabaseManager) int {
	res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases SET status = 'error', updated_at = NOW()
		WHERE status = 'processing'
		  AND updated_at < NOW() - make_interval(mins => $1)`,
		config.Config.KBProcessingStaleMinutes)
	if err != nil {
		slog.Error("kb_sync: failed to recover stuck KBs", "error", err)
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// kbEnsureResult is the per-integration outcome returned by ensureKBForIntegration.
type kbEnsureResult int

const (
	kbEnsureCreated kbEnsureResult = iota // inserted a new llm_knowledgebases row + kicked indexing
	kbEnsureSkipped                       // KB already existed (pre-check or ON CONFLICT)
	kbEnsureErrored                       // DB error reading/writing — logged inside
)

// ensureKBForIntegration creates the integration's organisation knowledge base
// if it does not exist yet, refreshes its name after an integration rename, and
// on a fresh insert kicks rag-server's indexing endpoint. Idempotent — safe to
// call repeatedly (existing-row pre-check + ON CONFLICT DO NOTHING handle races).
func ensureKBForIntegration(ctx context.Context, dbms *common.DatabaseManager, integration integrationKBRow) kbEnsureResult {
	if integration.IntegrationID == "" || integration.TenantID == "" {
		slog.Error("kb_sync: refusing to ensure KB with empty identifiers",
			"integration_id", integration.IntegrationID,
			"tenant_id", integration.TenantID)
		return kbEnsureErrored
	}

	// KB display name is the user-typed integration name verbatim; it need not
	// be unique (idx_kb_name_tenant_unique covers manual organisation rows only).
	kbName := integration.IntegrationName
	kbDescription := "Knowledge Base for integration: " + integration.IntegrationName

	var existingID string
	switch err := dbms.Db.GetContext(ctx, &existingID, `SELECT id FROM llm_knowledgebases
		WHERE integration_id = $1 AND account_id IS NULL`, integration.IntegrationID); {
	case err == nil:
		// Refresh display name + description in case the user renamed the
		// integration. The IS DISTINCT FROM guard makes this a no-op when
		// nothing changed, so updated_at does not churn.
		res, err := dbms.Db.ExecContext(ctx, `UPDATE llm_knowledgebases
			SET name = $1, description = $2, updated_at = NOW()
			WHERE id = $3
			  AND (name IS DISTINCT FROM $1 OR description IS DISTINCT FROM $2)`,
			kbName, kbDescription, existingID)
		if err != nil {
			// Not fatal — the KB stays usable; only the display label is stale.
			slog.Warn("kb_sync: failed to refresh KB display name",
				"integration_id", integration.IntegrationID, "error", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			// Skill bodies and menus are cached per account under the KB's
			// name, and every account of the tenant sees this KB.
			invalidateOrgKnowledgeCaches(integration.TenantID)
			slog.Info("kb_sync: refreshed KB display name after integration rename",
				"integration_id", integration.IntegrationID, "kb_name", kbName)
		}
		return kbEnsureSkipped
	case errors.Is(err, sql.ErrNoRows):
	default:
		slog.Error("kb_sync: failed to check existing KB",
			"integration_id", integration.IntegrationID, "error", err)
		return kbEnsureErrored
	}

	// New rows start as 'processing' — the rag-server scrape flips them to
	// 'active' once the integration's documents are indexed.
	result, err := dbms.Db.ExecContext(ctx, `INSERT INTO llm_knowledgebases
		(id, tenant_id, account_id, visibility, name, description, data, data_format, data_filename, data_size_bytes, status, kb_type, kb_source, integration_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, NULL, 'organisation', $2, $3, '', 'text', $4, 0, 'processing', 'integration', $5, $6, NOW(), NOW())
		ON CONFLICT (integration_id) WHERE account_id IS NULL AND integration_id IS NOT NULL DO NOTHING`,
		integration.TenantID,
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
	if n, _ := result.RowsAffected(); n == 0 {
		// ON CONFLICT DO NOTHING — another replica won the race.
		return kbEnsureSkipped
	}

	slog.Info("kb_sync: created KB entry",
		"integration_id", integration.IntegrationID,
		"integration_name", integration.IntegrationName,
		"integration_type", integration.IntegrationType,
		"kb_name", kbName)
	invalidateOrgKnowledgeCaches(integration.TenantID)

	// Kick initial indexing so the new KB does not sit idle until the next
	// scrape cycle. Best-effort — the periodic scrape is the safety net. The
	// ctx deadline bounds the rag-server HTTP call. rag-server scrapes by
	// integration id; the account is not used.
	if kickErr := triggerIntegrationKBSync(ctx, "", integration.IntegrationID, integration.IntegrationType, "system"); kickErr != nil {
		slog.Warn("kb_sync: failed to kick initial indexing",
			"integration_id", integration.IntegrationID, "error", kickErr)
	}
	return kbEnsureCreated
}

// EnsureIntegrationKBsForAccount runs the integration-KB sync for the tenant
// of one cloud account. Called from the cache-invalidation consumer right after
// an integration mutation (create / status change / delete / link change) so the
// KB list reflects the new state within seconds instead of waiting up to
// KBSyncIntervalMinutes (default 30) for the next periodic tick.
//
// The knowledge base belongs to the tenant, so the whole tenant is reconciled.
// Unlike the periodic tick, an empty eligible set here is a legitimate state
// (the tenant disabled or deleted every KB-syncing integration), and archives
// all of the tenant's integration knowledge bases.
func EnsureIntegrationKBsForAccount(accountId string) {
	if accountId == "" {
		return
	}

	// Bounds the whole fast path; the consumer dispatches it in a goroutine, and
	// without a deadline a hung rag-server would leak one per message.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("kb_sync: failed to get database manager", "account_id", accountId, "error", err)
		return
	}

	var tenantID string
	if err := dbms.Db.GetContext(ctx, &tenantID, `SELECT tenant::text FROM cloud_accounts WHERE id = $1`, accountId); err != nil {
		slog.Error("kb_sync: failed to resolve the account's tenant", "account_id", accountId, "error", err)
		return
	}

	var integrations []integrationKBRow
	if err := dbms.Db.SelectContext(ctx, &integrations, eligibleIntegrationsQuery, tenantID); err != nil {
		slog.Error("kb_sync: per-tenant fetch failed", "tenant_id", tenantID, "error", err)
		return
	}

	created, skipped, errored := ensureIntegrationKBs(ctx, dbms, integrations)
	archived, reactivated := reconcileIntegrationKBs(ctx, dbms, integrationIDs(integrations), tenantID)
	if archived+reactivated > 0 {
		invalidateOrgKnowledgeCaches(tenantID)
	}

	slog.Info("kb_sync: per-tenant fast path complete",
		"account_id", accountId,
		"tenant_id", tenantID,
		"found", len(integrations),
		"created", created,
		"skipped", skipped,
		"errors", errored,
		"archived", archived,
		"reactivated", reactivated)
}
