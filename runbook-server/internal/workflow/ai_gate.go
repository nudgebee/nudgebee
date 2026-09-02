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
// by any future caller; here it covers every workflow-execution path at once.
//
// SCOPE — this gates workflow EXECUTION (ExecuteWorkflow, plus the outright
// refusal in TriggerWorkflowFromDraft). The automation agent holds two other
// tools that run things and are NOT gated here: `tasks/:type/execute`, which
// runs a single task standalone, and `workflows/dry-run`, which executes an
// AI-composed definition on Temporal (most tasks do not honour _dry_run, so
// HTTP, script, cloud and K8s tasks still reach external systems). Both predate
// this gate. Do not read "ai_invocable is enforced server-side" as covering
// them — if you are reasoning about what an AI caller can execute, those two
// routes are separate and still open. Tracked in #35398.
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

	// A coarse, safe-to-show code accompanies each denial. The full reason still
	// only goes to the logs, but a single opaque message turned out to cost a
	// human: told merely "not available", the assistant confidently advised
	// enabling a toggle that was already on, for an automation whose real
	// problem was that it was PAUSED. These codes distinguish nothing an AI
	// caller could not learn from workflow_list, and save the user a dead end.
	deny := func(code, reason string) error {
		ctx.GetLogger().Warn("refused AI-originated workflow run",
			"workflow_id", wf.ID, "account_id", accountID, "tenant_id", tenantID,
			"reason", reason, "code", code)
		return common.ErrorUnauthorized(fmt.Sprintf("workflow is not available to the AI assistant (%s)", code))
	}

	// Require a real user. runbook-server treats a missing user id as a system
	// call and grants tenant-account-admin, so an unidentified AI run would
	// execute with more authority than whoever asked for it, skipping their own
	// canRunWorkflows check. llm-server refuses these client-side too, but the
	// whole reason this gate lives at the service layer is that a client-side
	// check is one a future caller can silently miss.
	if userID := ctx.GetSecurityContext().GetUserId(); userID == "" || userID == security.GetSystemUserId() {
		return deny("no_user", "AI-originated run has no identified user")
	}

	// The feature flag is checked first so that a tenant which has not enrolled
	// behaves exactly as it did before this feature existed, whatever is set on
	// individual workflows.
	enabled, err := featureEnabledForAccount(common.FeatureAIWorkflowTools, tenantID, accountID)
	if err != nil {
		// Fail closed. The lookup already logs; deny() records the decision.
		return deny("not_enabled", fmt.Sprintf("feature flag lookup failed: %v", err))
	}
	if !enabled {
		return deny("not_enabled", "feature not enabled for this tenant/account")
	}

	if !wf.AIInvocable {
		return deny("not_opted_in", "workflow is not opted in to AI invocation")
	}

	// ACTIVE only. ExecuteWorkflow itself lets a human run a PAUSED workflow
	// manually, but PAUSED means "deliberately switched off", and the AI reaching
	// past that is not something an operator would expect.
	if wf.Status != model.WorkflowStatusActive {
		return deny("not_active", fmt.Sprintf("workflow status is %s (must be ACTIVE)", wf.Status))
	}

	// Re-checked against the live definition, not the draft that model validation
	// saw: a workflow could have been opted in while its draft had a manual
	// trigger, then published without one.
	if !wf.Definition.HasManualTrigger() {
		return deny("no_manual_trigger", "live version has no manual trigger")
	}

	return nil
}
