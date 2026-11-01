package workflow

import (
	"fmt"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/services/cloud"
	"nudgebee/runbook/services/security"
)

// accountProvider is a variable so tests can supply a provider without a database.
var accountProvider = cloud.GetAccountProvider

// validateScriptAccount refuses a script step whose account cannot run on the
// executor it names — a Kubernetes cluster account on Nudgebee's own runner,
// say — so the author learns it while editing rather than from a failed run.
// A step without an account uses the workflow's. Templated values resolve at
// run time, where the task makes the same check.
func validateScriptAccount(ctx *security.RequestContext, workflowAccountID string, task model.Task) error {
	executorType, _ := task.Params["executor_type"].(string)
	if executorType == "" || strings.Contains(executorType, "{{") {
		return nil
	}
	accountID, _ := task.Params["account_id"].(string)
	if accountID == "" {
		accountID = workflowAccountID
	}
	if accountID == "" || strings.Contains(accountID, "{{") {
		return nil
	}
	provider, err := accountProvider(ctx, accountID)
	if err != nil {
		// An unknown account is reported by the account check that follows;
		// anything else still fails the step at run time with the same message.
		return nil
	}
	if err := executors.CheckAccountSupports(executorType, provider); err != nil {
		return common.ErrorBadRequest(fmt.Sprintf("task '%s': %v", task.ID, err))
	}
	return nil
}
