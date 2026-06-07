package workflow

import (
	"database/sql"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/tasks/scripting"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// TestExecuteWorkflowNoLiveVersion is a regression guard for the
// runWorkflowVersion refactor: the live-version-required check at the top of
// ExecuteWorkflow must still fire when workflows.live_version_id is nil,
// even though most of the original body moved into a shared helper.
func TestExecuteWorkflowNoLiveVersion(t *testing.T) {
	service, _, _, sc := newExecuteTestService()

	mockStore := service.store.(*MockWorkflowStore)
	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-no-live").
		Return(&model.Workflow{
			ID:         "wf-no-live",
			Name:       "wf-no-live",
			Status:     model.WorkflowStatusActive,
			Definition: model.WorkflowDefinition{Tasks: []model.Task{{ID: "t1", Type: "scripting.run_script"}}},
			// LiveVersionID intentionally nil
		}, nil).Once()

	_, err := service.ExecuteWorkflow(sc, "test-account", "wf-no-live", model.WorkflowTriggerManual, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has no live version")
}

// TestExecuteWorkflowStampsLiveVersionMemo verifies the happy path through
// the refactored helper: live snapshot is loaded, its definition overrides
// the draft, and the Temporal Memo gets the live version's ID/number/name.
func TestExecuteWorkflowStampsLiveVersionMemo(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	liveVersionID := "v-live-1"
	liveVersionName := "v1-prod"
	wf := &model.Workflow{
		ID:            "wf-live",
		Name:          "wf-live",
		Status:        model.WorkflowStatusActive,
		Definition:    model.WorkflowDefinition{Tasks: []model.Task{{ID: "draft-task", Type: "scripting.run_script"}}},
		LiveVersionID: strPtrLocal(liveVersionID),
	}
	liveSnapshot := &model.WorkflowVersion{
		ID:            liveVersionID,
		WorkflowID:    "wf-live",
		VersionNumber: 1,
		Name:          strPtrLocal(liveVersionName),
		Source:        model.WorkflowVersionSourcePublish,
		Definition:    model.WorkflowDefinition{Tasks: []model.Task{{ID: "live-task", Type: "scripting.run_script"}}},
	}

	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-live").Return(wf, nil).Once()
	mockStore.On("GetLiveWorkflowVersion", mock.Anything, "wf-live").Return(liveSnapshot, nil).Once()

	mockRun := &MockWorkflowRun{}
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		if opts.Memo == nil {
			return false
		}
		if opts.Memo[model.MemoWorkflowVersionID] != liveVersionID {
			return false
		}
		if opts.Memo[model.MemoWorkflowVersionNumber] != int64(1) {
			return false
		}
		if opts.Memo[model.MemoWorkflowVersionName] != liveVersionName {
			return false
		}
		return true
	}), mock.Anything, mock.MatchedBy(func(wf *model.Workflow) bool {
		// Live snapshot tasks should override the draft tasks.
		return len(wf.Definition.Tasks) == 1 && wf.Definition.Tasks[0].ID == "live-task"
	}), mock.Anything).Return(mockRun, nil).Once()

	runID, err := service.ExecuteWorkflow(sc, "test-account", "wf-live", model.WorkflowTriggerManual, nil)
	assert.NoError(t, err)
	assert.Equal(t, "test-run-id", runID)
	// SetLiveVersion is a pointer flip only — it must not happen on Execute.
	mockStore.AssertNotCalled(t, "SetLiveVersion", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockTemporal.AssertExpectations(t)
}

// TestTriggerWorkflowFromDraftRunsDraftNoVersion covers the "Run current" canvas
// path: run the on-screen draft definition directly, tagging the execution with
// the draft-run Memo marker — without writing any workflow_versions row, without
// stamping a version into Memo, and without touching the live pointer. A Paused
// workflow must still accept this manual draft run.
func TestTriggerWorkflowFromDraftRunsDraftNoVersion(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	existingLive := "v-live-1" // workflow already has a live version
	wf := &model.Workflow{
		ID:            "wf-draft",
		Name:          "wf-draft",
		Status:        model.WorkflowStatusPaused, // Run current must work on non-Active runnable statuses
		Definition:    model.WorkflowDefinition{Tasks: []model.Task{{ID: "draft-task", Type: "scripting.run_script"}}},
		LiveVersionID: strPtrLocal(existingLive),
	}

	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-draft").Return(wf, nil).Once()

	mockRun := &MockWorkflowRun{}
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		if opts.Memo == nil {
			return false
		}
		// Draft-run marker present, no version linkage.
		return opts.Memo[model.MemoWorkflowIsDraftRun] == true &&
			opts.Memo[model.MemoWorkflowVersionID] == nil
	}), mock.Anything, mock.MatchedBy(func(arg *model.Workflow) bool {
		// Runs the current draft definition as-is.
		return len(arg.Definition.Tasks) == 1 && arg.Definition.Tasks[0].ID == "draft-task"
	}), mock.Anything).Return(mockRun, nil).Once()

	runID, err := service.TriggerWorkflowFromDraft(sc, "test-account", "wf-draft", nil)
	assert.NoError(t, err)
	assert.Equal(t, "test-run-id", runID)
	// Critical invariants: no version row written, live pointer untouched, and we
	// must not have asked for the live snapshot.
	mockStore.AssertNotCalled(t, "PublishVersion", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SetLiveVersion", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "GetLiveWorkflowVersion", mock.Anything, mock.Anything)
	mockTemporal.AssertExpectations(t)
}

