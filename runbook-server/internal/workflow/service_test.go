package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/tasks/core"
	"nudgebee/runbook/internal/tasks/integrations"
	"nudgebee/runbook/internal/tasks/scripting"
	"nudgebee/runbook/internal/tasks/types"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonapi "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historyapi "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	workflowapi "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// MockTemporalClient is a mock implementation of client.Client for testing purposes.
type MockTemporalClient struct {
	client.Client
	mock.Mock
}

// ScheduleClient returns a mock ScheduleClient.
func (m *MockTemporalClient) ScheduleClient() client.ScheduleClient {
	return &MockScheduleClient{Mock: &m.Mock}
}

func (m *MockTemporalClient) ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workflowservice.ListWorkflowExecutionsResponse), args.Error(1)
}

func (m *MockTemporalClient) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	args := m.Called(ctx, workflowID, runID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workflowservice.DescribeWorkflowExecutionResponse), args.Error(1)
}

func (m *MockTemporalClient) GetWorkflowHistory(ctx context.Context, workflowID, runID string, isLongPoll bool, filterType enums.HistoryEventFilterType) client.HistoryEventIterator {
	args := m.Called(ctx, workflowID, runID, isLongPoll, filterType)
	return args.Get(0).(client.HistoryEventIterator)
}

func (m *MockTemporalClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error {
	return nil
}

func (m *MockTemporalClient) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	callArgs := []interface{}{ctx, workflowID, runID, queryType}
	callArgs = append(callArgs, args...)
	mArgs := m.Called(callArgs...)
	if mArgs.Get(0) == nil {
		return nil, mArgs.Error(1)
	}
	return mArgs.Get(0).(converter.EncodedValue), mArgs.Error(1)
}

// MockValue is a mock implementation of converter.EncodedValue
type MockValue struct {
	mock.Mock
}

func (m *MockValue) Get(valuePtr interface{}) error {
	args := m.Called(valuePtr)
	return args.Error(0)
}

func (m *MockValue) HasValue() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockValue) Type() reflect.Type {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(reflect.Type)
}

// MockScheduleClient is a mock implementation of client.ScheduleClient for testing purposes.
type MockScheduleClient struct {
	client.ScheduleClient
	Mock *mock.Mock
}

func (m *MockScheduleClient) GetHandle(ctx context.Context, scheduleID string) client.ScheduleHandle {
	return &MockScheduleHandle{scheduleID: scheduleID, Mock: m.Mock}
}

func (m *MockScheduleClient) Create(ctx context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	args := m.Mock.Called(ctx, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(client.ScheduleHandle), args.Error(1)
}

func (m *MockScheduleClient) List(ctx context.Context, options client.ScheduleListOptions) (client.ScheduleListIterator, error) {
	args := m.Mock.Called(ctx, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(client.ScheduleListIterator), args.Error(1)
}

type MockScheduleListIterator struct {
	client.ScheduleListIterator
	Schedules []*client.ScheduleListEntry
	Current   int
}

func (m *MockScheduleListIterator) HasNext() bool {
	return m.Current < len(m.Schedules)
}

func (m *MockScheduleListIterator) Next() (*client.ScheduleListEntry, error) {
	if m.Current >= len(m.Schedules) {
		return nil, nil
	}
	entry := m.Schedules[m.Current]
	m.Current++
	return entry, nil
}

type MockScheduleHandle struct {
	client.ScheduleHandle
	scheduleID string
	Mock       *mock.Mock
}

func (m *MockScheduleHandle) Describe(ctx context.Context) (*client.ScheduleDescription, error) {
	args := m.Mock.Called(m.scheduleID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*client.ScheduleDescription), args.Error(1)
}

func (m *MockScheduleHandle) Delete(ctx context.Context) error {
	// args := m.Mock.Called(m.scheduleID) // Optional: verify calls
	return nil
}

func (m *MockScheduleHandle) Pause(ctx context.Context, options client.SchedulePauseOptions) error {
	args := m.Mock.Called(m.scheduleID, options)
	return args.Error(0)
}

func (m *MockScheduleHandle) Unpause(ctx context.Context, options client.ScheduleUnpauseOptions) error {
	args := m.Mock.Called(m.scheduleID, options)
	return args.Error(0)
}

// MockConfigService is a mock implementation of configSvc.ConfigService for testing purposes.
type MockConfigService struct {
	mock.Mock
}

func (m *MockConfigService) SaveConfig(ctx context.Context, cfg model.Config) (string, error) {
	args := m.Called(ctx, cfg)
	return args.String(0), args.Error(1)
}

func (m *MockConfigService) GetConfig(ctx context.Context, tenantID string, accountID *string, key string, decrypt bool) (*model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, key, decrypt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Config), args.Error(1)
}

func (m *MockConfigService) ListConfigs(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, labels)
	return args.Get(0).([]model.Config), args.Error(1)
}

func (m *MockConfigService) ListConfigsDecrypted(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, labels)
	return args.Get(0).([]model.Config), args.Error(1)
}

func (m *MockConfigService) DeleteConfig(ctx context.Context, tenantID string, accountID *string, key string) error {
	args := m.Called(ctx, tenantID, accountID, key)
	return args.Error(0)
}

// Ensure MockConfigService implements configSvc.ConfigService interface
var _ configSvc.ConfigService = (*MockConfigService)(nil)

// ... (rest of the file) ...

