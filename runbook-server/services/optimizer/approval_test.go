package optimizer

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

var approvalTestNow = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

func withApprovalClock(t *testing.T) {
	t.Helper()
	prevNow, prevAudit := approvalNow, emitApprovalAudit
	approvalNow = func() time.Time { return approvalTestNow }
	emitApprovalAudit = func(*security.RequestContext, *model.AutoOptimize, uuid.UUID, ApprovalDecision, int, int) {}
	t.Cleanup(func() { approvalNow, emitApprovalAudit = prevNow, prevAudit })
}

func gatedAutoOptimize() *model.AutoOptimize {
	ns, kind := "shop", "Deployment"
	return &model.AutoOptimize{
		ID:           uuid.New(),
		AccountID:    uuid.New(),
		TenantID:     uuid.New(),
		Category:     "vertical_rightsize",
		Status:       model.AutoOptimizeStatusActive,
		ScheduleTime: "0 * * * *",
		Rule:         map[string]any{"cpu": true, "memory": true},
		Attributes:   model.AutoOptimizeAttributes{RequireApproval: true},
		ResourceFilters: []model.AutoOptimizeResourceFilter{
			{Namespace: &ns, Type: &kind},
		},
	}
}

func approvalErrCode(t *testing.T, err error) int {
	t.Helper()
	var ce common.Error
	require.True(t, errors.As(err, &ce), "want a common.Error, got %v", err)
	return ce.Code
}

// A gated run saves its plan as Pending_Approval, files one approval holding
// exactly those tasks, applies nothing, and does not count as an execution.
func TestGenerateTasks_GatedRunParksForApproval(t *testing.T) {
	withApprovalClock(t)
	mockRepo := new(MockOptimizerRepository)
	svc := &optimizerService{dao: mockRepo, factory: NewExecutorFactory()}
	svc.factory.Register("vertical_rightsize", &VerticalRightsizeGenerator{})

	ctx := context.Background()
	ao := gatedAutoOptimize()
	recID := uuid.New()
	content := map[string]any{"cart": []any{map[string]any{
		"resource": "cpu", "allocated": map[string]any{"request": 0.5}, "recommended": map[string]any{"request": 0.25},
		"metric": map[string]any{"p99": 0.21},
	}}}

	mockRepo.On("GetAutoOptimize", ctx, ao.ID).Return(ao, nil)
	mockRepo.On("GetAgent", ctx, ao.AccountID).Return(&model.Agent{Status: "Connected"}, nil)
	mockRepo.expectNoApprovals(ao.ID)
	mockRepo.On("GetFullRecommendationsForOptimizerCategory", ctx, ao.AccountID, "vertical_rightsize").
		Return([]model.RecommendationWithResource{{
			Recommendation:     model.Recommendation{ID: recID, Recommendation: content},
			ResourceIdentifier: "shop/Deployment/cart",
		}}, nil)
	mockRepo.On("GetActiveTasksForRecommendations", ctx, mock.Anything).Return(map[uuid.UUID]model.AutoOptimizeTask{}, nil)
	mockRepo.On("GetActiveResolutionsForRecommendations", ctx, mock.Anything).Return(map[uuid.UUID][]model.RecommendationResolution{}, nil)
	mockRepo.On("GetWorkloadFiltersForNamespace", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]model.AutoOptimizeResourceFilter{}, nil).Maybe()

	var saved []model.AutoOptimizeTask
	var filed model.AutoOptimizeApproval
	mockRepo.On("ParkAutoOptimizeRun", ctx,
		mock.MatchedBy(func(ts []model.AutoOptimizeTask) bool { saved = ts; return true }),
		mock.MatchedBy(func(a model.AutoOptimizeApproval) bool { filed = a; return true })).Return(nil)
	var states []model.AutoOptimize
	mockRepo.On("SaveAutoOptimize", ctx, mock.MatchedBy(func(a model.AutoOptimize) bool { states = append(states, a); return true })).Return(nil)

	tasks, err := svc.GenerateTasks(ctx, ao.ID)
	require.NoError(t, err)
	assert.Empty(t, tasks, "a parked run hands nothing to the execute loop")

	require.Len(t, saved, 1)
	assert.Equal(t, string(model.AutopilotTaskStatusPendingApproval), saved[0].Status)
	assert.Equal(t, model.PlannedValuesHash("vertical_rightsize", content), saved[0].Attributes.PlannedRecommendationHash)
	assert.NotEmpty(t, saved[0].Attributes.PlannedRecommendationHash)

	assert.Equal(t, model.AutoOptimizeApprovalStatusPending, filed.Status)
	assert.Equal(t, []uuid.UUID{saved[0].ID}, filed.Attributes.TaskIDs)
	assert.Equal(t, approvalTestNow.Add(model.AutoOptimizeApprovalWindow), filed.Attributes.ExpiresAt)
	assert.Equal(t, ao.PlanFingerprint(), filed.Attributes.PlanFingerprint)
	assert.Equal(t, ao.TenantID, filed.TenantID)

	last := states[len(states)-1]
	assert.Equal(t, string(model.AutopilotExecutionStatusIdle), last.ExecutionStatus)
	assert.Nil(t, last.LastExecutedTime, "nothing was applied, so the run must not read as executed")
	require.NotNil(t, last.NextScheduleTime)
	assert.True(t, last.NextScheduleTime.After(time.Now().UTC()))
}

