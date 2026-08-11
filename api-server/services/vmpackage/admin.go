package vmpackage

import (
	"database/sql"
	"errors"
	"fmt"

	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

// DiscoveryTargetRequest is integrations_upsert_discovery_target's input:
// which discovery integration, and which cloud account its network scan
// target belongs to. See cron.go's ListDiscoveryDatasources doc comment for
// why this association has to exist before resource_match.go can match
// anything — a discovery integration is filed under the forager agent's own
// account by default (usually Kubernetes/self-hosted), never the customer's
// actual AWS/Azure/GCP account whose network it scans.
type DiscoveryTargetRequest struct {
	IntegrationId  string `json:"integration_id" mapstructure:"integration_id" validate:"required"`
	CloudAccountId string `json:"cloud_account_id" mapstructure:"cloud_account_id" validate:"required"`
}

// UpsertDiscoveryTarget associates integration_id (a type='discovery'
// integration) with cloud_account_id as its scan target — a second
// integrations_cloud_accounts row (link_role='discovery_target'), alongside
// the agent's own account row (link_role='own') UpsertAgentDatasources
// already maintains for every discovery integration. Enforces exactly one
// target account per datasource by clearing any prior association first —
// issue #36059 leaves splitting one datasource's CIDRs across multiple
// target accounts as an unresolved question, deliberately out of scope
// here. No self-serve UI exists for this yet; this RPC is the only way to
// set it for now.
func UpsertDiscoveryTarget(ctx *security.RequestContext, req DiscoveryTargetRequest) error {
	tenantID := ctx.GetSecurityContext().GetTenantId()

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	var integrationType string
	if err := dbms.Db.Get(&integrationType,
		`SELECT type FROM integrations WHERE id = $1 AND tenant_id = $2`,
		req.IntegrationId, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return common.ErrorBadRequest(fmt.Sprintf("vmpackage: integration %s not found for this tenant", req.IntegrationId))
		}
		return fmt.Errorf("vmpackage: lookup integration %s: %w", req.IntegrationId, err)
	}
	if integrationType != "discovery" {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: integration %s is not a discovery datasource", req.IntegrationId))
	}

	targetAccount, err := account.GetAccount(ctx, req.CloudAccountId)
	if err != nil {
		return err
	}
	if targetAccount.Id == "" || targetAccount.Tenant != tenantID {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: cloud account %s not found for this tenant", req.CloudAccountId))
	}

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(
			`DELETE FROM integrations_cloud_accounts WHERE integration_id = $1 AND tenant_id = $2 AND link_role = 'discovery_target'`,
			req.IntegrationId, tenantID,
		); err != nil {
			return nil, fmt.Errorf("clear prior discovery target: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO integrations_cloud_accounts (integration_id, cloud_account_id, tenant_id, link_role) VALUES ($1, $2, $3, 'discovery_target')`,
			req.IntegrationId, req.CloudAccountId, tenantID,
		); err != nil {
			return nil, fmt.Errorf("set discovery target: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("vmpackage: upsert discovery target: %w", err)
	}
	return nil
}