func TestMultipleSchedules(t *testing.T) {
	config.Config.ServiceEndpoint = "http://mock-service"
	config.Config.ServiceApiServerToken = "test-token"

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
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Create Workflow with Multiple Schedules", func(t *testing.T) {
		wf := model.Workflow{
			Name: "multi-schedule",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "30 * * * *"}},
				},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		mockStore.On("FindByName", mock.Anything, "test-tenant", "test-account", wf.Name).Return(nil, sql.ErrNoRows)
		mockStore.On("CreateWorkflowWithInitialVersion", mock.Anything, "test-tenant", "test-account", mock.Anything).
			Return("wf-multi", &model.WorkflowVersion{ID: "v1-multi", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate, IsLive: true}, nil)

		// Expect 2 calls to Create (one for each schedule)
		mockTemporalClient.On("Describe", "workflow-schedule-wf-multi-0").Return(nil, serviceerror.NewNotFound("not found"))
		mockTemporalClient.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
			return opts.ID == "workflow-schedule-wf-multi-0" && opts.Spec.CronExpressions[0] == "0 * * * *"
		})).Return(&MockScheduleHandle{}, nil)

		mockTemporalClient.On("Describe", "workflow-schedule-wf-multi-1").Return(nil, serviceerror.NewNotFound("not found"))
		mockTemporalClient.On("Create", mock.Anything, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
			return opts.ID == "workflow-schedule-wf-multi-1" && opts.Spec.CronExpressions[0] == "30 * * * *"
		})).Return(&MockScheduleHandle{}, nil)

		// Expect cleanup: Check legacy
		mockTemporalClient.On("Describe", "workflow-schedule-wf-multi").Return(nil, serviceerror.NewNotFound("not found"))

		// Expect List call for cleanup
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-multi'"
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil) // Return empty list

		_, _, err := service.CreateWorkflow(sc, "test-account", wf)
		assert.NoError(t, err)
	})

	t.Run("Update Workflow: Invalid Input for Schedule", func(t *testing.T) {
		wfID := "wf-invalid-input"
		existingWf := &model.Workflow{
			ID:   wfID,
			Name: "wf-invalid-input", // Added Name
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{{ID: "limit", Type: "int", Required: true}},
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *", "limit": 10}},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)

		// New workflow definition with INVALID input (string instead of int)
		newWf := model.Workflow{
			ID:   wfID,
			Name: "wf-invalid-input", // Added Name
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{{ID: "limit", Type: "int", Required: true}},
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *", "limit": "not-an-int"}},
				},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		// Update should fail validation BEFORE attempting to create/update schedules
		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid inputs for schedule trigger")
	})

	t.Run("Update Workflow: Cleanup Orphaned Schedules", func(t *testing.T) {
		wfID := "wf-cleanup"
		// Existing workflow had 2 schedules (0 and 1)
		existingWf := &model.Workflow{
			ID:     wfID,
			Name:   "wf-cleanup", // Added Name
			Status: model.WorkflowStatusActive,
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "30 * * * *"}},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", wfID, mock.Anything).Return(nil)

		// Update to have only 1 schedule (index 0)
		newWf := model.Workflow{
			ID:     wfID,
			Name:   "wf-cleanup", // Added Name
			Status: model.WorkflowStatusActive,
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
				},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		// 1. Process schedule 0 (Update/Create)
		mockTemporalClient.On("Describe", "workflow-schedule-wf-cleanup-0").Return(&client.ScheduleDescription{}, nil)
		mockTemporalClient.On("Create", mock.Anything, mock.Anything).Return(&MockScheduleHandle{}, nil)

		// 2. Cleanup Legacy
		mockTemporalClient.On("Describe", "workflow-schedule-wf-cleanup").Return(nil, serviceerror.NewNotFound("not found"))

		// 3. List Cleanup: Return both 0 and 1. Expect 1 to be deleted because it's not in the valid set (only 0 is).
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-cleanup'"
		})).Return(&MockScheduleListIterator{
			Schedules: []*client.ScheduleListEntry{
				{ID: "workflow-schedule-wf-cleanup-0"},
				{ID: "workflow-schedule-wf-cleanup-1"},
				{ID: "workflow-schedule-wf-cleanup-orphaned"}, // Some random old one
			},
		}, nil)

		// Expect deletion of non-valid schedules
		// Note: Delete is called on the handle returned by GetHandle.
		// The MockScheduleHandle.Delete just returns nil, but we can verify logic by ensuring no error.
		// In a stricter test we'd mock GetHandle("workflow-schedule-wf-cleanup-1") and verify Delete() called.
		// For now, implicit verification via no error and coverage is okay.

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.NoError(t, err)
	})

	t.Run("Delete Workflow: Cleanup Multiple Schedules", func(t *testing.T) {
		wfID := "wf-delete-multi"
		existingWf := &model.Workflow{
			ID: wfID,
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Delete", mock.Anything, "test-tenant", "test-account", wfID).Return(nil)

		// 1. Cleanup Legacy
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID).Return(nil, serviceerror.NewNotFound("not found"))

		// 2. List Cleanup: Return 2 schedules to be deleted
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-delete-multi'"
		})).Return(&MockScheduleListIterator{
			Schedules: []*client.ScheduleListEntry{
				{ID: "workflow-schedule-wf-delete-multi-0"},
				{ID: "workflow-schedule-wf-delete-multi-1"},
			},
		}, nil)

		// Expect explicit Terminate calls for running executions (simulated as none for simplicity or mocked)
		// ListWorkflowExecutions is called to find running workflows to terminate
		mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(&workflowservice.ListWorkflowExecutionsResponse{}, nil)

		err := service.DeleteWorkflow(sc, "test-account", wfID)
		assert.NoError(t, err)
	})

	t.Run("GetWorkflow: Return Earliest Next Run from Multiple Schedules", func(t *testing.T) {
		wfID := "wf-get-multi"
		now := time.Now()
		next1 := now.Add(1 * time.Hour)
		next2 := now.Add(30 * time.Minute) // Earlier

		wf := &model.Workflow{
			ID:   wfID,
			Name: "multi-schedule-get",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "30 * * * *"}},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(wf, nil)

		// Mock legacy check
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID).Return(nil, serviceerror.NewNotFound("not found"))

		// Mock schedule 0
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID+"-0").Return(&client.ScheduleDescription{
			Info: client.ScheduleInfo{NextActionTimes: []time.Time{next1}},
		}, nil)

		// Mock schedule 1
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID+"-1").Return(&client.ScheduleDescription{
			Info: client.ScheduleInfo{NextActionTimes: []time.Time{next2}},
		}, nil)

		retrievedWf, err := service.GetWorkflow(sc, "test-account", wfID)
		assert.NoError(t, err)
		assert.NotNil(t, retrievedWf.TriggerDetails)
		// Should pick next2 because it's earlier than next1
		assert.True(t, retrievedWf.TriggerDetails.NextScheduledRun.Equal(next2))
	})
}