// While a run waits for approval, later runs are skipped without planning, but
// the next run time still moves forward.
func TestGenerateTasks_SkipsWhileAnApprovalIsOpen(t *testing.T) {
	withApprovalClock(t)
	mockRepo := new(MockOptimizerRepository)
	svc := &optimizerService{dao: mockRepo, factory: NewExecutorFactory()}

	ctx := context.Background()
	ao := gatedAutoOptimize()
	open := model.AutoOptimizeApproval{ID: uuid.New(), AutoPilotID: ao.ID, Status: model.AutoOptimizeApprovalStatusPending,
		Attributes: model.AutoOptimizeApprovalAttributes{ExpiresAt: approvalTestNow.Add(time.Hour)}}

	mockRepo.On("GetAutoOptimize", ctx, ao.ID).Return(ao, nil)
	mockRepo.On("ExpireAutoOptimizeApprovals", ctx, ao.ID, approvalTestNow).Return([]model.AutoOptimizeApproval{}, nil)
	mockRepo.On("GetPendingAutoOptimizeApprovals", ctx, ao.ID).Return([]model.AutoOptimizeApproval{open}, nil)
	var saved model.AutoOptimize
	mockRepo.On("SaveAutoOptimize", ctx, mock.MatchedBy(func(a model.AutoOptimize) bool { saved = a; return true })).Return(nil)

	tasks, err := svc.GenerateTasks(ctx, ao.ID)
	require.NoError(t, err)
	assert.Empty(t, tasks)
	mockRepo.AssertNotCalled(t, "GetFullRecommendationsForOptimizerCategory", mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, "", saved.ExecutionStatus, "never marked InProgress")
	assert.Nil(t, saved.LastExecutedTime)
	require.NotNil(t, saved.NextScheduleTime)
	assert.True(t, saved.NextScheduleTime.After(time.Now().UTC()))
}

