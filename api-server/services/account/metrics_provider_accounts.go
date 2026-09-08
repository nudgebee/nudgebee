package account

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

// ListActiveAccountsWithUserPrometheusDefault lists a tenant's active accounts
// whose default metrics provider is a Prometheus the user connected directly —
// the accounts that have metrics to discover from but no agent for
// ListActiveAccountsWithConnectedAgents to find.
func ListActiveAccountsWithUserPrometheusDefault(ctx *security.RequestContext, tenantId string) ([]models.Account, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.Account{}, err
	}
	rows, err := databaseManager.Db.Queryx(`SELECT DISTINCT ca.id, ca.cloud_provider, ca.account_number, ca.account_name,
			ca.created_at, ca.created_by, ca.updated_at, ca.updated_by, ca.billing_source, ca.start_date,
			ca.tenant, ca.assume_role, ca.region, ca.status, ca.account_url, ca.budget, ca.synced_at,
			ca.sync_status, ca.account_access, ca.account_purpose, ca.data, ca.access_key,
			ca.access_secret, ca.account_type, ca.agent_access_key, ca.agent_access_secret,
			ca.agent_synced_at, ca.sync_status_message, ca.external_id, ca.etl_attempt,
			ca.parent_account_id, ca.access_secret_v2, ca.account_env
		FROM cloud_accounts ca
		JOIN integrations_cloud_accounts ica ON ica.cloud_account_id = ca.id
		JOIN integrations i ON i.id = ica.integration_id
		WHERE ca.status = 'active' AND ca.tenant = $1
			AND ica.default_metrics_provider = true
			AND i.type = 'prometheus' AND i.source = 'user' AND i.status != 'disabled'`, tenantId)
	if err != nil {
		return []models.Account{}, err
	}
	defer func(ctx *security.RequestContext) {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Error closing rows", "error", err)
		}
	}(ctx)

	accounts := make([]models.Account, 0)
	for rows.Next() {
		var row = models.Account{}
		if err := rows.StructScan(&row); err != nil {
			return nil, err
		}
		accounts = append(accounts, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}