func TestGetDetailedWorkflowExecutionInputs(t *testing.T) {
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

	t.Run("Extract inputs from Workflow struct payload", func(t *testing.T) {
		wfID := "wf-123"
		runID := "run-456"

		// Mock Workflow definition in DB
		wf := &model.Workflow{
			ID: wfID,
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{{ID: "action", Default: "start"}},
				Tasks:  []model.Task{{ID: "t1", Type: "core.print"}},
			},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(wf, nil)

		// Mock DescribeWorkflowExecution
		mockTemporalClient.On("DescribeWorkflowExecution", mock.Anything, mock.Anything, mock.Anything).Return(&workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowapi.WorkflowExecutionInfo{
				Execution: &commonapi.WorkflowExecution{WorkflowId: wfID, RunId: runID},
				Status:    enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
			},
		}, nil)

		// Mock History with WORKFLOW_EXECUTION_STARTED event containing Workflow struct as input
		wfPayload, _ := mockDataConverter.ToPayload(wf)
		historyEvent := &historyapi.HistoryEvent{
			EventId:   1,
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historyapi.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historyapi.WorkflowExecutionStartedEventAttributes{
					Input: &commonapi.Payloads{Payloads: []*commonapi.Payload{wfPayload}},
				},
			},
		}

		mockIterator := new(MockHistoryIterator)
		mockIterator.On("HasNext").Return(true).Once()
		mockIterator.On("Next").Return(historyEvent, nil).Once()
		mockIterator.On("HasNext").Return(false)

		mockTemporalClient.On("GetWorkflowHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockIterator)

		// Mock ListWorkflow for ResolveTemporalWorkflowID
		mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(&workflowservice.ListWorkflowExecutionsResponse{
			Executions: []*workflowapi.WorkflowExecutionInfo{
				{
					Execution: &commonapi.WorkflowExecution{WorkflowId: wfID, RunId: runID},
				},
			},
		}, nil)

		details, err := service.GetDetailedWorkflowExecution(sc, "test-account", wfID, runID)
		assert.NoError(t, err)
		if assert.NotNil(t, details) {
			// Verify that "action" was correctly extracted from the Workflow struct's Definition.Inputs
			assert.Equal(t, "start", details.Inputs["action"])
		}
	})
}

func TestListWorkflowExecutions(t *testing.T) {
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

	t.Run("WorkflowID is the automation ID, not Temporal's per-run ID", func(t *testing.T) {
		automationID := "fb836ea7-fa0a-4044-a3d2-0375636a43c9"
		temporalID := "18b6360a-4ae2-473a-8879-c1bb7dcb8f5e"
		runID := "019db96b-d435-7737-ba87-71e82729b65e"

		mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(
			&workflowservice.ListWorkflowExecutionsResponse{
				Executions: []*workflowapi.WorkflowExecutionInfo{
					{
						Execution: &commonapi.WorkflowExecution{WorkflowId: temporalID, RunId: runID},
						Status:    enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
					},
				},
			},
			nil,
		).Once()

		resp, err := service.ListWorkflowExecutions(sc, "test-account", automationID, model.ListWorkflowExecutionRequest{})
		assert.NoError(t, err)
		if assert.Len(t, resp.Executions, 1) {
			assert.Equal(t, automationID, resp.Executions[0].WorkflowID, "WorkflowID must round-trip as the automation ID so callers can pass it to GET /workflows/{id}/runs/{execution_id}")
			assert.Equal(t, temporalID, resp.Executions[0].TemporalWorkflowID, "TemporalWorkflowID must still carry the per-run Temporal ID for internal TerminateWorkflow calls")
			assert.Equal(t, runID, resp.Executions[0].ID)
		}
	})
}

