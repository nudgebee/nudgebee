package optimizer

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"
	"nudgebee/runbook/services/audit"
	"nudgebee/runbook/services/notification"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
)

// A run of an automation that requires approval does not wait for the answer.
// It parks its planned tasks as Pending_Approval, files one approval and ends.
// Approving starts a separate, short run for exactly those tasks. Nothing stays
// open in Temporal, so the 1h run timeout and the Scheduled stale bound hold.

// ApprovalDecision is what a reviewer answers.
type ApprovalDecision string

const (
	ApprovalDecisionApprove ApprovalDecision = "APPROVED"
	ApprovalDecisionReject  ApprovalDecision = "REJECTED"
)

// Reasons written on tasks a parked run did not apply.
const (
	reasonApprovalRejected  = "Rejected by reviewer"
	reasonApprovalExpired   = "Approval expired before anyone decided"
	reasonApprovalCancelled = "Automation changed or was disabled before the approval was decided"
	reasonRecNotApplicable  = "Recommendation is no longer open"
	reasonRecChanged        = "Recommendation changed since the plan was reviewed"
	reasonRecResolving      = "Recommendation already has a pull request or ticket in progress"
)

// approvalNow is the clock used for approval windows; tests replace it.
var approvalNow = func() time.Time { return time.Now().UTC() }

// parkForApproval saves a gated run's plan and files its approval. It reports
// false when the run planned nothing to apply, in which case the caller carries
// on as an ordinary run.
func (s *optimizerService) parkForApproval(ctx context.Context, ao *model.AutoOptimize, tasks []model.AutoOptimizeTask, recContent map[uuid.UUID]map[string]any) (bool, error) {
	var parked []uuid.UUID
	for i := range tasks {
		t := &tasks[i]
		if t.Status != string(model.AutopilotTaskStatusScheduled) {
			continue
		}
		t.Status = string(model.AutopilotTaskStatusPendingApproval)
		if t.RecommendationID != nil {
			t.Attributes.PlannedRecommendationHash = model.PlannedValuesHash(ao.Category, recContent[*t.RecommendationID])
		}
		parked = append(parked, t.ID)
	}
	if len(parked) == 0 {
		return false, nil
	}

	now := approvalNow()
	approval := model.AutoOptimizeApproval{
		ID:          uuid.New(),
		AutoPilotID: ao.ID,
		TenantID:    ao.TenantID,
		AccountID:   ao.AccountID,
		Status:      model.AutoOptimizeApprovalStatusPending,
		CreatedAt:   now,
		Attributes: model.AutoOptimizeApprovalAttributes{
			TaskIDs:         parked,
			ExpiresAt:       now.Add(model.AutoOptimizeApprovalWindow),
			PlanFingerprint: ao.PlanFingerprint(),
		},
	}
	if err := s.dao.ParkAutoOptimizeRun(ctx, tasks, approval); err != nil {
		return false, err
	}

	slog.Info("Run parked for approval", "auto_optimize_id", ao.ID, "approval_id", approval.ID, "tasks", len(parked))
	s.notifyApprovalRequested(ao, approval, tasks)
	return true, nil
}

// settleApprovalsBeforeRun expires the automation's approvals whose window has
// closed, and reports whether one is still open. While one is open the run is
// skipped: approving it is how those changes go out.
func (s *optimizerService) settleApprovalsBeforeRun(ctx context.Context, ao *model.AutoOptimize) (bool, error) {
	expired, err := s.dao.ExpireAutoOptimizeApprovals(ctx, ao.ID, approvalNow())
	if err != nil {
		return false, err
	}
	// No notice: expiry is only noticed here, which on a slow schedule can be
	// days after the deadline the request already named. The Approvals tab shows
	// the run as expired on time.
	for _, a := range expired {
		s.releaseParkedTasks(ctx, a, reasonApprovalExpired)
	}

	pending, err := s.dao.GetPendingAutoOptimizeApprovals(ctx, ao.ID)
	if err != nil {
		return false, err
	}
	return len(pending) > 0, nil
}

