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
		// Capture the account we are about to stop targeting, before the
		// DELETE below drops the only record of it.
		var priorAccountIDs []string
		if err := tx.Select(&priorAccountIDs,
			`SELECT cloud_account_id::text FROM integrations_cloud_accounts
			 WHERE integration_id = $1 AND tenant_id = $2 AND link_role = 'discovery_target'`,
			req.IntegrationId, tenantID,
		); err != nil {
			return nil, fmt.Errorf("read prior discovery target: %w", err)
		}

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

		for _, prior := range priorAccountIDs {
			if prior == req.CloudAccountId {
				continue // re-asserting the same target: nothing was orphaned
			}
			if err := retireOrphanedVMScanArtifacts(tx, tenantID, prior); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("vmpackage: upsert discovery target: %w", err)
	}
	return nil
}

// retireOrphanedVMScanArtifacts retires the VM-scan output left behind under
// priorAccountID once no discovery datasource targets it any more.
//
// Re-pointing a datasource used to strand everything the previous target had
// accumulated: nothing scans that account again, so its findings and package
// inventory sit at whatever the last scan wrote, forever, in the same tenant as
// the correct rows. Observed live — an EC2 instance under a Kubernetes account
// still served 140 findings (105 of them false positives from #36278) three
// days after the same instance had been rescanned and corrected under the
// datasource's new target.
//
// Deliberately narrow, because "this account is no longer a discovery target"
// does not mean "this account is dead":
//
//   - recommendation rows are archived, never deleted, and only for
//     rule_name = 'vm_package_vulnerability' — the one rule this pipeline
//     writes. Every other rule's findings on those resources are untouched.
//   - vm_package rows are marked inactive rather than removed, matching how
//     upsertPackages already archives a package that has gone away.
//   - cloud_resourses is touched only for "vm-<ip>" placeholders, which this
//     package provably creates itself (see findOrCreateVMResource). Rows
//     created by resolveByIdentity carry a real cloud instance id and are
//     indistinguishable from cloud-collector's own, so retiring them could
//     deactivate live infrastructure in an account that is still perfectly
//     real — it just is not a scan target any more.
//
// Skipped entirely when another datasource still targets the account, since
// one target account may serve several datasources; only the last one to leave
// orphans anything.
func retireOrphanedVMScanArtifacts(tx *sqlx.Tx, tenantID, priorAccountID string) error {
	var stillTargeted int
	if err := tx.Get(&stillTargeted,
		`SELECT count(*) FROM integrations_cloud_accounts
		 WHERE cloud_account_id = $1 AND tenant_id = $2 AND link_role = 'discovery_target'`,
		priorAccountID, tenantID,
	); err != nil {
		return fmt.Errorf("check remaining discovery targets for %s: %w", priorAccountID, err)
	}
	if stillTargeted > 0 {
		return nil
	}

	if _, err := tx.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = NOW()
		 WHERE tenant_id = $1 AND cloud_account_id = $2
		   AND rule_name = $3 AND category = $4 AND status != 'Archive'`,
		tenantID, priorAccountID, recommendationRuleName, recommendationCategory,
	); err != nil {
		return fmt.Errorf("archive orphaned findings for %s: %w", priorAccountID, err)
	}

	if _, err := tx.Exec(
		`UPDATE vm_package SET is_active = false, updated_at = NOW()
		 WHERE tenant_id = $1 AND cloud_account_id = $2 AND is_active`,
		tenantID, priorAccountID,
	); err != nil {
		return fmt.Errorf("archive orphaned packages for %s: %w", priorAccountID, err)
	}

	if _, err := tx.Exec(
		`UPDATE cloud_resourses SET is_active = false, updated_at = NOW()
		 WHERE tenant = $1 AND account = $2 AND resourse_id LIKE $3 AND is_active IS NOT FALSE`,
		tenantID, priorAccountID, vmResourceIDPrefix+"%",
	); err != nil {
		return fmt.Errorf("retire orphaned placeholders for %s: %w", priorAccountID, err)
	}

	return nil
}