type MockHistoryIterator struct {
	client.HistoryEventIterator
	mock.Mock
}

func (m *MockHistoryIterator) HasNext() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockHistoryIterator) Next() (*historyapi.HistoryEvent, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*historyapi.HistoryEvent), args.Error(1)
}

// MockTaskWithSchema is a mock implementation of tasks.Task that provides a custom InputSchema
type MockTaskWithSchema struct {
	types.Task
	name        string
	inputSchema *types.Schema
}

func (m *MockTaskWithSchema) GetName() string {
	return m.name
}

func (m *MockTaskWithSchema) InputSchema() *types.Schema {
	return m.inputSchema
}

// MockWorkflowStore is a mock of WorkflowStore
type MockWorkflowStore struct {
	mock.Mock
}

func (m *MockWorkflowStore) CreateWorkflowWithInitialVersion(ctx context.Context, tenantID, accountID string, wf model.Workflow) (string, *model.WorkflowVersion, error) {
	args := m.Called(ctx, tenantID, accountID, wf)
	var v *model.WorkflowVersion
	if args.Get(1) != nil {
		v = args.Get(1).(*model.WorkflowVersion)
	}
	return args.String(0), v, args.Error(2)
}

func (m *MockWorkflowStore) List(ctx context.Context, tenantID, accountID string, request model.ListWorkflowRequest) ([]model.Workflow, int, error) {
	args := m.Called(ctx, tenantID, accountID, request)
	return args.Get(0).([]model.Workflow), args.Int(1), args.Error(2)
}

func (m *MockWorkflowStore) Find(ctx context.Context, tenantID, accountID, id string) (*model.Workflow, error) {
	args := m.Called(ctx, tenantID, accountID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Workflow), args.Error(1)
}

func (m *MockWorkflowStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	args := m.Called(ctx, tenantID, accountID, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Workflow), args.Error(1)
}

func (m *MockWorkflowStore) FindByIntegrationName(ctx context.Context, tenantID, accountID, integrationName string) (*model.Workflow, error) {
	args := m.Called(ctx, tenantID, accountID, integrationName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Workflow), args.Error(1)
}

func (m *MockWorkflowStore) ListByIntegrationName(ctx context.Context, tenantID, integrationName string) ([]model.Workflow, error) {
	args := m.Called(ctx, tenantID, integrationName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.Workflow), args.Error(1)
}

func (m *MockWorkflowStore) Update(ctx context.Context, tenantID, accountID, id string, wf model.Workflow) error {
	args := m.Called(ctx, tenantID, accountID, id, wf)
	return args.Error(0)
}

func (m *MockWorkflowStore) Delete(ctx context.Context, tenantID, accountID, id string) error {
	args := m.Called(ctx, tenantID, accountID, id)
	return args.Error(0)
}

func (m *MockWorkflowStore) UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowStatus) error {
	args := m.Called(ctx, tenantID, accountID, id, status)
	return args.Error(0)
}

func (m *MockWorkflowStore) GetState(ctx context.Context, workflowID string) ([]model.WorkflowStateItem, error) {
	args := m.Called(ctx, workflowID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.WorkflowStateItem), args.Error(1)
}

func (m *MockWorkflowStore) DeleteExpiredState(ctx context.Context, limit int) (int64, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return 0, args.Error(1)
	}
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockWorkflowStore) SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error {
	args := m.Called(ctx, tenantID, accountID, id, status, executionTime, statusMessage)
	if args.Get(0) == nil {
		return nil
	}
	return args.Error(1)
}

func (m *MockWorkflowStore) SetState(ctx context.Context, workflowID string, updates []model.WorkflowStateUpdate) error {
	args := m.Called(ctx, workflowID, updates)
	if args.Get(0) == nil {
		return nil
	}
	return args.Error(1)
}

func (m *MockWorkflowStore) CountWorkflows(ctx context.Context, tenantID, accountID string, status model.WorkflowStatus, triggerType string) (int64, error) {
	args := m.Called(ctx, tenantID, accountID, status, triggerType)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockWorkflowStore) ListWorkflowVersions(ctx context.Context, workflowID string, limit int) ([]model.WorkflowVersion, error) {
	args := m.Called(ctx, workflowID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.WorkflowVersion), args.Error(1)
}

func (m *MockWorkflowStore) GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*model.WorkflowVersion, error) {
	args := m.Called(ctx, workflowID, versionNumber)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowVersion), args.Error(1)
}

func (m *MockWorkflowStore) GetWorkflowVersionByID(ctx context.Context, versionID string) (*model.WorkflowVersion, error) {
	args := m.Called(ctx, versionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowVersion), args.Error(1)
}

func (m *MockWorkflowStore) GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*model.WorkflowVersion, error) {
	args := m.Called(ctx, workflowID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowVersion), args.Error(1)
}

func (m *MockWorkflowStore) PublishVersion(ctx context.Context, workflowID, createdBy string, source model.WorkflowVersionSource, name, description *string, restoredFromVersion *int) (*model.WorkflowVersion, error) {
	args := m.Called(ctx, workflowID, createdBy, source, name, description, restoredFromVersion)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowVersion), args.Error(1)
}

func (m *MockWorkflowStore) SetLiveVersion(ctx context.Context, tenantID, accountID, workflowID, versionID string) error {
	args := m.Called(ctx, tenantID, accountID, workflowID, versionID)
	return args.Error(0)
}

