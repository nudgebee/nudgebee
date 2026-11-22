package ai

import (
	"errors"
	"fmt"
	"strings"

	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"nudgebee/runbook/services/security"
)

// accountParamFieldName is the optional per-step account override shared by the
// AI steps that can work on another account of the tenant (llm.nubi,
// llm.investigate, llm.mcp_call). Same name the non-AI tasks use, so the builder
// renders the same Account field.
const accountParamFieldName = "account_id"

// workflowsModule must match the module the workflow service authorizes on
// (internal/workflow/authz.go), so a custom-role grant reads the same here.
const workflowsModule = "workflows"

// Variables so tests can resolve an account and a user's access without a
// database or the services-server.
var (
	validateAccountInTenant = cloud.ValidateAccount
	securityContextForUser  = security.NewSecurityContext
)

// accountInputSchemaProperty returns the InputSchema entry for the optional
// account override. what completes "Account to ..." for the step.
func accountInputSchemaProperty(order int, what string) types.Property {
	return types.Property{
		Type:        types.PropertyTypeAccount,
		Title:       "Account",
		Description: fmt.Sprintf("Account to %s. Leave empty to use the automation's own account. Accepts an expression such as {{ event.cloud_account_id }}.", what),
		Help: "The step works in this account instead of the automation's own. Runs act with the access of the automation's last editor, " +
			"so that person must be able to read the account. AI usage counts against this account's budget and the conversation is stored there; " +
			"steps that target different accounts never share a conversation, so a workflow-wide session splits into one conversation per account. " +
			"Pre-approved tools cannot be used on another account.",
		Required: false,
		Order:    order,
	}
}

// isRealUser reports whether a run carries a person's identity. Workflow runs
// carry the workflow's last editor whatever triggered them (executor.go sets
// _user_id from wf.UpdatedBy); dry runs and the Task Runner carry the caller.
// Hook actions carry none, and a workflow nobody has edited carries the system
// user.
func isRealUser(userID string) bool {
	return userID != "" && userID != security.GetSystemUserId()
}

// resolveTaskAccount returns the account the step runs against: the optional
// account_id param, else the workflow's own account.
//
// The param is usually a template, which save-time validation cannot check, so
// a target other than the workflow's account is verified here: it must belong
// to the tenant and be readable by the user the run acts as. A run with no
// such user (a hook action) is refused rather than let through on a tenant
// check alone — llm-server would treat it as a tenant-wide admin, and
// llm.mcp_call never reaches llm-server at all.
func resolveTaskAccount(taskCtx types.TaskContext, params map[string]any) (string, error) {
	own := taskCtx.GetAccountID()
	raw, ok := params[accountParamFieldName]
	if !ok || raw == nil {
		return own, nil
	}
	target, isString := raw.(string)
	if !isString {
		return "", fmt.Errorf("%s must be a string, got %T", accountParamFieldName, raw)
	}
	target = strings.TrimSpace(target)
	if target == "" || target == own {
		return own, nil
	}

	userID := taskCtx.GetUserID()
	if !isRealUser(userID) {
		return "", fmt.Errorf("%s: this step runs without a user, so it can only use the automation's own account", accountParamFieldName)
	}
	if err := validateAccountInTenant(taskCtx.GetNewRequestContext(), target); err != nil {
		return "", fmt.Errorf("%s: %w", accountParamFieldName, err)
	}
	sc, err := securityContextForUser(taskCtx.GetTenantID(), userID)
	if err != nil || sc == nil {
		return "", fmt.Errorf("%s: unable to verify access to account '%s'", accountParamFieldName, target)
	}
	if !sc.CanReadAccountData(target, workflowsModule) {
		return "", fmt.Errorf("%s: account '%s' is not accessible to the user this automation runs as (its last editor)", accountParamFieldName, target)
	}
	return target, nil
}

// rejectCrossAccountApprovals stops a step from carrying pre-approved tools
// into another account. approved_tools pre-answers llm-server's confirmation
// before a create/update/delete tool call; the access check above only
// establishes that the account may be read.
func rejectCrossAccountApprovals(taskCtx types.TaskContext, accountID string, approvedTools map[string]string) error {
	if len(approvedTools) == 0 || accountID == taskCtx.GetAccountID() {
		return nil
	}
	return errors.New("approved_tools cannot be used when the step targets another account; remove the pre-approved tools or leave account_id empty")
}
