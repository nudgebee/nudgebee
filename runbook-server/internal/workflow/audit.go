package workflow

import (
	"time"

	"github.com/google/uuid"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/audit"
	"nudgebee/runbook/services/security"
)

// emitWorkflowAudit fires a workflow-automation audit row asynchronously.
// Workflow operations must never block on the audit POST, and an audit
// delivery failure must never fail the action — failures are logged at warn.
func emitWorkflowAudit(
	ctx *security.RequestContext,
	accountID string,
	eventType audit.EventType,
	action audit.EventAction,
	status audit.EventStatus,
	target string,
	prevState any,
	state any,
	attrs map[string]any,
) {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return
	}
	a := audit.Audit{
		Id:             uuid.New().String(),
		UserId:         ctx.GetSecurityContext().GetUserId(),
		TenantId:       ctx.GetSecurityContext().GetTenantId(),
		AccountId:      accountID,
		EventTime:      time.Now().UTC(),
		EventCategory:  audit.EventCategoryAutomation,
		EventType:      eventType,
		EventPrevState: prevState,
		EventState:     state,
		EventActor:     audit.EventActorAutorunbookService,
		EventTarget:    target,
		EventAction:    action,
		EventStatus:    status,
		EventAttr:      attrs,
	}
	go func() {
		if err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{a}}); err != nil {
			ctx.GetLogger().Warn("workflow audit emit failed", "event_type", eventType, "target", target, "error", err)
		}
	}()
}

// workflowAuditSnapshot returns the compact field set we record in
// event_state / event_prev_state. Full definitions are too large and
// noisy for the audit log; the snapshot is enough for the UI to render a
// human-readable summary and for retention.
func workflowAuditSnapshot(wf *model.Workflow) map[string]any {
	if wf == nil {
		return nil
	}
	snap := map[string]any{
		"id":     wf.ID,
		"name":   wf.Name,
		"status": string(wf.Status),
	}
	triggers := make([]string, 0, len(wf.Definition.Triggers))
	for _, t := range wf.Definition.Triggers {
		triggers = append(triggers, string(t.Type))
	}
	snap["triggers"] = triggers
	return snap
}
