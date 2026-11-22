package workflow

import (
	"fmt"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

const mcpCallTaskType = "llm.mcp_call"

// aiAccountStepTypes are the AI steps whose optional account_id points an
// agent (or an MCP call) at another account of the tenant.
var aiAccountStepTypes = map[string]bool{"llm.nubi": true, "llm.investigate": true, mcpCallTaskType: true}

// validateAIStepAccounts applies validateAIStepAccount to every AI step in the
// definition: tasks however deeply nested, and hook actions. It walks on its
// own rather than riding validateTaskTypes' recursion, which only descends
// into group and foreach — a step inside an llm.router or legacy core.switch
// branch, or in a hook, would otherwise skip the check entirely.
func validateAIStepAccounts(ctx *security.RequestContext, workflowAccountID string, def model.WorkflowDefinition) error {
	if err := validateAIHookAccounts(ctx, workflowAccountID, "workflow", def.Hooks); err != nil {
		return err
	}
	return validateAITaskAccounts(ctx, workflowAccountID, def.Tasks)
}

func validateAITaskAccounts(ctx *security.RequestContext, workflowAccountID string, tasks []model.Task) error {
	for _, task := range tasks {
		if aiAccountStepTypes[task.Type] {
			if err := validateAIStepAccount(ctx, workflowAccountID, task); err != nil {
				return err
			}
		}
		if err := validateAIHookAccounts(ctx, workflowAccountID, task.ID, task.Hooks); err != nil {
			return err
		}
		if err := validateAITaskAccounts(ctx, workflowAccountID, task.Tasks); err != nil {
			return err
		}
		if err := validateEmbeddedAIStepAccounts(ctx, workflowAccountID, task.Params); err != nil {
			return err
		}
	}
	return nil
}

// validateAIHookAccounts checks the hook actions of a task or of the workflow
// (owner names which, for the error message).
func validateAIHookAccounts(ctx *security.RequestContext, workflowAccountID, owner string, hooks *model.Hooks) error {
	if hooks == nil {
		return nil
	}
	for _, actions := range [][]model.Action{hooks.Success, hooks.Failure, hooks.Always} {
		for _, action := range actions {
			if !aiAccountStepTypes[action.Type] {
				continue
			}
			step := model.Task{ID: owner + " hook", Type: action.Type, Params: action.Params}
			if err := validateAIStepAccount(ctx, workflowAccountID, step); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateEmbeddedAIStepAccounts finds AI steps embedded anywhere in a
// container's params (params.tasks, params.branches[].tasks, ...). It matches
// on the task shape rather than on known container keys, so a new container
// type is covered without touching this file.
func validateEmbeddedAIStepAccounts(ctx *security.RequestContext, workflowAccountID string, value any) error {
	switch v := value.(type) {
	case []model.Task:
		return validateAITaskAccounts(ctx, workflowAccountID, v)
	case []any:
		for _, item := range v {
			if err := validateEmbeddedAIStepAccounts(ctx, workflowAccountID, item); err != nil {
				return err
			}
		}
	case map[string]any:
		if taskType, _ := v["type"].(string); aiAccountStepTypes[taskType] {
			id, _ := v["id"].(string)
			params, _ := v["params"].(map[string]any)
			if err := validateAIStepAccount(ctx, workflowAccountID, model.Task{ID: id, Type: taskType, Params: params}); err != nil {
				return err
			}
		}
		for _, child := range v {
			if err := validateEmbeddedAIStepAccounts(ctx, workflowAccountID, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// isTemplated reports whether a param value is rendered at run time, so that
// nothing about it can be checked while saving.
func isTemplated(value string) bool {
	return strings.Contains(value, "{{") || strings.Contains(value, "{%")
}

// literalStepAccount returns the step's account_id when it is a plain value
// naming an account other than the workflow's own. Empty, templated (resolved
// and checked at run time) and own-account values all return "".
func literalStepAccount(workflowAccountID string, task model.Task) string {
	target, _ := task.Params["account_id"].(string)
	target = strings.TrimSpace(target)
	if target == "" || target == workflowAccountID || isTemplated(target) {
		return ""
	}
	return target
}

// validateAIStepAccount refuses an AI step aimed at an account outside the
// caller's tenant or one the caller cannot read. The generic account check
// only confirms tenant membership, which is not enough here: these steps hand
// a whole account's tools and data to an agent, so whoever saves the step must
// be able to read the account it targets. Custom-role grants count, as
// everywhere in this module (see authz.go).
func validateAIStepAccount(ctx *security.RequestContext, workflowAccountID string, task model.Task) error {
	target := literalStepAccount(workflowAccountID, task)
	if target == "" {
		return nil
	}
	sc := ctx.GetSecurityContext()
	// Super admins pass the access check for any account, so the tenant is checked too.
	tenantID, err := tenantForAccount(target)
	if err != nil || tenantID == "" || tenantID != sc.GetTenantId() || !sc.CanReadAccountData(target, workflowsModule) {
		return common.ErrorBadRequest(fmt.Sprintf("task '%s' targets account '%s', which is not accessible", task.ID, target))
	}
	return nil
}

// integrationLookupAccount is the account a step's integration is validated
// in, and whether it is known while saving. llm.mcp_call resolves its
// integration in the step's own account at run time, so saving checks the same
// one — and cannot check at all when that account is a template, since the
// integration may exist only in the account it will resolve to. Every other
// task keeps the workflow's account.
func integrationLookupAccount(workflowAccountID string, task model.Task) (string, bool) {
	if task.Type == mcpCallTaskType {
		if account, _ := task.Params["account_id"].(string); isTemplated(account) {
			return "", false
		}
		if target := literalStepAccount(workflowAccountID, task); target != "" {
			return target, true
		}
	}
	return workflowAccountID, true
}
