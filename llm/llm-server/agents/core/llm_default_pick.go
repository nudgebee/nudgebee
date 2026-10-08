package core

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// llmCandidate is one enabled LLM integration as seen from one account. The
// rules below are mirrored by rag-server (get_default_llm_integration) and
// api-server (integrations_check_llm_defaults); all three run the cases in
// testdata/llm_default_cases.json. Spec: docs/llm-default-provider-spec.md.
type llmCandidate struct {
	Id             string
	Linked         bool // linked to this account
	AccountDefault bool // marked default on this account's link
	TenantDefault  bool // the tenant default (default_llm_provider config value)
	HasAccounts    bool // linked to any account at all
}

// chooseAccountLLMIntegration returns the integration an account resolves
// against: its own default, otherwise the tenant default, otherwise "" (the
// caller falls through to ENV). It never picks by how many integrations an
// account has.
func chooseAccountLLMIntegration(accountId string, candidates []llmCandidate) string {
	var own, tenant []string
	for _, c := range candidates {
		if c.Linked && c.AccountDefault {
			own = append(own, c.Id)
		}
		if c.TenantDefault {
			tenant = append(tenant, c.Id)
		}
	}
	if len(own) == 1 {
		return own[0]
	}
	if len(own) > 1 {
		slog.Error("LLM config: account has several integrations marked default; using the tenant default",
			"accountId", accountId, "count", len(own))
	}
	if len(tenant) == 1 {
		return tenant[0]
	}
	if len(tenant) > 1 {
		slog.Error("LLM config: tenant has several tenant defaults; using ENV", "accountId", accountId, "count", len(tenant))
	}
	return ""
}

// llmPickableForAccount reports whether an account's users can choose the
// integration in the model picker: linked to the account, tenant-wide (linked
// to no account), or the tenant default.
func llmPickableForAccount(c llmCandidate) bool {
	return c.Linked || !c.HasAccounts || c.TenantDefault
}

// IsEnvLLMConfigured reports whether llm-server has a platform LLM to fall back
// to: the same condition as the env-global layer in ResolveLLMConfig.
func IsEnvLLMConfigured() bool {
	return config.Config.LlmProvider != "" && config.Config.LlmModel != ""
}

// llmDefaultsMigratedKey marks an LLM integration the cleanup below has handled.
// Not an llm_* name, so the config resolver never reads it as a setting. Saves
// write it on new integrations, so the job never touches them. Remove the job,
// this key and the save-path write in the release after this one reaches prod.
const llmDefaultsMigratedKey = "defaults_migrated"

// RegisterLLMHookLinkCleanup deletes the links the old account-creation hook
// made: each new account was linked to the tenant default and marked default on
// it. Those accounts now reach the same integration through the tenant default,
// so deleting the link changes nothing for them today and lets them follow the
// tenant default from now on. It runs in the background on the leader; a
// failure is logged and retried on the next tick and never stops llm-server.
func RegisterLLMHookLinkCleanup() error {
	return common.NewLeaderIntervalJob("llm_hook_link_cleanup", func() error {
		if err := runLLMHookLinkCleanup(); err != nil {
			slog.Error("llm-defaults: hook link cleanup failed", "error", err)
		}
		return nil
	}, time.Hour)
}

// llmHookLinkCleanupTimeout bounds one run, so a slow or locked query cannot
// hold the leader's job runner.
const llmHookLinkCleanupTimeout = 2 * time.Minute

func runLLMHookLinkCleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), llmHookLinkCleanupTimeout)
	defer cancel()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}
	var ids []string
	if err := dbms.Db.SelectContext(ctx, &ids, `
		SELECT i.id FROM integrations i
		WHERE i.type = 'llm'
		  AND NOT EXISTS (SELECT 1 FROM integration_config_values v
						  WHERE v.integration_id = i.id AND v.name = $1)`, llmDefaultsMigratedKey); err != nil {
		return err
	}
	for _, id := range ids {
		if err := cleanupLLMHookLinks(ctx, dbms, id); err != nil {
			slog.Error("llm-defaults: hook link cleanup failed for integration", "integrationId", id, "error", err)
		}
	}
	return nil
}

// cleanupLLMHookLinks handles one integration in one transaction. The record is
// inserted first, so a second pod racing on the same integration finds it and
// stops. A link counts as hook-created when the account was created after the
// integration was last saved (a save always moves updated_at forward, so no
// admin could have added it), the integration is the tenant default, the link
// is marked default, and it is the account's only LLM link.
func cleanupLLMHookLinks(ctx context.Context, dbms *common.DatabaseManager, integrationId string) error {
	tx, err := dbms.Db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO integration_config_values (integration_id, name, value, is_encrypted)
		VALUES ($1, $2, 'v1', false)
		ON CONFLICT (integration_id, name) DO NOTHING`, integrationId, llmDefaultsMigratedKey)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}

	var removed []string
	if err := tx.SelectContext(ctx, &removed, `
		DELETE FROM integrations_cloud_accounts ica
		USING integrations i, cloud_accounts ca
		WHERE ica.integration_id = $1
		  AND i.id = ica.integration_id
		  AND ca.id = ica.cloud_account_id
		  AND ca.created_at > i.updated_at
		  AND ica.default_llm_provider = true
		  AND EXISTS (SELECT 1 FROM integration_config_values d
					  WHERE d.integration_id = i.id AND d.name = 'default_llm_provider' AND d.value = 'true')
		  AND (SELECT count(*) FROM integrations_cloud_accounts x
			   JOIN integrations y ON y.id = x.integration_id
			   WHERE y.type = 'llm' AND x.cloud_account_id = ica.cloud_account_id) = 1
		RETURNING ica.cloud_account_id::text`, integrationId); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(removed) > 0 {
		slog.Info("llm-defaults: removed hook-created links", "integrationId", integrationId, "count", len(removed))
	}
	return nil
}