// TestTriggerWorkflowFromDraftRejectsInactiveStatus locks in the status
// gate: Active/Paused run; everything else (e.g. "INACTIVE") is refused.
// Without this guard a deleted-but-not-purged workflow could be hot-revived
// via the draft path.
func TestTriggerWorkflowFromDraftRejectsInactiveStatus(t *testing.T) {
	service, _, mockStore, sc := newExecuteTestService()

	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-inactive").
		Return(&model.Workflow{
			ID:     "wf-inactive",
			Status: model.WorkflowStatus("INACTIVE"),
		}, nil).Once()

	_, err := service.TriggerWorkflowFromDraft(sc, "test-account", "wf-inactive", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not runnable")
	// Must short-circuit before any version write.
	mockStore.AssertNotCalled(t, "PublishVersion", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestTriggerWorkflowFromDraftAuth verifies the account-access gate. Without
// SecurityAccessTypeCreate on the account, the call must short-circuit
// before any store or temporal interaction.
func TestTriggerWorkflowFromDraftAuth(t *testing.T) {
	service, _, mockStore, _ := newExecuteTestService()

	// Build a context for a different account so HasAccountAccess returns false.
	scNoAccess := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"other-account"})

	_, err := service.TriggerWorkflowFromDraft(scNoAccess, "test-account", "wf-any", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "account not accessible")
	mockStore.AssertNotCalled(t, "Find", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "PublishVersion", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// newExecuteTestService wires up a Service with mock store + temporal so the
// trigger-path tests in this file don't repeat the boilerplate.
func newExecuteTestService() (*Service, *MockTemporalClient, *MockWorkflowStore, *security.RequestContext) {
	mockTemporal := &MockTemporalClient{}
	mockStore := new(MockWorkflowStore)
	mockDataConverter := converter.GetDefaultDataConverter()
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})
	mockConfigService := new(MockConfigServiceDryRun)

	executor := &WorkflowExecutor{
		temporalClient: mockTemporal,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	service := NewService(mockTemporal, mockStore, mockDataConverter, mockTaskRegistry, executor, mockConfigService)

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	return service, mockTemporal, mockStore, sc
}

// TestCreateWorkflowDefaultsPaused verifies a workflow created with no explicit
// status now defaults to PAUSED (V746 moved the runtime gate to per-version
// status; workflows opt in via the publish dialog). The schedule is still
// registered so the worker has a handle, but it is created paused — see
// handleWorkflowTrigger's `paused := wf.Status == WorkflowStatusPaused`.
func TestCreateWorkflowDefaultsPaused(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	wf := model.Workflow{
		Name: "wf-default-paused",
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}}},
			Tasks:    []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
		},
		// Status intentionally empty -> must default to PAUSED.
	}

	mockStore.On("FindByName", mock.Anything, "test-tenant", "test-account", wf.Name).Return(nil, sql.ErrNoRows)
	mockStore.On("CreateWorkflowWithInitialVersion", mock.Anything, "test-tenant", "test-account", mock.MatchedBy(func(w model.Workflow) bool {
		return w.Status == model.WorkflowStatusPaused
	})).Return("wf-dp", &model.WorkflowVersion{ID: "v1", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate, IsLive: true, Status: model.WorkflowStatusPaused}, nil)

	// PAUSED status still creates the schedule handle so the temporal worker
	// knows the workflow exists; the schedule is just created in a paused
	// state and won't fire until the user activates.
	mockTemporal.On("Describe", "workflow-schedule-wf-dp-0").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		return opts.ID == "workflow-schedule-wf-dp-0"
	})).Return(&MockScheduleHandle{}, nil)
	mockTemporal.On("Describe", "workflow-schedule-wf-dp").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("List", mock.Anything, mock.Anything).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

	_, _, err := service.CreateWorkflow(sc, "test-account", wf)
	assert.NoError(t, err)
	mockStore.AssertCalled(t, "CreateWorkflowWithInitialVersion", mock.Anything, "test-tenant", "test-account", mock.MatchedBy(func(w model.Workflow) bool {
		return w.Status == model.WorkflowStatusPaused
	}))
	mockTemporal.AssertCalled(t, "Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		return opts.ID == "workflow-schedule-wf-dp-0"
	}))
}