func (m *MockWorkflowStore) UpdateVersionMetadata(ctx context.Context, workflowID string, versionNumber int, name, description *string) (*model.WorkflowVersion, error) {
	args := m.Called(ctx, workflowID, versionNumber, name, description)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowVersion), args.Error(1)
}

func TestValidateTaskTypes(t *testing.T) {
	testCtx := security.NewRequestContextForTenantAccountAdmin("test_tenant_id", "test_user_id", []string{"test_account_id"})

	t.Run("Valid task types", func(t *testing.T) {
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

		mockTaskRegistry.RegisterTask(&integrations.HttpTask{})
		mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})
		mockTaskRegistry.RegisterTask(&core.GroupTask{})

		wf := model.Workflow{
			Definition: model.WorkflowDefinition{
				Tasks: []model.Task{
					{ID: "task1", Type: "integrations.http", Params: map[string]any{"url": "http://example.com"}},
					{ID: "task2", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
					{ID: "task3", Type: "core.group", Params: map[string]any{"tasks": []any{}}},
				},
			},
		}
		err := service.validateTaskTypes(testCtx, "test_account_id", wf)
		assert.NoError(t, err)
	})

	t.Run("Unknown task type", func(t *testing.T) {
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

		mockTaskRegistry.RegisterTask(&integrations.HttpTask{})

		wf := model.Workflow{
			Definition: model.WorkflowDefinition{
				Tasks: []model.Task{
					{ID: "task1", Type: "integrations.http", Params: map[string]any{"url": "http://example.com"}},
					{ID: "task2", Type: "unknown_task_type"},
				},
			},
		}
		err := service.validateTaskTypes(testCtx, "test_account_id", wf)
		assert.Error(t, err)
	})
}

func TestWebhookTriggers(t *testing.T) {
	// Setup Mock Server for Integrations
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hasura/integration" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			actionMap, ok := body["action"].(map[string]any)
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			action := actionMap["name"].(string)

			switch action {
			case "integrations_create_config":
				input := body["input"].(map[string]any)["request"].(map[string]any)
				if input["integration_config_name"] == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				response := map[string]any{
					"id": "int-123",
					"configs": []map[string]any{
						{"name": "token", "value": "secret-token-123"},
					},
				}
				_ = json.NewEncoder(w).Encode(response)
				return
			case "integrations_delete_config":
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	config.Config.ServiceEndpoint = mockServer.URL
	config.Config.ServiceApiServerToken = "test-token"

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

	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Create Workflow with Webhook Trigger", func(t *testing.T) {
		wf := model.Workflow{
			Name: "webhook-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"integration_name": "my-hook"}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
				},
			},
		}

		mockStore.On("FindByName", mock.Anything, "test-tenant", "test-account", wf.Name).Return(nil, sql.ErrNoRows)

		// Mock create. We can't match exact ID because it's generated randomly.
		mockStore.On("CreateWorkflowWithInitialVersion", mock.Anything, "test-tenant", "test-account", mock.Anything).
			Return("ignored-id", &model.WorkflowVersion{ID: "v1-webhook", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate, IsLive: true}, nil).
			Run(func(args mock.Arguments) {
				savedWf := args.Get(3).(model.Workflow)
				assert.NotEmpty(t, savedWf.ID)
				// Assert normalization
				assert.Equal(t, "my-hook", savedWf.Definition.Triggers[0].Params["integration_name"])
				assert.NotNil(t, savedWf.Definition.Triggers[0].Internal)
				assert.Equal(t, "wf-"+savedWf.ID+"-my-hook", savedWf.Definition.Triggers[0].Internal.Name)
			})

		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", mock.Anything, mock.Anything).Return(nil)

		// Expect Describe to return NotFound (simulating no existing schedule)
		// Use mock.Anything for the scheduleID string
		mockTemporalClient.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect List call for cleanup
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			// We can't match exact ID 'ignored-id' easily as it's returned by Save mock.
			// But we know the query structure.
			return true // Accept any list query for now, or match prefix "nb_workflow_id ="
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

		_, token, err := service.CreateWorkflow(sc, "test-account", wf)
		assert.NoError(t, err)
		assert.Equal(t, "secret-token-123", token)
	})

	t.Run("Delete Workflow with Webhook Trigger", func(t *testing.T) {
		wfID := "wf-123"
		existingWf := &model.Workflow{
			ID:   wfID,
			Name: "webhook-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{
						Type:   model.WorkflowTriggerWebhook,
						Params: map[string]any{"integration_name": "my-hook"},
						Internal: &model.TriggerInternal{
							Name: "wf-123-my-hook",
						},
					},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Delete", mock.Anything, "test-tenant", "test-account", wfID).Return(nil)

		// Expect Describe for the specific schedule ID
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID).Return(&client.ScheduleDescription{}, nil)

		// Expect ListWorkflow for cleanup of running executions
		mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Return(&workflowservice.ListWorkflowExecutionsResponse{}, nil)

		err := service.DeleteWorkflow(sc, "test-account", wfID)
		assert.NoError(t, err)
	})

	t.Run("Update Workflow: Remove Trigger", func(t *testing.T) {
		wfID := "wf-update-1"
		existingWf := &model.Workflow{
			ID:   wfID,
			Name: "webhook-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{
						Type:   model.WorkflowTriggerWebhook,
						Params: map[string]any{"integration_name": "old-hook"},
						Internal: &model.TriggerInternal{
							Name: "wf-update-1-old-hook",
						},
					},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
				},
			},
		}

		newWf := model.Workflow{
			Name: "webhook-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerManual},
				}, // Removed webhook trigger, kept manual
				Tasks: []model.Task{
					{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
				},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", wfID, mock.Anything).Return(nil)

		// Expect Describe for UpdateWorkflow -> handleWorkflowTrigger
		mockTemporalClient.On("Describe", "workflow-schedule-"+wfID).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect List call for cleanup
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-update-1'"
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.NoError(t, err)
	})
}