// An approval whose window closed is expired by the next run, which skips its
// tasks and then plans afresh.
func TestGenerateTasks_ExpiresClosedApprovalsThenPlans(t *testing.T) {
	withApprovalClock(t)
	mockRepo := new(MockOptimizerRepository)
	svc := &optimizerService{dao: mockRepo, factory: NewExecutorFactory()}

	ctx := context.Background()
	ao := gatedAutoOptimize()
	parkedTask := uuid.New()
	expired := model.AutoOptimizeApproval{ID: uuid.New(), AutoPilotID: ao.ID, Status: model.AutoOptimizeApprovalStatusExpired,
		Attributes: model.AutoOptimizeApprovalAttributes{TaskIDs: []uuid.UUID{parkedTask}}}

	mockRepo.On("GetAutoOptimize", ctx, ao.ID).Return(ao, nil)
	mockRepo.On("ExpireAutoOptimizeApprovals", ctx, ao.ID, approvalTestNow).Return([]model.AutoOptimizeApproval{expired}, nil)
	mockRepo.On("MoveAutoOptimizeTasks", ctx, []uuid.UUID{parkedTask}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return r != nil && *r == reasonApprovalExpired })).Return(nil, nil)
	mockRepo.On("GetPendingAutoOptimizeApprovals", ctx, ao.ID).Return([]model.AutoOptimizeApproval{}, nil)
	mockRepo.On("GetAgent", ctx, ao.AccountID).Return(&model.Agent{Status: "Connected"}, nil)
	mockRepo.On("SaveAutoOptimize", ctx, mock.Anything).Return(nil)
	mockRepo.On("GetFullRecommendationsForOptimizerCategory", ctx, ao.AccountID, "vertical_rightsize").Return([]model.RecommendationWithResource{}, nil)
	svc.factory.Register("vertical_rightsize", &VerticalRightsizeGenerator{})
	mockRepo.On("GetWorkloadFiltersForNamespace", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]model.AutoOptimizeResourceFilter{}, nil).Maybe()

	_, err := svc.GenerateTasks(ctx, ao.ID)
	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func verticalContent(recommendedCPU float64, scanMetric float64) map[string]any {
	return map[string]any{"app": []any{map[string]any{
		"resource":    "cpu",
		"allocated":   map[string]any{"request": 0.5},
		"recommended": map[string]any{"request": recommendedCPU},
		"metric":      map[string]any{"p99": scanMetric},
	}}}
}

type decideFixture struct {
	repo     *MockOptimizerRepository
	temporal *mocks.Client
	svc      *optimizerService
	sc       *security.RequestContext
	ao       *model.AutoOptimize
	approval *model.AutoOptimizeApproval
	reviewer uuid.UUID
}

func newDecideFixture(t *testing.T) *decideFixture {
	withApprovalClock(t)
	f := &decideFixture{repo: new(MockOptimizerRepository), temporal: &mocks.Client{}, ao: gatedAutoOptimize(), reviewer: uuid.New()}
	f.svc = &optimizerService{dao: f.repo, factory: NewExecutorFactory(), temporalClient: f.temporal}
	f.sc = security.NewRequestContextForTenantAccountAdmin(f.ao.TenantID.String(), f.reviewer.String(), []string{f.ao.AccountID.String()})
	f.approval = &model.AutoOptimizeApproval{
		ID: uuid.New(), AutoPilotID: f.ao.ID, TenantID: f.ao.TenantID, AccountID: f.ao.AccountID,
		Status: model.AutoOptimizeApprovalStatusPending,
		Attributes: model.AutoOptimizeApprovalAttributes{
			ExpiresAt:       approvalTestNow.Add(time.Hour),
			PlanFingerprint: f.ao.PlanFingerprint(),
		},
	}
	f.repo.On("GetAutoOptimizeApproval", mock.Anything, f.approval.ID, f.ao.TenantID, f.ao.AccountID).Return(f.approval, nil)
	f.repo.On("GetAutoOptimize", mock.Anything, f.ao.ID).Return(f.ao, nil)
	return f
}

func (f *decideFixture) parkedTask(recID uuid.UUID, plannedContent map[string]any) model.AutoOptimizeTask {
	task := model.AutoOptimizeTask{ID: uuid.New(), RecommendationID: &recID, Status: string(model.AutopilotTaskStatusPendingApproval)}
	task.Attributes.PlannedRecommendationHash = model.PlannedValuesHash("vertical_rightsize", plannedContent)
	f.approval.Attributes.TaskIDs = append(f.approval.Attributes.TaskIDs, task.ID)
	return task
}

func (f *decideFixture) decide(decision ApprovalDecision, comment string) (string, error) {
	return f.svc.DecideApproval(context.Background(), f.sc, f.ao.AccountID, f.approval.ID, decision, comment)
}

