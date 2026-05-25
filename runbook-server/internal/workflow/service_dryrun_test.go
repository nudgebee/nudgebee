package workflow

import (
	"context"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/tasks/scripting"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// MockWorkflowRun is a mock implementation of client.WorkflowRun for testing purposes.
type MockWorkflowRun struct {
	client.WorkflowRun
	mock.Mock
}

func (m *MockWorkflowRun) Get(ctx context.Context, valuePtr interface{}) error {
	args := m.Called(ctx, valuePtr)
	if valuePtr != nil && args.Get(0) != nil {
		val := args.Get(0).(string)
		*(valuePtr.(*string)) = val
	}
	return args.Error(1)
}

func (m *MockWorkflowRun) GetID() string {
	return "test-workflow-id"
}

func (m *MockWorkflowRun) GetRunID() string {
	return "test-run-id"
}

func (m *MockTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	callArgs := make([]interface{}, 0)
	callArgs = append(callArgs, ctx, options, workflow)
	callArgs = append(callArgs, args...)
	mArgs := m.Called(callArgs...)
	if mArgs.Get(0) == nil {
		return nil, mArgs.Error(1)
	}
	return mArgs.Get(0).(client.WorkflowRun), mArgs.Error(1)
}

// MockConfigServiceDryRun is a mock implementation of configSvc.ConfigService for testing purposes.
type MockConfigServiceDryRun struct {
	mock.Mock
}

func (m *MockConfigServiceDryRun) SaveConfig(ctx context.Context, cfg model.Config) (string, error) {
	args := m.Called(ctx, cfg)
	return args.String(0), args.Error(1)
}

func (m *MockConfigServiceDryRun) GetConfig(ctx context.Context, tenantID string, accountID *string, key string, decrypt bool) (*model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, key, decrypt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Config), args.Error(1)
}

func (m *MockConfigServiceDryRun) ListConfigs(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, labels)
	return args.Get(0).([]model.Config), args.Error(1)
}

func (m *MockConfigServiceDryRun) ListConfigsDecrypted(ctx context.Context, tenantID string, accountID *string, labels map[string]string) ([]model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, labels)
	return args.Get(0).([]model.Config), args.Error(1)
}

func (m *MockConfigServiceDryRun) DeleteConfig(ctx context.Context, tenantID string, accountID *string, key string) error {
	args := m.Called(ctx, tenantID, accountID, key)
	return args.Error(0)
}

// Ensure MockConfigServiceDryRun implements configSvc.ConfigService interface
var _ configSvc.ConfigService = (*MockConfigServiceDryRun)(nil)