// cancelPendingApprovals cancels the automation's open approvals after a change
// that makes their plans obsolete. Best-effort: approving re-checks the
// automation, so a missed cancel still cannot apply a stale plan.
func (s *optimizerService) cancelPendingApprovals(ctx context.Context, ao *model.AutoOptimize) {
	cancelled, err := s.dao.CancelPendingAutoOptimizeApprovals(ctx, ao.ID)
	if err != nil {
		slog.Error("Failed to cancel pending approvals", "auto_optimize_id", ao.ID, "error", err)
		return
	}
	for _, a := range cancelled {
		s.releaseParkedTasks(ctx, a, reasonApprovalCancelled)
		s.notifyApprovalOutcome(ao, a, "approval cancelled", "The automation was changed or disabled, so its planned changes were not applied.")
	}
}

// releaseParkedTasks skips the tasks an undecided or rejected approval held.
func (s *optimizerService) releaseParkedTasks(ctx context.Context, a model.AutoOptimizeApproval, reason string) {
	if _, err := s.dao.MoveAutoOptimizeTasks(ctx, a.Attributes.TaskIDs, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped, &reason); err != nil {
		slog.Error("Failed to skip parked tasks", "approval_id", a.ID, "error", err)
	}
}

// DecideApproval records a reviewer's answer to a parked run. Approving applies
// only what was reviewed: tasks whose recommendation closed or changed since
// planning are skipped, and the rest run in a new optimizer run.
func (s *optimizerService) DecideApproval(ctx context.Context, sc *security.RequestContext, accountID, approvalID uuid.UUID, decision ApprovalDecision, comments string) (string, error) {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return "", common.ErrorBadRequest("invalid tenant id")
	}
	reviewerID, err := uuid.Parse(sc.GetSecurityContext().GetUserId())
	if err != nil {
		return "", common.ErrorUnauthenticated("a signed-in user is required to decide an approval")
	}
	if decision != ApprovalDecisionApprove && decision != ApprovalDecisionReject {
		return "", common.ErrorBadRequest("status must be APPROVED or REJECTED")
	}

	approval, err := s.dao.GetAutoOptimizeApproval(ctx, approvalID, tenantID, accountID)
	if err != nil {
		return "", err
	}
	if approval.Status != model.AutoOptimizeApprovalStatusPending {
		return "", errAlreadyDecided(approval.Status)
	}

	ao, err := s.dao.GetAutoOptimize(ctx, approval.AutoPilotID)
	if err != nil {
		return "", err
	}
	if ao.TenantID != tenantID || ao.AccountID != accountID {
		return "", common.ErrorNotFound("approval not found")
	}

	now := approvalNow()
	if !approval.Attributes.ExpiresAt.After(now) {
		if _, err := s.settleApprovalsBeforeRun(ctx, ao); err != nil {
			slog.Error("Failed to expire approval", "approval_id", approval.ID, "error", err)
		}
		return "", errAlreadyDecided(model.AutoOptimizeApprovalStatusExpired)
	}

	var commentPtr *string
	if c := strings.TrimSpace(comments); c != "" {
		commentPtr = &c
	}

	if decision == ApprovalDecisionReject {
		ok, err := s.dao.DecideAutoOptimizeApproval(ctx, approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusRejected, &reviewerID, commentPtr, nil)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errAlreadyDecided("")
		}
		s.releaseParkedTasks(ctx, *approval, reasonApprovalRejected)
		detail := "The planned changes were rejected and not applied."
		if commentPtr != nil {
			detail += "\nComment: " + firstLine(*commentPtr, 280)
		}
		s.notifyApprovalOutcome(ao, *approval, "changes rejected", detail)
		emitApprovalAudit(sc, ao, approval.ID, decision, len(approval.Attributes.TaskIDs), 0)
		return fmt.Sprintf("Rejected. %d planned change(s) will not be applied.", len(approval.Attributes.TaskIDs)), nil
	}

	if !ao.RequiresApproval() || ao.PlanFingerprint() != approval.Attributes.PlanFingerprint {
		s.cancelPendingApprovals(ctx, ao)
		return "", common.Error{Code: 409, Message: "The automation changed since this plan was made, so it was cancelled. Its next run plans afresh."}
	}

	ok, err := s.dao.DecideAutoOptimizeApproval(ctx, approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, &reviewerID, commentPtr, &now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errAlreadyDecided("")
	}

	// An edit that saved between the check above and this decision cancels only
	// PENDING approvals, so it would miss this one. Look again now that it is
	// decided.
	current, err := s.dao.GetAutoOptimize(ctx, ao.ID)
	if err != nil {
		s.revertApproval(ctx, approval.ID, nil)
		return "", err
	}
	if !current.RequiresApproval() || current.PlanFingerprint() != approval.Attributes.PlanFingerprint {
		if _, err := s.dao.DecideAutoOptimizeApproval(ctx, approval.ID, model.AutoOptimizeApprovalStatusApproved, model.AutoOptimizeApprovalStatusCancelled, &reviewerID, commentPtr, nil); err != nil {
			slog.Error("Failed to cancel approval after a concurrent edit", "approval_id", approval.ID, "error", err)
		}
		s.releaseParkedTasks(ctx, *approval, reasonApprovalCancelled)
		return "", common.Error{Code: 409, Message: "The automation changed since this plan was made, so it was cancelled. Its next run plans afresh."}
	}

	apply, skipped, err := s.splitApprovedTasks(ctx, ao.Category, approval.Attributes.TaskIDs)
	if err != nil {
		s.revertApproval(ctx, approval.ID, nil)
		return "", err
	}
	for reason, ids := range skipped {
		r := reason
		if _, err := s.dao.MoveAutoOptimizeTasks(ctx, ids, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped, &r); err != nil {
			slog.Error("Failed to skip inapplicable approved tasks", "approval_id", approval.ID, "error", err)
		}
	}
	skippedCount := 0
	for _, ids := range skipped {
		skippedCount += len(ids)
	}

	// Run exactly the tasks this move took out of Pending_Approval: a task that
	// left that status meanwhile (skipped by a cancel, say) must not be counted
	// as applied.
	moved, err := s.dao.MoveAutoOptimizeTasks(ctx, apply, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusScheduled, nil)
	if err != nil {
		s.revertApproval(ctx, approval.ID, nil)
		return "", err
	}
	if len(moved) == 0 {
		emitApprovalAudit(sc, ao, approval.ID, decision, 0, skippedCount)
		return "Approved, but none of the planned changes still apply, so nothing was changed.", nil
	}
	apply = moved

	if err := s.dao.SetAutoOptimizeExecutionStatus(ctx, ao.ID, model.AutopilotExecutionStatusInProgress); err != nil {
		slog.Error("Failed to mark auto optimize in progress", "auto_optimize_id", ao.ID, "error", err)
	}

	if err := s.startApprovedRun(ctx, ao.ID, approval.ID, apply); err != nil {
		s.revertApproval(ctx, approval.ID, apply)
		if setErr := s.dao.SetAutoOptimizeExecutionStatus(ctx, ao.ID, model.AutopilotExecutionStatusIdle); setErr != nil {
			slog.Error("Failed to reset execution status after start failure", "auto_optimize_id", ao.ID, "error", setErr)
		}
		return "", fmt.Errorf("failed to start the approved run: %w", err)
	}

	emitApprovalAudit(sc, ao, approval.ID, decision, len(apply), skippedCount)
	msg := fmt.Sprintf("Approved. Applying %d change(s).", len(apply))
	if skippedCount > 0 {
		msg += fmt.Sprintf(" %d skipped because their recommendation closed or changed since the plan was made.", skippedCount)
	}
	return msg, nil
}