// Approving applies only what was reviewed: a task whose recommendation changed
// or closed since planning is skipped, the rest go out in an approved run that
// carries exactly their ids.
func TestDecideApproval_ApproveAppliesOnlyWhatWasReviewed(t *testing.T) {
	f := newDecideFixture(t)
	same, changed, closed, resolving := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	planned := verticalContent(0.25, 0.2)
	keep := f.parkedTask(same, planned)
	stale := f.parkedTask(changed, planned)
	gone := f.parkedTask(closed, planned)
	busy := f.parkedTask(resolving, planned)

	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved,
		&f.reviewer, mock.Anything, mock.MatchedBy(func(at *time.Time) bool { return at != nil && at.Equal(approvalTestNow) })).Return(true, nil)
	f.repo.On("GetAutoOptimizeTasksByIDs", mock.Anything, f.approval.Attributes.TaskIDs).Return([]model.AutoOptimizeTask{keep, stale, gone, busy}, nil)
	f.repo.On("GetRecommendationSnapshots", mock.Anything, mock.Anything).Return(map[uuid.UUID]model.RecommendationSnapshot{
		same:      {Status: "Open", Content: verticalContent(0.25, 0.9)}, // rescanned: metrics moved, recommendation did not
		changed:   {Status: "Open", Content: verticalContent(0.4, 0.2)},
		closed:    {Status: "Closed", Content: planned},
		resolving: {Status: "InProgress", Content: planned},
	}, nil)
	f.repo.On("GetActiveResolutionsForRecommendations", mock.Anything, mock.Anything).Return(map[uuid.UUID][]model.RecommendationResolution{
		resolving: {{ID: uuid.New(), RecommendationID: resolving}},
	}, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{busy.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonRecResolving })).Return(nil, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{stale.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonRecChanged })).Return(nil, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{gone.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonRecNotApplicable })).Return(nil, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{keep.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusScheduled, (*string)(nil)).Return([]uuid.UUID{keep.ID}, nil)
	f.repo.On("SetAutoOptimizeExecutionStatus", mock.Anything, f.ao.ID, model.AutopilotExecutionStatusInProgress).Return(nil)
	f.temporal.On("ExecuteWorkflow", mock.Anything,
		mock.MatchedBy(func(o client.StartWorkflowOptions) bool {
			return o.ID == "workflow_auto_optimize_"+f.ao.ID.String()+"_approval_"+f.approval.ID.String() &&
				o.TaskQueue == workflow.OptimizerTaskQueue
		}), mock.Anything,
		workflow.OptimizerWorkflowInput{AutoOptimizeID: f.ao.ID.String(), TaskIDs: []string{keep.ID.String()}},
	).Return(&mocks.WorkflowRun{}, nil)

	msg, err := f.decide(ApprovalDecisionApprove, "")
	require.NoError(t, err)
	assert.Contains(t, msg, "Applying 1 change")
	assert.Contains(t, msg, "3 skipped")
	f.repo.AssertExpectations(t)
	f.temporal.AssertExpectations(t)
}

// If the approved run cannot be started, the approval and its tasks go back to
// waiting so the reviewer can try again.
func TestDecideApproval_StartFailureReturnsToPending(t *testing.T) {
	f := newDecideFixture(t)
	rec := uuid.New()
	planned := verticalContent(0.25, 0.2)
	task := f.parkedTask(rec, planned)

	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	f.repo.On("GetAutoOptimizeTasksByIDs", mock.Anything, mock.Anything).Return([]model.AutoOptimizeTask{task}, nil)
	f.repo.On("GetRecommendationSnapshots", mock.Anything, mock.Anything).Return(map[uuid.UUID]model.RecommendationSnapshot{rec: {Status: "Open", Content: planned}}, nil)
	f.repo.On("GetActiveResolutionsForRecommendations", mock.Anything, mock.Anything).Return(map[uuid.UUID][]model.RecommendationResolution{}, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusScheduled, (*string)(nil)).Return([]uuid.UUID{task.ID}, nil)
	f.repo.On("SetAutoOptimizeExecutionStatus", mock.Anything, f.ao.ID, model.AutopilotExecutionStatusInProgress).Return(nil)
	f.repo.On("SetAutoOptimizeExecutionStatus", mock.Anything, f.ao.ID, model.AutopilotExecutionStatusIdle).Return(nil)
	f.temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusScheduled, model.AutopilotTaskStatusPendingApproval, (*string)(nil)).Return(nil, nil)
	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusApproved, model.AutoOptimizeApprovalStatusPending, (*uuid.UUID)(nil), (*string)(nil), (*time.Time)(nil)).Return(true, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	require.Error(t, err)
	f.repo.AssertExpectations(t)
}