func TestDryRunWorkflow(t *testing.T) {
	mockTemporalClient := &MockTemporalClient{}
	mockDataConverter := converter.GetDefaultDataConverter()
	mockStore := new(MockWorkflowStore)
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})
	mockConfigService := new(MockConfigServiceDryRun)

	workflowExecutor := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}
	service := NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExecutor, mockConfigService)

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Successful Dry-Run", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{{ID: "name", Type: "string", Required: true}},
				Tasks:  []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo hello {{ Inputs.name }}"}}},
				Output: map[string]any{"res": "ok"},
			},
			Inputs: map[string]any{"name": "world"},
		}

		mockRun := &MockWorkflowRun{}
		expectedOutput := `{"res":"ok"}`
		mockRun.On("Get", mock.Anything, mock.Anything).Return(expectedOutput, nil)

		mockTemporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockRun, nil)

		// Mock History Iterator (Empty for now as we don't test history processing here deeply)
		mockIterator := new(MockHistoryIterator)
		mockIterator.On("HasNext").Return(false)
		mockTemporalClient.On("GetWorkflowHistory", mock.Anything, "test-workflow-id", "test-run-id", mock.Anything, mock.Anything).Return(mockIterator)

		mockQueryValue := new(MockValue)
		mockQueryValue.On("Get", mock.Anything).Run(func(args mock.Arguments) {
			if ptr, ok := args.Get(0).(*[]model.TaskExecutionDetails); ok {
				*ptr = []model.TaskExecutionDetails{
					{ID: "task1", Type: "scripting.run_script", Status: model.TaskStatusCompleted, Output: map[string]any{"dry_run": "simulated_success"}},
				}
			}
		}).Return(nil)
		mockTemporalClient.On("QueryWorkflow", mock.Anything, "test-workflow-id", "test-run-id", "getDryRunTrace").Return(mockQueryValue, nil)

		resp, err := service.DryRunWorkflow(sc, "test-account", req)

		assert.NoError(t, err)
		assert.Equal(t, model.WorkflowExecutionStatusCompleted, resp.Status)
		assert.Equal(t, map[string]any{"res": "ok"}, resp.Output)
		assert.NotEmpty(t, resp.Tasks) // Verify tasks are populated from query
		assert.Equal(t, "task1", resp.Tasks[0].ID)
		mockTemporalClient.AssertExpectations(t)
	})

	t.Run("Dry-Run with Validation Error (Inputs)", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{{ID: "name", Type: "string", Required: true}},
			},
			Inputs: map[string]any{}, // Missing required input
		}

		_, err := service.DryRunWorkflow(sc, "test-account", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required input 'name' is missing")
	})

	t.Run("Dry-Run with Validation Error (Hook Empty Type)", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Tasks: []model.Task{
					{
						ID:   "task1",
						Type: "scripting.run_script",
						Hooks: &model.Hooks{
							Success: []model.Action{{Type: ""}},
						},
					},
				},
			},
			Inputs: map[string]any{},
		}

		_, err := service.DryRunWorkflow(sc, "test-account", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "hook 'success[0]' for 'task1' has an empty type")
	})

	t.Run("Dry-Run with Validation Error (Workflow Hook Empty Type)", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script"}},
				Hooks: &model.Hooks{
					Always: []model.Action{{Type: ""}},
				},
			},
			Inputs: map[string]any{},
		}

		_, err := service.DryRunWorkflow(sc, "test-account", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "hook 'always[0]' for 'workflow' has an empty type")
	})

	t.Run("Dry-Run Default Timeout", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Tasks: []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo hello"}}},
			},
			Inputs: map[string]any{},
		}

		mockRun := &MockWorkflowRun{}
		mockRun.On("Get", mock.Anything, mock.Anything).Return(`{}`, nil)

		// Verification of 30m timeout
		mockTemporalClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(options client.StartWorkflowOptions) bool {
			return options.WorkflowExecutionTimeout == 30*time.Minute
		}), mock.Anything, mock.Anything, mock.Anything).Return(mockRun, nil)

		mockIterator := new(MockHistoryIterator)
		mockIterator.On("HasNext").Return(false)
		mockTemporalClient.On("GetWorkflowHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockIterator)

		mockQueryValue := new(MockValue)
		mockQueryValue.On("Get", mock.Anything).Return(nil)
		mockTemporalClient.On("QueryWorkflow", mock.Anything, "test-workflow-id", "test-run-id", "getDryRunTrace").Return(mockQueryValue, nil)

		_, err := service.DryRunWorkflow(sc, "test-account", req)
		assert.NoError(t, err)
		mockTemporalClient.AssertExpectations(t)
	})

	t.Run("Dry-Run User-Specified Timeout", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Tasks:   []model.Task{{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo hello"}}},
				Timeout: "1h",
			},
			Inputs: map[string]any{},
		}

		mockRun := &MockWorkflowRun{}
		mockRun.On("Get", mock.Anything, mock.Anything).Return(`{}`, nil)

		// Verification of 1h timeout
		mockTemporalClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(options client.StartWorkflowOptions) bool {
			return options.WorkflowExecutionTimeout == 1*time.Hour
		}), mock.Anything, mock.Anything, mock.Anything).Return(mockRun, nil)

		mockIterator := new(MockHistoryIterator)
		mockIterator.On("HasNext").Return(false)
		mockTemporalClient.On("GetWorkflowHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockIterator)

		mockQueryValue := new(MockValue)
		mockQueryValue.On("Get", mock.Anything).Return(nil)
		mockTemporalClient.On("QueryWorkflow", mock.Anything, "test-workflow-id", "test-run-id", "getDryRunTrace").Return(mockQueryValue, nil)

		_, err := service.DryRunWorkflow(sc, "test-account", req)
		assert.NoError(t, err)
		mockTemporalClient.AssertExpectations(t)
	})
}