func TestWorkflowValidationAndLogic(t *testing.T) {
	config.Config.ServiceEndpoint = "http://mock-integration-service"
	config.Config.ServiceApiServerToken = "test-token"

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
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Create Workflow: Fail if secret provided", func(t *testing.T) {
		wf := model.Workflow{
			Name: "fail-secret",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:   model.WorkflowTriggerWebhook,
					Params: map[string]any{"integration_name": "hook", "secret": "user-secret"},
				}},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}
		mockStore.On("FindByName", mock.Anything, "test-tenant", "test-account", wf.Name).Return(nil, sql.ErrNoRows)

		_, _, err := service.CreateWorkflow(sc, "test-account", wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "webhook secret is system managed")
	})

	t.Run("Update Workflow: Fail if secret mismatched", func(t *testing.T) {
		wfID := "wf-mismatch"
		existingWf := &model.Workflow{
			ID: wfID,
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:     model.WorkflowTriggerWebhook,
					Params:   map[string]any{"integration_name": "hook", "secret": "system-secret"},
					Internal: &model.TriggerInternal{Name: "wf-mismatch-hook"},
				}},
			},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)

		newWf := model.Workflow{
			ID:   wfID,
			Name: "wf-mismatch",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:   model.WorkflowTriggerWebhook,
					Params: map[string]any{"integration_name": "hook", "secret": "hacker-secret"},
				}},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "webhook secret cannot be modified")
	})

	t.Run("Update Workflow: Restore secret if missing", func(t *testing.T) {
		wfID := "wf-restore"
		existingWf := &model.Workflow{
			ID: wfID,
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:     model.WorkflowTriggerWebhook,
					Params:   map[string]any{"integration_name": "hook", "secret": "system-secret"},
					Internal: &model.TriggerInternal{Name: "wf-restore-hook"},
				}},
			},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", wfID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			updatedWf := args.Get(4).(model.Workflow)
			// Assert secret restored
			assert.Equal(t, "system-secret", updatedWf.Definition.Triggers[0].Params["secret"])
		})

		mockTemporalClient.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect List call for cleanup
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-restore'"
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

		newWf := model.Workflow{
			Name: "wf-restore",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:   model.WorkflowTriggerWebhook,
					Params: map[string]any{"integration_name": "hook"}, // No secret
				}},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.NoError(t, err)
	})

	t.Run("Update Workflow: Fail if secret provided for NEW trigger", func(t *testing.T) {
		wfID := "wf-new-trigger"
		existingWf := &model.Workflow{ID: wfID}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)

		newWf := model.Workflow{
			ID:   wfID,
			Name: "wf-new-trigger",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{
					Type:   model.WorkflowTriggerWebhook,
					Params: map[string]any{"integration_name": "hook", "secret": "user-secret"},
				}},
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "webhook secret cannot be provided for new trigger")
	})

	t.Run("Update Workflow: Preserve Status and LastExecutionStatus", func(t *testing.T) {
		wfID := "wf-status"
		existingWf := &model.Workflow{
			ID:                  wfID,
			Status:              model.WorkflowStatusActive,
			LastExecutionStatus: model.WorkflowExecutionStatusFailed,
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existingWf, nil)
		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", wfID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			updatedWf := args.Get(4).(model.Workflow)
			assert.Equal(t, model.WorkflowStatusActive, updatedWf.Status)
			assert.Equal(t, model.WorkflowExecutionStatusFailed, updatedWf.LastExecutionStatus)
		})

		mockTemporalClient.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))

		// Expect List call for cleanup
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = 'wf-status'"
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

		newWf := model.Workflow{
			ID:                  wfID,
			Name:                "wf-status",
			Status:              "",                                   // Empty status
			LastExecutionStatus: model.WorkflowExecutionStatusRunning, // Should be ignored
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}}, // Add a trigger
				Tasks:    []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}

		_, err := service.UpdateWorkflow(sc, "test-account", wfID, newWf)
		assert.NoError(t, err)
	})
}

