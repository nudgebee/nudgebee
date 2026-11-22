package agents

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
)

// integrationGaps checks a workflow definition (JSON) against the task registry for integration
// steps that point at nothing (tools.FindIntegrationGaps). An unreadable definition or registry
// finds nothing, which leaves the save path as it was.
func (a *WorkflowBuilderAgent) integrationGaps(ctx *security.RequestContext, workflowJSON string, configExists func(key string) bool) []tools.IntegrationGap {
	var workflow map[string]interface{}
	if err := json.Unmarshal([]byte(workflowJSON), &workflow); err != nil {
		return nil
	}
	registry := a.fetchTaskRegistry(ctx)
	if registry == "" {
		ctx.GetLogger().Warn("workflow_builder: task registry unavailable, skipping the integration check")
		return nil
	}
	return tools.FindIntegrationGaps(workflow, tools.IntegrationParamsByTaskType([]byte(registry)), configExists)
}

// integrationGapMessage explains to the person why the automation was not saved.
func (a *WorkflowBuilderAgent) integrationGapMessage(ctx *security.RequestContext, gaps []tools.IntegrationGap) string {
	return "I did not save this automation, because a step needs an integration it cannot use:\n" +
		strings.Join(tools.IntegrationGapLines(ctx, a.accountId, gaps), "\n")
}

// readOnlyCheckResults runs the save-time checks on a saved automation so an answer to "why
// doesn't it work" starts from what is actually wrong with it, not from guesses about its status
// or trigger (#42204): integration steps that point at nothing, and a definition the runbook
// server rejects. A check that cannot run adds nothing — an outage is not a defect of the
// automation. Returns "" when there is nothing to report.
func (a *WorkflowBuilderAgent) readOnlyCheckResults(ctx *security.RequestContext, workflow map[string]interface{}) string {
	var findings []string
	if gaps := tools.WorkflowIntegrationGaps(ctx, a.accountId, workflow); len(gaps) > 0 {
		findings = append(findings, tools.IntegrationGapLines(ctx, a.accountId, gaps)...)
	}

	reqCtx, cancel := context.WithTimeout(ctx.GetContext(), 15*time.Second)
	defer cancel()
	if _, err := tools.DoRunbookRequestWithContext(reqCtx, "POST", "workflows/validate", workflow, a.accountId,
		ctx.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetUserId()); err != nil {
		if tools.IsValidationRejection(err) {
			findings = append(findings, "- The server rejects this definition as saved: "+common.SanitizeErrorMessage(err.Error()))
		} else {
			ctx.GetLogger().Warn("workflow_builder: could not validate the saved automation for a read-only answer", "error", err)
		}
	}

	if len(findings) == 0 {
		return ""
	}
	return "\nCHECK RESULTS (automatic checks run on this automation just now; each problem below stops it from working even when it is active):\n" + strings.Join(findings, "\n") + "\n"
}
