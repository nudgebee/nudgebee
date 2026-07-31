package workflow

import (
	"fmt"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// assertAIInvocationAllowed is the server-side gate on AI-originated workflow
// runs. It is what makes `workflows.ai_invocable` a real boundary rather than a
// hint the caller may ignore.
//
// This lives at the service layer, not in the HTTP handlers, because both the
// REST trigger endpoint and the RPC action funnel through ExecuteWorkflow. A
// handler-level check would have to be duplicated and would be silently missed
// by any future caller; here it cannot be bypassed.
//
// Why it exists at all: llm-server already has a generic workflow_trigger tool
// that can name any workflow id. Without this gate the per-workflow opt-in would
// be decoration — the AI could simply trigger a workflow that was never opted
// in. Enforcing server-side also means revocation is immediate, rather than
// waiting out llm-server's tool cache.
//
// Every failure is deliberately reported as the same "not available to the AI
// assistant" message: an AI-originated caller has no business learning why a
// particular workflow is off limits, and the distinctions are visible in logs
// and to the human in the UI.
//
// The caller must pass a workflow whose Definition is the LIVE version snapshot
// (as ExecuteWorkflow does), because the AI is only ever allowed to run what is
// published — never an in-progress draft.
// featureEnabledForAccount is indirected through a variable so the gate's
// decision table can be unit-tested without a metastore. Production always uses
// the real lookup; only tests replace it.
var featureEnabledForAccount = common.IsFeatureEnabledForAccount

func (s *Service) assertAIInvocationAllowed(ctx *security.RequestContext, accountID string, wf *model.Workflow) error {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	deny := func(reason string) error {
		ctx.GetLogger().Warn("refused AI-originated workflow run",
			"workflow_id", wf.ID, "account_id", accountID, "tenant_id", tenantID, "reason", reason)
		return common.ErrorUnauthorized("workflow is not available to the AI assistant")
	}

	// The feature flag is checked first so that a tenant which has not enrolled
	// behaves exactly as it did before this feature existed, whatever is set on
	// individual workflows.
	enabled, err := featureEnabledForAccount(common.FeatureAIWorkflowTools, tenantID, accountID)
	if err != nil {
		// Fail closed. The lookup already logs; deny() records the decision.
		return deny(fmt.Sprintf("feature flag lookup failed: %v", err))
	}
	if !enabled {
		return deny("feature not enabled for this tenant/account")
	}

	if !wf.AIInvocable {
		return deny("workflow is not opted in to AI invocation")
	}

	// ACTIVE only. ExecuteWorkflow itself lets a human run a PAUSED workflow
	// manually, but PAUSED means "deliberately switched off", and the AI reaching
	// past that is not something an operator would expect.
	if wf.Status != model.WorkflowStatusActive {
		return deny(fmt.Sprintf("workflow status is %s (must be ACTIVE)", wf.Status))
	}

	// Re-checked against the live definition, not the draft that model validation
	// saw: a workflow could have been opted in while its draft had a manual
	// trigger, then published without one.
	if !wf.Definition.HasManualTrigger() {
		return deny("live version has no manual trigger")
	}

	return nil
}