// splitApprovedTasks returns the parked tasks that still apply, and the rest
// grouped by the reason they don't. It repeats the checks planning made, since
// the recommendation may have moved on while the plan waited.
func (s *optimizerService) splitApprovedTasks(ctx context.Context, category string, taskIDs []uuid.UUID) ([]uuid.UUID, map[string][]uuid.UUID, error) {
	tasks, err := s.dao.GetAutoOptimizeTasksByIDs(ctx, taskIDs)
	if err != nil {
		return nil, nil, err
	}
	var recIDs []uuid.UUID
	for _, t := range tasks {
		if t.RecommendationID != nil {
			recIDs = append(recIDs, *t.RecommendationID)
		}
	}
	snaps, err := s.dao.GetRecommendationSnapshots(ctx, recIDs)
	if err != nil {
		return nil, nil, err
	}
	resolving, err := s.dao.GetActiveResolutionsForRecommendations(ctx, recIDs)
	if err != nil {
		return nil, nil, err
	}

	var apply []uuid.UUID
	skipped := map[string][]uuid.UUID{}
	for _, t := range tasks {
		if t.Status != string(model.AutopilotTaskStatusPendingApproval) {
			continue
		}
		if t.RecommendationID == nil {
			apply = append(apply, t.ID)
			continue
		}
		snap, ok := snaps[*t.RecommendationID]
		switch {
		case !ok || !snap.StillApplicable():
			skipped[reasonRecNotApplicable] = append(skipped[reasonRecNotApplicable], t.ID)
		case len(resolving[*t.RecommendationID]) > 0:
			skipped[reasonRecResolving] = append(skipped[reasonRecResolving], t.ID)
		case t.Attributes.PlannedRecommendationHash != "" &&
			model.PlannedValuesHash(category, snap.Content) != t.Attributes.PlannedRecommendationHash:
			skipped[reasonRecChanged] = append(skipped[reasonRecChanged], t.ID)
		default:
			apply = append(apply, t.ID)
		}
	}
	return apply, skipped, nil
}

