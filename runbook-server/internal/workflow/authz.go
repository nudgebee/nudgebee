package workflow

import "nudgebee/runbook/services/security"

// Authorization for workflow operations is the UNION of two independent paths:
// the built-in account/namespace roles (HasAccountAccess) and dynamic-RBAC
// custom-role grants on the `workflows` module. The gateway
// (app/src/lib/actions.yaml + permissionCatalog) already admits a caller on
// either one, so a service check that consults only the built-in half turns
// every granted action into a 401 the user cannot explain — the operation is
// offered by the UI, allowed by the gateway, and refused here.
//
// These helpers are the grant half ONLY. Call sites keep their existing
// HasAccountAccess expression verbatim and OR this in, which makes the change
// provably additive: no built-in role gains or loses anything.
//
// The module name must match what classifyAction assigns to these actions
// (`workflows`) — gateway and service then agree by construction.
const workflowsModule = "workflows"

// grantedWorkflows reports whether the caller holds a custom-role grant for
// (workflows, one of classes) that applies to accountId. HasScopedPermission
// covers both flavors: a tenant-global grant (bounded to accounts in the
// caller's tenant) and an account-scoped one.
func grantedWorkflows(sc *security.SecurityContext, accountId string, classes ...string) bool {
	for _, class := range classes {
		if sc.HasScopedPermission(accountId, workflowsModule, class) {
			return true
		}
	}
	return false
}

// canWriteWorkflows: a workflows:Write grant authorizes any workflow mutation
// (update, delete, publish, version management).
func canWriteWorkflows(sc *security.SecurityContext, accountId string) bool {
	return grantedWorkflows(sc, accountId, "Write")
}

// canRunWorkflows: running, replaying, cancelling, pausing and resuming are the
// Execute class. Write is accepted too, mirroring ExecuteTask — someone who may
// rewrite a workflow can already make it do anything running it would.
func canRunWorkflows(sc *security.SecurityContext, accountId string) bool {
	return grantedWorkflows(sc, accountId, "Execute", "Write")
}

// canInspectWorkflows: the read class, for operations the gateway classifies as
// workflows:Read (validate/check) but whose built-in check is write-level.
func canInspectWorkflows(sc *security.SecurityContext, accountId string) bool {
	return grantedWorkflows(sc, accountId, "Read", "Write")
}

// hasTenantWideWorkflowsGrant reports a TENANT-GLOBAL workflows read grant
// (Write implies Read). HasPermission — not HasScopedPermission — is the right
// question: an account-scoped grant covers specific accounts and must never be
// mistaken for tenant-wide reach.
func hasTenantWideWorkflowsGrant(sc *security.SecurityContext) bool {
	return sc.HasPermission(workflowsModule, "Read") || sc.HasPermission(workflowsModule, "Write")
}

// readableWorkflowAccounts is the account set a tenant-level listing defaults to:
// the built-in account scope (ListAccountIds) UNION whatever a dynamic-RBAC
// workflows grant reaches — every account in the tenant for a tenant-global
// grant, or just the bound accounts for an account-scoped one.
//
// The union matters in both directions: ListAccountIds() is empty for a pure
// custom-role holder, and conversely a user who holds account_admin on A and a
// scoped grant on B must see both, not whichever check happens to run first.
// Order is deterministic (built-in scope first) so the resulting filter is stable.
func readableWorkflowAccounts(sc *security.SecurityContext) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(ids []string) {
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(sc.ListAccountIds())
	if hasTenantWideWorkflowsGrant(sc) {
		// Tenant-global grant: every account of the caller's tenant.
		add(sc.GetAccountIds())
	}
	add(sc.ScopedAccountIdsForModule(workflowsModule))
	return out
}
