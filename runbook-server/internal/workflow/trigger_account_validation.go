package workflow

import (
	"fmt"

	"github.com/google/uuid"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// tenantForAccount is a variable so tests can resolve an account's tenant without a database.
var tenantForAccount = security.GetTenantIdFromAccountId

// validateTriggerAccounts refuses a trigger that listens to an account outside the
// caller's tenant or one the caller cannot read. The registry trusts the stored list,
// so this is the only gate on which accounts' events can start the workflow.
// Reading an account is a built-in role with Read on it, or a Read or Write grant
// of the action's module on it (SecurityContext.CanReadAccount).
func validateTriggerAccounts(ctx *security.RequestContext, workflowAccountID string, triggers []model.Trigger) error {
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return common.ErrorBadRequest("unauthorized")
	}
	for _, t := range triggers {
		for _, id := range t.ListenAccounts(workflowAccountID) {
			if id == workflowAccountID {
				continue
			}
			// Registry SQL compares the canonical text form, so only that form can ever match.
			if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
				return common.ErrorBadRequest(fmt.Sprintf("the %s trigger lists '%s', but an account ID is expected", t.Type, id))
			}
			// Super admins pass HasAccountAccess for any account, so the tenant is checked too.
			tenantID, err := tenantForAccount(id)
			if err != nil || tenantID == "" || tenantID != sc.GetTenantId() ||
				!sc.CanReadAccount(id) {
				return common.ErrorBadRequest(fmt.Sprintf("the %s trigger listens to account '%s', which is not accessible", t.Type, id))
			}
		}
	}
	return nil
}