// revertApproval puts an approval back to PENDING after the approved run could
// not be started, returning its tasks to Pending_Approval, so the reviewer can
// try again.
func (s *optimizerService) revertApproval(ctx context.Context, approvalID uuid.UUID, scheduled []uuid.UUID) {
	if _, err := s.dao.MoveAutoOptimizeTasks(ctx, scheduled, model.AutopilotTaskStatusScheduled, model.AutopilotTaskStatusPendingApproval, nil); err != nil {
		slog.Error("Failed to return tasks to pending approval", "approval_id", approvalID, "error", err)
	}
	if _, err := s.dao.DecideAutoOptimizeApproval(ctx, approvalID, model.AutoOptimizeApprovalStatusApproved, model.AutoOptimizeApprovalStatusPending, nil, nil, nil); err != nil {
		slog.Error("Failed to return approval to pending", "approval_id", approvalID, "error", err)
	}
}

func (s *optimizerService) startApprovedRun(ctx context.Context, aoID, approvalID uuid.UUID, taskIDs []uuid.UUID) error {
	ids := make([]string, len(taskIDs))
	for i, id := range taskIDs {
		ids[i] = id.String()
	}
	_, err := s.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "workflow_" + s.scheduleID(aoID) + "_approval_" + approvalID.String(),
		TaskQueue:                workflow.OptimizerTaskQueue,
		WorkflowExecutionTimeout: workflow.OptimizerWorkflowExecutionTimeout,
	}, workflow.OptimizerWorkflow, workflow.OptimizerWorkflowInput{AutoOptimizeID: aoID.String(), TaskIDs: ids})
	return err
}

func errAlreadyDecided(status model.AutoOptimizeApprovalStatus) error {
	switch status {
	case model.AutoOptimizeApprovalStatusExpired:
		return common.Error{Code: 409, Message: "This approval expired, so its changes were not applied. The next run plans afresh."}
	case model.AutoOptimizeApprovalStatusCancelled:
		return common.Error{Code: 409, Message: "This approval was cancelled because the automation changed."}
	case "":
		return common.Error{Code: 409, Message: "This approval was already decided or has expired."}
	}
	return common.Error{Code: 409, Message: fmt.Sprintf("This approval was already %s.", strings.ToLower(string(status)))}
}