// Rejecting records the reviewer and comment and skips every parked task.
func TestDecideApproval_RejectSkipsParkedTasks(t *testing.T) {
	f := newDecideFixture(t)
	task := f.parkedTask(uuid.New(), map[string]any{})

	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusRejected,
		&f.reviewer, mock.MatchedBy(func(c *string) bool { return c != nil && *c == "too aggressive" }), (*time.Time)(nil)).Return(true, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonApprovalRejected })).Return(nil, nil)

	_, err := f.decide(ApprovalDecisionReject, "  too aggressive ")
	require.NoError(t, err)
	f.repo.AssertExpectations(t)
	f.temporal.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A plan made before the automation's plan-affecting settings changed is
// cancelled instead of applied.
func TestDecideApproval_ChangedAutomationCancelsPlan(t *testing.T) {
	f := newDecideFixture(t)
	task := f.parkedTask(uuid.New(), map[string]any{})
	f.ao.Rule = map[string]any{"cpu": true, "memory": false}

	f.repo.On("CancelPendingAutoOptimizeApprovals", mock.Anything, f.ao.ID).Return([]model.AutoOptimizeApproval{*f.approval}, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonApprovalCancelled })).Return(nil, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
	f.repo.AssertExpectations(t)
	f.repo.AssertNotCalled(t, "DecideAutoOptimizeApproval", mock.Anything, mock.Anything, mock.Anything, model.AutoOptimizeApprovalStatusApproved, mock.Anything, mock.Anything, mock.Anything)
}