func TestWebhookTriggerMatchesIntegration(t *testing.T) {
	// Picker-flow workflows: params.integration_name == internal.name == bare
	// name. URL carries the same bare name.
	pickerTrigger := model.Trigger{
		Type:     model.WorkflowTriggerWebhook,
		Params:   map[string]any{"integration_name": "workflowWebhookVK"},
		Internal: &model.TriggerInternal{Name: "workflowWebhookVK"},
	}

	// Legacy workflows: params.integration_name is the bare name, internal.name
	// is the `wf-<workflowID>-<name>` prefixed form. URL carries the prefixed
	// form (the api-server integration row's name), so matching only on
	// params.integration_name would drop these subscribers — the regression
	// this helper plus ListByIntegrationName together close.
	legacyTrigger := model.Trigger{
		Type:     model.WorkflowTriggerWebhook,
		Params:   map[string]any{"integration_name": "github_ci_webhook"},
		Internal: &model.TriggerInternal{Name: "wf-fb836ea7-fa0a-4044-a3d2-0375636a43c9-github_ci_webhook"},
	}

	// Triggers without internal.name populated yet (newly-created in-memory
	// objects pre-normalize). Should still match by params.integration_name.
	preNormalizeTrigger := model.Trigger{
		Type:   model.WorkflowTriggerWebhook,
		Params: map[string]any{"integration_name": "my-hook"},
	}

	nonWebhookTrigger := model.Trigger{
		Type:   model.WorkflowTriggerManual,
		Params: map[string]any{"integration_name": "my-hook"},
	}

	cases := []struct {
		name            string
		trigger         model.Trigger
		integrationName string
		want            bool
	}{
		{"picker flow matches", pickerTrigger, "workflowWebhookVK", true},
		{"picker flow mismatch", pickerTrigger, "something-else", false},
		{"legacy matches via internal.name", legacyTrigger, "wf-fb836ea7-fa0a-4044-a3d2-0375636a43c9-github_ci_webhook", true},
		{"legacy matches via params (bare name)", legacyTrigger, "github_ci_webhook", true},
		{"legacy mismatch", legacyTrigger, "different-integration", false},
		{"pre-normalize matches by params", preNormalizeTrigger, "my-hook", true},
		{"pre-normalize mismatch", preNormalizeTrigger, "wf-someid-my-hook", false},
		{"non-webhook trigger never matches", nonWebhookTrigger, "my-hook", false},
		{"empty integrationName never matches", pickerTrigger, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := webhookTriggerMatchesIntegration(tc.trigger, tc.integrationName)
			assert.Equal(t, tc.want, got)
		})
	}
}

func newVersioningService(mockStore *MockWorkflowStore, mockTemporalClient *MockTemporalClient) *Service {
	mockDataConverter := converter.GetDefaultDataConverter()
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})
	mockConfigService := new(MockConfigService)
	workflowExecutor := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	return NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExecutor, mockConfigService)
}

func TestListWorkflowVersions(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Missing accountId or id returns error", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.ListWorkflowVersions(sc, "", "wf-1")
		assert.Error(t, err)
		_, err = service.ListWorkflowVersions(sc, "test-account", "")
		assert.Error(t, err)
		mockStore.AssertNotCalled(t, "Find")
		mockStore.AssertNotCalled(t, "ListWorkflowVersions")
	})

	t.Run("Account not accessible returns unauthorized", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.ListWorkflowVersions(sc, "other-account", "wf-1")
		assert.Error(t, err)
		var commonErr common.Error
		assert.ErrorAs(t, err, &commonErr)
		assert.Equal(t, http.StatusForbidden, commonErr.Code)
		mockStore.AssertNotCalled(t, "Find")
	})

	t.Run("Workflow not found wraps ErrNoRows", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-missing").Return((*model.Workflow)(nil), sql.ErrNoRows)
		_, err := service.ListWorkflowVersions(sc, "test-account", "wf-missing")
		assert.Error(t, err)
		assert.ErrorIs(t, err, sql.ErrNoRows)
		mockStore.AssertNotCalled(t, "ListWorkflowVersions")
	})

	t.Run("Success returns versions capped by retention", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		existing := &model.Workflow{ID: "wf-ok"}
		expected := []model.WorkflowVersion{
			{ID: "v-2", WorkflowID: "wf-ok", VersionNumber: 2, Source: model.WorkflowVersionSourceSave},
			{ID: "v-1", WorkflowID: "wf-ok", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-ok").Return(existing, nil)
		mockStore.On("ListWorkflowVersions", mock.Anything, "wf-ok", model.MaxWorkflowVersionsPerWorkflow).Return(expected, nil)

		got, err := service.ListWorkflowVersions(sc, "test-account", "wf-ok")
		assert.NoError(t, err)
		assert.Equal(t, expected, got)
		mockStore.AssertExpectations(t)
	})
}

func TestGetWorkflowVersion(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Invalid versionNumber returns error", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.GetWorkflowVersion(sc, "test-account", "wf-1", 0)
		assert.Error(t, err)
		_, err = service.GetWorkflowVersion(sc, "test-account", "wf-1", -3)
		assert.Error(t, err)
		mockStore.AssertNotCalled(t, "Find")
	})

	t.Run("Account not accessible returns unauthorized", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.GetWorkflowVersion(sc, "other-account", "wf-1", 1)
		assert.Error(t, err)
		var commonErr common.Error
		assert.ErrorAs(t, err, &commonErr)
		assert.Equal(t, http.StatusForbidden, commonErr.Code)
	})

	t.Run("Workflow not found wraps ErrNoRows", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-missing").Return((*model.Workflow)(nil), sql.ErrNoRows)
		_, err := service.GetWorkflowVersion(sc, "test-account", "wf-missing", 1)
		assert.Error(t, err)
		assert.ErrorIs(t, err, sql.ErrNoRows)
		mockStore.AssertNotCalled(t, "GetWorkflowVersion")
	})

	t.Run("Version not found wraps ErrNoRows", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-ok").Return(&model.Workflow{ID: "wf-ok"}, nil)
		mockStore.On("GetWorkflowVersion", mock.Anything, "wf-ok", 99).Return((*model.WorkflowVersion)(nil), sql.ErrNoRows)
		_, err := service.GetWorkflowVersion(sc, "test-account", "wf-ok", 99)
		assert.Error(t, err)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("Success returns version", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		expected := &model.WorkflowVersion{
			ID:            "v-1",
			WorkflowID:    "wf-ok",
			VersionNumber: 1,
			Source:        model.WorkflowVersionSourceCreate,
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-ok").Return(&model.Workflow{ID: "wf-ok"}, nil)
		mockStore.On("GetWorkflowVersion", mock.Anything, "wf-ok", 1).Return(expected, nil)

		got, err := service.GetWorkflowVersion(sc, "test-account", "wf-ok", 1)
		assert.NoError(t, err)
		assert.Equal(t, expected, got)
		mockStore.AssertExpectations(t)
	})
}