// TestPublishWorkflowWithExplicitActiveStatus locks in the new contract: the
// caller picks the version's status at publish time (ACTIVE here). SetLiveVersion
// mirrors that status onto workflows.status in the same UPDATE, so the service
// no longer needs a separate UpdateWorkflowStatus call. Triggers are still
// (re)registered on every publish so a newly added schedule/webhook trigger
// in the published definition takes effect immediately.
func TestPublishWorkflowWithExplicitActiveStatus(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	wf := &model.Workflow{
		ID:     "wf-pub",
		Name:   "wf-pub",
		Status: model.WorkflowStatusPaused,
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}}},
			Tasks:    []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
		},
	}
	publishedVersion := &model.WorkflowVersion{ID: "v-pub-2", WorkflowID: "wf-pub", VersionNumber: 2, Source: model.WorkflowVersionSourcePublish, Status: model.WorkflowStatusActive}

	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-pub").Return(wf, nil)
	mockStore.On("PublishVersion", mock.Anything, "wf-pub", "test-user", model.WorkflowVersionSourcePublish, (*string)(nil), (*string)(nil), (*int)(nil), model.WorkflowStatusActive).
		Return(publishedVersion, nil).Once()
	mockStore.On("SetLiveVersion", mock.Anything, "test-tenant", "test-account", "wf-pub", "v-pub-2").Return(nil).Once()

	mockTemporal.On("Describe", "workflow-schedule-wf-pub-0").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		return opts.ID == "workflow-schedule-wf-pub-0"
	})).Return(&MockScheduleHandle{}, nil)
	mockTemporal.On("Describe", "workflow-schedule-wf-pub").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("List", mock.Anything, mock.Anything).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

	v, err := service.PublishWorkflow(sc, "test-account", "wf-pub", nil, nil, true, model.WorkflowStatusActive)
	assert.NoError(t, err)
	assert.Equal(t, "v-pub-2", v.ID)
	assert.True(t, v.IsLive)
	// The dedicated UpdateWorkflowStatus call is gone — SetLiveVersion now
	// mirrors the version's status onto the workflow row atomically.
	mockStore.AssertNotCalled(t, "UpdateWorkflowStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestPublishWorkflowRegistersTriggersWhenAlreadyActive locks in the recovery
// path: when the workflow is already ACTIVE (e.g. a prior publish failed to
// register triggers and is being retried), publish must still re-register the
// triggers — registration is unconditional, not gated on a status flip.
func TestPublishWorkflowRegistersTriggersWhenAlreadyActive(t *testing.T) {
	service, mockTemporal, mockStore, sc := newExecuteTestService()

	wf := &model.Workflow{
		ID:     "wf-active",
		Name:   "wf-active",
		Status: model.WorkflowStatusActive, // already active
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}}},
			Tasks:    []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
		},
	}
	publishedVersion := &model.WorkflowVersion{ID: "v-active-3", WorkflowID: "wf-active", VersionNumber: 3, Source: model.WorkflowVersionSourcePublish, Status: model.WorkflowStatusActive}

	mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-active").Return(wf, nil)
	mockStore.On("PublishVersion", mock.Anything, "wf-active", "test-user", model.WorkflowVersionSourcePublish, (*string)(nil), (*string)(nil), (*int)(nil), model.WorkflowStatusActive).
		Return(publishedVersion, nil).Once()
	mockStore.On("SetLiveVersion", mock.Anything, "test-tenant", "test-account", "wf-active", "v-active-3").Return(nil).Once()

	// Triggers must still be (re)registered even though the status is unchanged.
	mockTemporal.On("Describe", "workflow-schedule-wf-active-0").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		return opts.ID == "workflow-schedule-wf-active-0"
	})).Return(&MockScheduleHandle{}, nil)
	mockTemporal.On("Describe", "workflow-schedule-wf-active").Return(nil, serviceerror.NewNotFound("not found"))
	mockTemporal.On("List", mock.Anything, mock.Anything).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

	v, err := service.PublishWorkflow(sc, "test-account", "wf-active", nil, nil, true, model.WorkflowStatusActive)
	assert.NoError(t, err)
	assert.True(t, v.IsLive)
	// Status mirror happens inside SetLiveVersion now, so there's no separate
	// UpdateWorkflowStatus call to assert on.
	mockStore.AssertNotCalled(t, "UpdateWorkflowStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	// ...but triggers were registered regardless (the recovery guarantee).
	mockTemporal.AssertCalled(t, "Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		return opts.ID == "workflow-schedule-wf-active-0"
	}))
}

func strPtrLocal(s string) *string { return &s }