// emitApprovalAudit records a reviewer's decision. The approved changes still
// run as the optimizer's own principal; the audit names the reviewer as the
// one who decided, not the one who applied.
var emitApprovalAudit = func(sc *security.RequestContext, ao *model.AutoOptimize, approvalID uuid.UUID, decision ApprovalDecision, applied, skipped int) {
	a := audit.Audit{
		Id:            uuid.New().String(),
		UserId:        sc.GetSecurityContext().GetUserId(),
		TenantId:      ao.TenantID.String(),
		AccountId:     ao.AccountID.String(),
		EventTime:     time.Now().UTC(),
		EventCategory: audit.EventCategoryAutopilot,
		EventType:     audit.EventTypeAutopilotUpdate,
		EventState:    map[string]any{"approval_id": approvalID.String(), "decision": string(decision), "applied": applied, "skipped": skipped},
		EventActor:    audit.EventActorAutopilotService,
		EventTarget:   ao.ID.String(),
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"source": "auto_optimize_approval"},
	}
	// CreateAudit detaches from the request's cancellation and bounds its own
	// insert, so passing the request context here is safe.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("auto optimize approval audit panicked", "approval_id", approvalID, "recover", r, "stack", string(debug.Stack()))
			}
		}()
		if err := audit.CreateAudit(sc, &audit.AuditRequest{Audits: []audit.Audit{a}}); err != nil {
			slog.Warn("auto optimize approval audit failed", "approval_id", approvalID, "error", err)
		}
	}()
}

func approvalsDeepLink(ao *model.AutoOptimize) string {
	return fmt.Sprintf("%s/optimise?accountId=%s#auto-optimize/approvals",
		strings.TrimRight(config.Config.BaseUrl, "/"), ao.AccountID)
}

// approvalPreviewLimit caps how many planned changes the request lists.
const approvalPreviewLimit = 10

func (s *optimizerService) notifyApprovalRequested(ao *model.AutoOptimize, a model.AutoOptimizeApproval, tasks []model.AutoOptimizeTask) {
	var parked []model.AutoOptimizeTask
	for _, t := range tasks {
		if t.Status == string(model.AutopilotTaskStatusPendingApproval) {
			parked = append(parked, t)
		}
	}
	s.sendToConfiguredChannelsWithParams(ao, notification.EventApprovalRequested, func(platform string) string {
		return buildApprovalRequest(ao, a, parked, platform)
	}, func(platform string) map[string]any {
		return approvalButtonParams(ao, a, platform)
	})
}

// chatApprovalTokenPrefix marks an approval token as an Auto Optimize approval,
// so notifications-server decides it through POST /autopilot/approval as the
// clicking user instead of resolving a workflow approval task.
const chatApprovalTokenPrefix = "auto_optimize:"

// approvalButtonParams adds Approve / Reject to the request on the platforms
// whose buttons notifications-server can answer (Slack and MS Teams). The
// token only names the approval; the clicker's own access decides it.
func approvalButtonParams(ao *model.AutoOptimize, a model.AutoOptimizeApproval, platform string) map[string]any {
	if platform != "slack" && platform != "ms_teams" {
		return nil
	}
	return map[string]any{
		"approval_token":   fmt.Sprintf("%s%s:%s:%s", chatApprovalTokenPrefix, a.ID, ao.AccountID, ao.TenantID),
		"approval_options": []string{"approve", "reject"},
	}
}

func buildApprovalRequest(ao *model.AutoOptimize, a model.AutoOptimizeApproval, parked []model.AutoOptimizeTask, platform string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — approval needed\n", optimizeDisplayName(ao))
	fmt.Fprintf(&b, "\n%d change(s) planned, nothing applied yet:\n", len(parked))
	shown := parked
	if len(shown) > approvalPreviewLimit {
		shown = shown[:approvalPreviewLimit]
	}
	writeResourceLines(&b, shown, false)
	if more := len(parked) - len(shown); more > 0 {
		fmt.Fprintf(&b, "  …and %d more\n", more)
	}
	fmt.Fprintf(&b, "\nApprove or reject by %s UTC.\n", a.Attributes.ExpiresAt.UTC().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "\n%s", formatLink(platform, approvalsDeepLink(ao), "Review in Nudgebee"))
	return b.String()
}

func (s *optimizerService) notifyApprovalOutcome(ao *model.AutoOptimize, a model.AutoOptimizeApproval, title, detail string) {
	s.sendToConfiguredChannels(ao, notification.EventTaskCompleted, func(platform string) string {
		return fmt.Sprintf("%s — %s\n\n%s\n\n%s", optimizeDisplayName(ao), title, detail,
			formatLink(platform, approvalsDeepLink(ao), "View in Nudgebee"))
	})
}
