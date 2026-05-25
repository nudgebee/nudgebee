package core

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/jmoiron/sqlx"
)

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

	// Query for all integrations (cloud account specific and tenant level)
	query := `
		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ica.cloud_account_id,
			ca.tenant as tenant_id,
			COALESCE(icv_name.value, i.type) as integration_name
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		JOIN cloud_accounts ca ON ica.cloud_account_id = ca.id
		LEFT JOIN integration_config_values icv_name
			ON i.id = icv_name.integration_id
			AND icv_name.name = 'integration_config_name'
		WHERE i.type IN ('confluence') AND i.status = 'enabled'

		UNION ALL

		SELECT DISTINCT
			i.id as integration_id,
			i.type as integration_type,
			ca.id as cloud_account_id,
			i.tenant_id as tenant_id,
			COALESCE(icv_name.value, i.type) as integration_name
		FROM integrations i
		LEFT JOIN integration_config_values icv_name
			ON i.id = icv_name.integration_id AND icv_name.name = 'integration_config_name'
		JOIN integration_config_values icv_sync
			ON i.id = icv_sync.integration_id
		INNER JOIN cloud_accounts ca ON i.tenant_id = ca.tenant
		WHERE i.type = 'servicenow'
		  AND i.status = 'enabled'
		  AND ca.status = 'active'
		  AND icv_sync.name = 'sync_knowledge_base' AND icv_sync.value = 'true'
	`

	type IntegrationRow struct {
		IntegrationID   string `db:"integration_id"`
		IntegrationType string `db:"integration_type"`
		CloudAccountID  string `db:"cloud_account_id"`
		TenantID        string `db:"tenant_id"`
		IntegrationName string `db:"integration_name"`
	}

	var integrations []IntegrationRow
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
		// Check if KB entry already exists for this integration
		var existingCount int
		checkQuery := `
			SELECT COUNT(*) FROM llm_knowledgebases
			WHERE integration_id = $1
			AND account_id = $2
		`
		err = dbms.Db.Get(&existingCount, checkQuery, integration.IntegrationID, integration.CloudAccountID)
		if err != nil {
			slog.Error("kb_sync: failed to check existing KB",
				"integration_id", integration.IntegrationID,
				"error", err)
			errors++
			continue
		}

		if existingCount > 0 {
			slog.Debug("kb_sync: KB already exists",
				"integration_id", integration.IntegrationID,
				"integration_name", integration.IntegrationName)
			skipped++
			continue
		}

		// Create unique KB name using integration_id to avoid duplicates
		// when multiple integrations of the same type exist for an account
		kbName := integration.IntegrationName + "_" + integration.IntegrationID[:8]

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

		result, err := dbms.Db.Exec(insertQuery,
			integration.TenantID,
			integration.CloudAccountID,
			kbName,
			"Knowledge Base for integration: "+integration.IntegrationName,
			integration.IntegrationType+"_"+integration.IntegrationID+"_kb.txt",
			integration.IntegrationType,
			integration.IntegrationID,
		)

		if err != nil {
			slog.Error("kb_sync: failed to create KB entry",
				"integration_id", integration.IntegrationID,
				"integration_name", integration.IntegrationName,
				"error", err)
			errors++
		} else {
			rowsAffected, _ := result.RowsAffected()
			if rowsAffected > 0 {
				slog.Info("kb_sync: created KB entry",
					"integration_id", integration.IntegrationID,
					"integration_name", integration.IntegrationName,
					"integration_type", integration.IntegrationType,
					"kb_name", kbName)
				created++
				// Kick initial indexing so the new KB does not sit idle until
				// the next scrape cycle. Best-effort — the periodic scrape is
				// the safety net if this call fails.
				if kickErr := triggerIntegrationKBSync(integration.CloudAccountID, integration.IntegrationID, integration.IntegrationType, "system"); kickErr != nil {
					slog.Warn("kb_sync: failed to kick initial indexing",
						"integration_id", integration.IntegrationID, "error", kickErr)
				}
			} else {
				slog.Debug("kb_sync: KB already exists (conflict handled)",
					"integration_id", integration.IntegrationID,
					"integration_name", integration.IntegrationName)
				skipped++
			}
		}
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