// Turning the gate off cancels too: the plan was only ever meant to run after
// review.
func TestDecideApproval_GateTurnedOffCancelsPlan(t *testing.T) {
	f := newDecideFixture(t)
	f.ao.Attributes.RequireApproval = false
	f.repo.On("CancelPendingAutoOptimizeApprovals", mock.Anything, f.ao.ID).Return([]model.AutoOptimizeApproval{}, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
}

// A decision after the window closed is refused and the approval is expired.
func TestDecideApproval_ExpiredIsRefused(t *testing.T) {
	f := newDecideFixture(t)
	f.approval.Attributes.ExpiresAt = approvalTestNow.Add(-time.Minute)
	f.repo.On("ExpireAutoOptimizeApprovals", mock.Anything, f.ao.ID, approvalTestNow).Return([]model.AutoOptimizeApproval{}, nil)
	f.repo.On("GetPendingAutoOptimizeApprovals", mock.Anything, f.ao.ID).Return([]model.AutoOptimizeApproval{}, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
	f.repo.AssertNotCalled(t, "DecideAutoOptimizeApproval", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Losing the race to another decision (or the expiry) is a conflict, and
// nothing is applied.
func TestDecideApproval_LostRaceIsConflict(t *testing.T) {
	f := newDecideFixture(t)
	f.parkedTask(uuid.New(), map[string]any{})
	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
	f.repo.AssertNotCalled(t, "MoveAutoOptimizeTasks", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDecideApproval_AlreadyDecidedIsConflict(t *testing.T) {
	f := newDecideFixture(t)
	f.approval.Status = model.AutoOptimizeApprovalStatusApproved

	_, err := f.decide(ApprovalDecisionReject, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
}

func TestDecideApproval_RequiresSignedInUser(t *testing.T) {
	f := newDecideFixture(t)
	f.sc = security.NewRequestContextForTenantAccountAdmin(f.ao.TenantID.String(), "", []string{f.ao.AccountID.String()})

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 401, approvalErrCode(t, err))
}

func TestDecideApproval_RejectsUnknownDecision(t *testing.T) {
	f := newDecideFixture(t)
	_, err := f.decide("MAYBE", "")
	assert.Equal(t, 400, approvalErrCode(t, err))
}

// Run now is refused with a clear message while a run waits for approval.
func TestExecuteAutoOptimize_RefusedWhileApprovalOpen(t *testing.T) {
	withApprovalClock(t)
	mockRepo := new(MockOptimizerRepository)
	svc := &optimizerService{dao: mockRepo, factory: NewExecutorFactory()}
	ao := gatedAutoOptimize()
	mockRepo.On("GetAutoOptimize", mock.Anything, ao.ID).Return(ao, nil)
	mockRepo.On("GetPendingAutoOptimizeApprovals", mock.Anything, ao.ID).Return([]model.AutoOptimizeApproval{{
		Status: model.AutoOptimizeApprovalStatusPending, Attributes: model.AutoOptimizeApprovalAttributes{ExpiresAt: approvalTestNow.Add(time.Hour)},
	}}, nil)

	err := svc.ExecuteAutoOptimize(context.Background(), ao.ID)
	assert.Equal(t, 409, approvalErrCode(t, err))
}

func TestPlanFingerprint_IgnoresNonPlanSettings(t *testing.T) {
	a := gatedAutoOptimize()
	base := a.PlanFingerprint()

	b := *a
	b.Notification = map[string]any{"slack": map[string]any{"enabled": true, "channel_id": "C1"}}
	b.ScheduleTime = "*/5 * * * *"
	name := "renamed"
	b.Name = &name
	assert.Equal(t, base, b.PlanFingerprint(), "notification, schedule and name do not change the plan")

	ns2 := "billing"
	c := *a
	c.ResourceFilters = append([]model.AutoOptimizeResourceFilter{{Namespace: &ns2}}, a.ResourceFilters...)
	assert.NotEqual(t, base, c.PlanFingerprint(), "scope changes the plan")

	d := *a
	d.ResourceFilters = []model.AutoOptimizeResourceFilter{c.ResourceFilters[1], c.ResourceFilters[0]}
	assert.Equal(t, c.PlanFingerprint(), d.PlanFingerprint(), "filter order does not matter")

	e := *a
	e.Attributes.GitOpsConfig.Enabled = true
	assert.NotEqual(t, base, e.PlanFingerprint(), "switching to GitOps changes how the plan applies")
}

func TestRequiresApproval(t *testing.T) {
	ao := gatedAutoOptimize()
	assert.True(t, ao.RequiresApproval())

	dry := *ao
	dry.Status = model.AutoOptimizeStatusDryrun
	assert.False(t, dry.RequiresApproval(), "a dry run applies nothing")

	cont := *ao
	cont.Category = "continuous_rightsize"
	assert.False(t, cont.RequiresApproval(), "continuous rightsizing has no plan to review")

	off := *ao
	off.Attributes.RequireApproval = false
	assert.False(t, off.RequiresApproval())
}

// An edit that saves after the approval is decided but before its tasks move is
// caught by the second look, and the plan is cancelled rather than applied.
func TestDecideApproval_EditRacingTheDecisionCancels(t *testing.T) {
	withApprovalClock(t)
	f := &decideFixture{repo: new(MockOptimizerRepository), temporal: &mocks.Client{}, ao: gatedAutoOptimize(), reviewer: uuid.New()}
	f.svc = &optimizerService{dao: f.repo, factory: NewExecutorFactory(), temporalClient: f.temporal}
	f.sc = security.NewRequestContextForTenantAccountAdmin(f.ao.TenantID.String(), f.reviewer.String(), []string{f.ao.AccountID.String()})
	f.approval = &model.AutoOptimizeApproval{
		ID: uuid.New(), AutoPilotID: f.ao.ID, TenantID: f.ao.TenantID, AccountID: f.ao.AccountID,
		Status:     model.AutoOptimizeApprovalStatusPending,
		Attributes: model.AutoOptimizeApprovalAttributes{ExpiresAt: approvalTestNow.Add(time.Hour), PlanFingerprint: f.ao.PlanFingerprint()},
	}
	task := f.parkedTask(uuid.New(), verticalContent(0.25, 0.2))
	edited := *f.ao
	edited.Rule = map[string]any{"cpu": false}

	f.repo.On("GetAutoOptimizeApproval", mock.Anything, f.approval.ID, f.ao.TenantID, f.ao.AccountID).Return(f.approval, nil)
	f.repo.On("GetAutoOptimize", mock.Anything, f.ao.ID).Return(f.ao, nil).Once()
	f.repo.On("GetAutoOptimize", mock.Anything, f.ao.ID).Return(&edited, nil).Once()
	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusApproved, model.AutoOptimizeApprovalStatusCancelled, mock.Anything, mock.Anything, (*time.Time)(nil)).Return(true, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped,
		mock.MatchedBy(func(r *string) bool { return *r == reasonApprovalCancelled })).Return(nil, nil)

	_, err := f.decide(ApprovalDecisionApprove, "")
	assert.Equal(t, 409, approvalErrCode(t, err))
	f.repo.AssertExpectations(t)
	f.temporal.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPlannedValuesHash(t *testing.T) {
	base := model.PlannedValuesHash("vertical_rightsize", verticalContent(0.25, 0.2))
	assert.NotEmpty(t, base)
	assert.Equal(t, base, model.PlannedValuesHash("vertical_rightsize", verticalContent(0.25, 0.7)), "scan metrics do not count as a change")
	assert.NotEqual(t, base, model.PlannedValuesHash("vertical_rightsize", verticalContent(0.3, 0.2)), "a moved recommendation does")
	assert.Empty(t, model.PlannedValuesHash("horizontal_rightsize", map[string]any{"recommended_replicas": 3.0}), "replica plans are applied as reviewed")
	assert.Empty(t, model.PlannedValuesHash("pvc_rightsize", map[string]any{"spec": map[string]any{}}))
}

// If the parked tasks left Pending_Approval before the approval moved them (a
// concurrent cancel, say), nothing is started and the reviewer is told so.
func TestDecideApproval_NothingMovedStartsNothing(t *testing.T) {
	f := newDecideFixture(t)
	rec := uuid.New()
	planned := verticalContent(0.25, 0.2)
	task := f.parkedTask(rec, planned)

	f.repo.On("DecideAutoOptimizeApproval", mock.Anything, f.approval.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	f.repo.On("GetAutoOptimizeTasksByIDs", mock.Anything, mock.Anything).Return([]model.AutoOptimizeTask{task}, nil)
	f.repo.On("GetRecommendationSnapshots", mock.Anything, mock.Anything).Return(map[uuid.UUID]model.RecommendationSnapshot{rec: {Status: "Open", Content: planned}}, nil)
	f.repo.On("GetActiveResolutionsForRecommendations", mock.Anything, mock.Anything).Return(map[uuid.UUID][]model.RecommendationResolution{}, nil)
	f.repo.On("MoveAutoOptimizeTasks", mock.Anything, []uuid.UUID{task.ID}, model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusScheduled, (*string)(nil)).Return([]uuid.UUID{}, nil)

	msg, err := f.decide(ApprovalDecisionApprove, "")
	require.NoError(t, err)
	assert.Contains(t, msg, "nothing was changed")
	f.temporal.AssertNotCalled(t, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	f.repo.AssertNotCalled(t, "SetAutoOptimizeExecutionStatus", mock.Anything, mock.Anything, mock.Anything)
}

func TestApprovalButtonParams(t *testing.T) {
	ao := gatedAutoOptimize()
	a := model.AutoOptimizeApproval{ID: uuid.New()}

	for _, platform := range []string{"slack", "ms_teams"} {
		params := approvalButtonParams(ao, a, platform)
		// notifications-server splits this token on ":" into approval, account and tenant.
		assert.Equal(t, "auto_optimize:"+a.ID.String()+":"+ao.AccountID.String()+":"+ao.TenantID.String(), params["approval_token"], platform)
		assert.Equal(t, []string{"approve", "reject"}, params["approval_options"], platform)
	}
	// Google Chat has no handler for approval buttons.
	assert.Nil(t, approvalButtonParams(ao, a, "google_chat"))
}