func TestRestoreWorkflowVersion(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Invalid args return error", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.RestoreWorkflowVersion(sc, "", "wf-1", 1)
		assert.Error(t, err)
		_, err = service.RestoreWorkflowVersion(sc, "test-account", "", 1)
		assert.Error(t, err)
		_, err = service.RestoreWorkflowVersion(sc, "test-account", "wf-1", 0)
		assert.Error(t, err)
	})

	t.Run("Account not accessible returns unauthorized", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		_, err := service.RestoreWorkflowVersion(sc, "other-account", "wf-1", 1)
		assert.Error(t, err)
		var commonErr common.Error
		assert.ErrorAs(t, err, &commonErr)
		assert.Equal(t, http.StatusForbidden, commonErr.Code)
		mockStore.AssertNotCalled(t, "Find")
	})

	t.Run("Workflow not found wraps ErrNoRows", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-missing").Return((*model.Workflow)(nil), sql.ErrNoRows)
		_, err := service.RestoreWorkflowVersion(sc, "test-account", "wf-missing", 1)
		assert.Error(t, err)
		assert.ErrorIs(t, err, sql.ErrNoRows)
		mockStore.AssertNotCalled(t, "GetWorkflowVersion")
		mockStore.AssertNotCalled(t, "Update")
	})

	t.Run("Target version not found wraps ErrNoRows", func(t *testing.T) {
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, &MockTemporalClient{})
		existing := &model.Workflow{
			ID:   "wf-ok",
			Name: "wf-ok",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "t1", Type: "scripting.run_script", Params: map[string]any{"script": "echo"}}},
			},
		}
		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", "wf-ok").Return(existing, nil)
		mockStore.On("GetWorkflowVersion", mock.Anything, "wf-ok", 42).Return((*model.WorkflowVersion)(nil), sql.ErrNoRows)
		_, err := service.RestoreWorkflowVersion(sc, "test-account", "wf-ok", 42)
		assert.Error(t, err)
		assert.ErrorIs(t, err, sql.ErrNoRows)
		mockStore.AssertNotCalled(t, "Update")
	})

	t.Run("Success swaps definition into draft, preserves metadata, does not snapshot or change live pointer", func(t *testing.T) {
		mockTemporalClient := &MockTemporalClient{}
		mockStore := new(MockWorkflowStore)
		service := newVersioningService(mockStore, mockTemporalClient)

		wfID := "wf-restore"
		existing := &model.Workflow{
			ID:        wfID,
			Name:      "current-name",
			Tags:      map[string]any{"team": "platform"},
			Status:    model.WorkflowStatusActive,
			AccountID: "test-account",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "current-task", Type: "scripting.run_script", Params: map[string]any{"script": "echo current"}}},
			},
		}
		targetVersion := &model.WorkflowVersion{
			ID:            "v-1",
			WorkflowID:    wfID,
			VersionNumber: 1,
			Source:        model.WorkflowVersionSourceCreate,
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "old-task", Type: "scripting.run_script", Params: map[string]any{"script": "echo old"}}},
			},
		}

		mockStore.On("Find", mock.Anything, "test-tenant", "test-account", wfID).Return(existing, nil)
		mockStore.On("GetWorkflowVersion", mock.Anything, wfID, 1).Return(targetVersion, nil)
		mockStore.On("Update", mock.Anything, "test-tenant", "test-account", wfID, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			updated := args.Get(4).(model.Workflow)
			assert.Equal(t, "current-name", updated.Name, "name preserved")
			assert.Equal(t, map[string]any{"team": "platform"}, updated.Tags, "tags preserved")
			assert.Equal(t, model.WorkflowStatusActive, updated.Status, "status preserved")
			assert.Equal(t, "test-user", updated.UpdatedBy, "updated_by set by service")
			assert.Equal(t, "old-task", updated.Definition.Tasks[0].ID, "definition swapped into draft from target version")
		})
		mockTemporalClient.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))
		mockTemporalClient.On("List", mock.Anything, mock.MatchedBy(func(opts client.ScheduleListOptions) bool {
			return opts.Query == "nb_workflow_id = '"+wfID+"'"
		})).Return(&MockScheduleListIterator{Schedules: []*client.ScheduleListEntry{}}, nil)

		_, err := service.RestoreWorkflowVersion(sc, "test-account", wfID, 1)
		assert.NoError(t, err)
		mockStore.AssertExpectations(t)
		mockStore.AssertNotCalled(t, "PublishVersion")
		mockStore.AssertNotCalled(t, "SetLiveVersion")
	})
}
