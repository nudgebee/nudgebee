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

	// archive-all-statuses: decommission, not a re-scan. The account has stopped
	// being a scan target, so every finding it owns is retired regardless of who
	// owned its status, and nothing re-upserts them afterwards to reopen one.
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

// VMActiveRequest is security_update_vm_active's input. IsActive is a pointer so
// a missing field fails validation instead of silently decommissioning.
type VMActiveRequest struct {
	AccountId       string `json:"account_id" mapstructure:"account_id" validate:"required"`
	CloudResourceId string `json:"cloud_resource_id" mapstructure:"cloud_resource_id" validate:"required"`
	IsActive        *bool  `json:"is_active" mapstructure:"is_active" validate:"required"`
}

// UpdateVMActive decommissions (IsActive=false) or recommissions (IsActive=true) one
// VM. Decommissioning soft-retires a VM that has left the network: the
// cloud_resourses row goes inactive, its packages go inactive, and its open
// vulnerability findings are archived. Nothing is deleted.
//
// Manual on purpose. No scan signal says a VM is gone — the sweep only probes
// a few management ports and a stopped forager looks identical to a departed
// host — so the operator decides, and the decision sticks: the sweep skips a
// decommissioned host even while it still answers (see markVMSeen).
//
// Recommissioning only flips the VM row back to active; its packages and
// findings return with the next sweep's scan, since the sweep skipped it while
// it was decommissioned.
//
// Limited to rows in a SelfHosted account: rows under a cloud account belong to
// cloud-collector, which retires them itself. Both the vm-<ip> placeholders and
// the cloud-shaped rows resolveByIdentity files there are VMs of this page.
//
// The row is marked meta.nb_decommissioned so the sweep can tell an operator's
// decision from cloud-collector retiring an instance (see resolveByIdentity).
func UpdateVMActive(ctx *security.RequestContext, req VMActiveRequest) error {
	if !ctx.GetSecurityContext().CanAccessAccount(req.AccountId, security.SecurityAccessTypeCreate) {
		return common.ErrorUnauthorized("unauthorized")
	}

	a, err := account.GetAccount(ctx, req.AccountId)
	if err != nil {
		return err
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if a.Id == "" || a.Tenant != tenantID {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: account %s not found for this tenant", req.AccountId))
	}
	if a.CloudProvider != account.AccountProviderSelfHosted {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: account %s is not a self-hosted account", req.AccountId))
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if *req.IsActive {
			return nil, reactivateVM(tx, tenantID, req.AccountId, req.CloudResourceId)
		}
		return nil, retireVM(tx, tenantID, req.AccountId, req.CloudResourceId)
	})
	if err != nil {
		return fmt.Errorf("vmpackage: update VM active: %w", err)
	}
	return nil
}

// retireVM is UpdateVMActive's decommission write set, split out so it runs in one
// transaction and can be tested without the account lookup.
func retireVM(tx *sqlx.Tx, tenantID, accountID, cloudResourceID string) error {
	res, err := tx.Exec(
		`UPDATE cloud_resourses
		 SET is_active = false, status = 'Inactive', updated_at = NOW(),
		     meta = COALESCE(meta::jsonb, '{}'::jsonb) || '{"nb_decommissioned": true}'::jsonb
		 WHERE id = $1 AND account = $2 AND tenant = $3`,
		cloudResourceID, accountID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("retire VM %s: %w", cloudResourceID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: VM %s not found for this account", cloudResourceID))
	}

	if _, err := tx.Exec(
		`UPDATE vm_package SET is_active = false, updated_at = NOW()
		 WHERE tenant_id = $1 AND cloud_resource_id = $2 AND is_active`,
		tenantID, cloudResourceID,
	); err != nil {
		return fmt.Errorf("archive packages of %s: %w", cloudResourceID, err)
	}

	// archive-all-statuses: decommission, not a re-scan. The operator has said
	// the VM is gone, so every finding on it is retired whoever owned its status.
	if _, err := tx.Exec(
		`UPDATE recommendation SET status = 'Archive', updated_at = NOW()
		 WHERE tenant_id = $1 AND cloud_account_id = $2 AND resource_id = $3
		   AND rule_name = $4 AND category = $5 AND status != 'Archive'`,
		tenantID, accountID, cloudResourceID, recommendationRuleName, recommendationCategory,
	); err != nil {
		return fmt.Errorf("archive findings of %s: %w", cloudResourceID, err)
	}
	return nil
}

// reactivateVM undoes retireVM's row change. Packages and findings are not
// restored here; the next scan of the host writes them.
func reactivateVM(tx *sqlx.Tx, tenantID, accountID, cloudResourceID string) error {
	res, err := tx.Exec(
		`UPDATE cloud_resourses
		 SET is_active = true, status = 'Active', updated_at = NOW(),
		     meta = COALESCE(meta::jsonb, '{}'::jsonb) - 'nb_decommissioned'
		 WHERE id = $1 AND account = $2 AND tenant = $3`,
		cloudResourceID, accountID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("reactivate VM %s: %w", cloudResourceID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return common.ErrorBadRequest(fmt.Sprintf("vmpackage: VM %s not found for this account", cloudResourceID))
	}
	return nil
}
