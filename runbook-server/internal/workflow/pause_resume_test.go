package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

func TestPauseResumeWorkflow_MissingSchedule(t *testing.T) {
	mockTemporalClient := &MockTemporalClient{}
	mockDataConverter := converter.GetDefaultDataConverter()
	mockStore := new(MockWorkflowStore)
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockConfigService := new(MockConfigService)
	workflowExector := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	service := NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExector, mockConfigService)

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	workflowID := "wf-no-schedule"

	// Pause/Resume now route DB writes through applyStatusToLiveVersion which
	// looks up the workflow to decide between UpdateVersionStatus (when a live
	// version exists) and UpdateWorkflowStatus (legacy fallback). The fixtures
	// here intentionally leave LiveVersionID nil so the fallback runs and the
	// expected DB write is the legacy UpdateWorkflowStatus call.
	wfNoLive := &model.Workflow{ID: workflowID, TenantID: "test-tenant", AccountID: "test-account"}

	t.Run("PauseWorkflow updates DB only for non-scheduled workflow", func(t *testing.T) {
		// Mock Legacy Schedule Check (Expect NotFound)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Mock Indexed Schedule Check (Expect loop break on NotFound)
		// The loop starts with index 0
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-0").Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect lookup + DB update via the legacy fallback path.
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", workflowID).Return(wfNoLive, nil)
		mockStore.On("UpdateWorkflowStatus", mock.Anything, "test-tenant", "test-account", workflowID, model.WorkflowStatusPaused).Return(nil)

		err := service.PauseWorkflow(sc, "test-account", workflowID)
		assert.NoError(t, err)

		mockStore.AssertExpectations(t)
		// We assert that Pause was NOT called. Since Pause is the only tracked method that would be called if logic failed,
		// asserting expectations (which checks Pause is not in there) is implicit if we didn't add it.
		// But we can also explicitly check:
		mockTemporalClient.AssertNotCalled(t, "Pause")
	})

	t.Run("ResumeWorkflow updates DB only for non-scheduled workflow", func(t *testing.T) {
		mockStore.ExpectedCalls = nil
		mockTemporalClient.ExpectedCalls = nil

		// Mock Legacy Schedule Check (Expect NotFound)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Mock Indexed Schedule Check (Expect loop break on NotFound)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-0").Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect lookup + DB update via the legacy fallback path.
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", workflowID).Return(wfNoLive, nil)
		mockStore.On("UpdateWorkflowStatus", mock.Anything, "test-tenant", "test-account", workflowID, model.WorkflowStatusActive).Return(nil)

		err := service.ResumeWorkflow(sc, "test-account", workflowID)
		assert.NoError(t, err)

		mockStore.AssertExpectations(t)
		mockTemporalClient.AssertNotCalled(t, "Unpause")
	})
}

func TestPauseResumeWorkflow_ScheduleExists(t *testing.T) {
	mockTemporalClient := &MockTemporalClient{}
	mockDataConverter := converter.GetDefaultDataConverter()
	mockStore := new(MockWorkflowStore)
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockConfigService := new(MockConfigService)
	workflowExector := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	service := NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExector, mockConfigService)

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	workflowID := "wf-with-schedule"
	scheduleID := "workflow-schedule-" + workflowID

	// Define a workflow WITH a schedule trigger
	scheduleWf := &model.Workflow{
		ID: workflowID,
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{
				{Type: model.WorkflowTriggerSchedule},
			},
		},
	}

	t.Run("PauseWorkflow pauses schedule", func(t *testing.T) {
		// Mock Store Find — used both by the legacy guard and (now) by
		// applyStatusToLiveVersion's branch on LiveVersionID. The fixture leaves
		// LiveVersionID nil so the fallback UpdateWorkflowStatus path runs.
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", workflowID).Return(scheduleWf, nil)

		// Mock Legacy Schedule Check (Found)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID).Return(&client.ScheduleDescription{}, nil)

		// Mock Indexed Schedule Check (Found then NotFound)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-0").Return(&client.ScheduleDescription{}, nil)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-1").Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Mock Temporal calls
		// NOTE: ScheduleClient() and GetHandle() in the mock implementation do NOT record calls, so we don't expect them.
		// Only Pause() records the call.
		mockTemporalClient.On("Pause", scheduleID, mock.Anything).Return(nil)
		mockTemporalClient.On("Pause", scheduleID+"-0", mock.Anything).Return(nil)

		// Expect DB update via the legacy UpdateWorkflowStatus fallback path.
		mockStore.On("UpdateWorkflowStatus", mock.Anything, "test-tenant", "test-account", workflowID, model.WorkflowStatusPaused).Return(nil)

		err := service.PauseWorkflow(sc, "test-account", workflowID)
		assert.NoError(t, err)
		mockTemporalClient.AssertExpectations(t)
	})

	t.Run("ResumeWorkflow unpauses schedule", func(t *testing.T) {
		mockTemporalClient.ExpectedCalls = nil
		mockStore.ExpectedCalls = nil

		// Mock Store Find — see comment above on applyStatusToLiveVersion routing.
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", workflowID).Return(scheduleWf, nil)

		// Mock Legacy Schedule Check (Found)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID).Return(&client.ScheduleDescription{}, nil)

		// Mock Indexed Schedule Check (Found then NotFound)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-0").Return(&client.ScheduleDescription{}, nil)
		mockTemporalClient.On("Describe", "workflow-schedule-"+workflowID+"-1").Return(nil, serviceerror.NewNotFound("schedule not found"))

		mockTemporalClient.On("Unpause", scheduleID, mock.Anything).Return(nil)
		mockTemporalClient.On("Unpause", scheduleID+"-0", mock.Anything).Return(nil)
		mockStore.On("UpdateWorkflowStatus", mock.Anything, "test-tenant", "test-account", workflowID, model.WorkflowStatusActive).Return(nil)

		err := service.ResumeWorkflow(sc, "test-account", workflowID)
		assert.NoError(t, err)
		mockTemporalClient.AssertExpectations(t)
	})
}
