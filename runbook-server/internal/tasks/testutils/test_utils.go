package testutils

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

// MockTaskContext is a mock implementation of TaskContext for testing purposes.
type MockTaskContext struct {
	Ctx           context.Context
	Logger        log.Logger
	Tenant        string
	Account       string
	WfID          string
	TskID         string
	User          string
	WfName        string
	DataConv      converter.DataConverter
	TempClient    client.Client
	WfStore       model.WorkflowStore
	WorkflowRunId string
	DryRun        bool
}

func (m *MockTaskContext) GetContext() context.Context               { return m.Ctx }
func (m *MockTaskContext) GetLogger() log.Logger                     { return m.Logger }
func (m *MockTaskContext) GetTenantID() string                       { return m.Tenant }
func (m *MockTaskContext) GetAccountID() string                      { return m.Account }
func (m *MockTaskContext) GetWorkflowID() string                     { return m.WfID }
func (m *MockTaskContext) GetTaskID() string                         { return m.TskID }
func (m *MockTaskContext) GetUserID() string                         { return m.User }
func (m *MockTaskContext) GetWorkflowName() string                   { return m.WfName }
func (m *MockTaskContext) GetUserDisplayName() string                { return "" }
func (m *MockTaskContext) GetDataConverter() converter.DataConverter { return m.DataConv }
func (m *MockTaskContext) GetTemporalClient() client.Client          { return m.TempClient }
func (m *MockTaskContext) GetStore() model.WorkflowStore             { return m.WfStore }
func (m *MockTaskContext) GetNewRequestContext() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(m.Tenant, m.User, []string{m.Account})
}
func (m *MockTaskContext) GetNewRequestContextForAccount(account string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(m.Tenant, m.User, []string{account})
}
func (m *MockTaskContext) GetWorkflowRunID() string {
	return m.WorkflowRunId
}
func (m *MockTaskContext) IsDryRun() bool {
	return m.DryRun
}

// NewTestTaskContext creates a mock TaskContext for tests.
func NewTestTaskContext(tenantID, accountID, userID string, logger log.Logger) types.TaskContext {
	return &MockTaskContext{
		Ctx:           context.TODO(),
		Logger:        logger,
		Tenant:        tenantID,
		Account:       accountID,
		WfID:          uuid.NewString(),
		TskID:         uuid.NewString(),
		User:          userID,
		WfName:        "trigger-task",
		DataConv:      converter.GetDefaultDataConverter(), // Fixed typo here
		TempClient:    nil,                                 // Mock as needed
		WfStore:       &MockWorkflowStore{},                // Mock a WorkflowStore
		WorkflowRunId: uuid.NewString(),
		DryRun:        false,
	}
}

func NewTestTaskContextWithContext(ctx context.Context, tenantID, accountID, userID string, logger log.Logger) types.TaskContext {
	return &MockTaskContext{
		Ctx:           ctx,
		Logger:        logger,
		Tenant:        tenantID,
		Account:       accountID,
		WfID:          uuid.NewString(),
		TskID:         uuid.NewString(),
		User:          userID,
		WfName:        "trigger-task",
		DataConv:      converter.GetDefaultDataConverter(), // Fixed typo here
		TempClient:    nil,                                 // Mock as needed
		WfStore:       &MockWorkflowStore{},                // Mock a WorkflowStore
		WorkflowRunId: uuid.NewString(),
		DryRun:        false,
	}
}

// MockWorkflowStore is a mock implementation of model.WorkflowStore for testing.
type MockWorkflowStore struct{}

func (m *MockWorkflowStore) CreateWorkflowWithInitialVersion(ctx context.Context, tenantID, accountID string, wf model.Workflow) (string, *model.WorkflowVersion, error) {
	return "mock-workflow-id", &model.WorkflowVersion{ID: "mock-version-id", WorkflowID: "mock-workflow-id", VersionNumber: 1, Source: model.WorkflowVersionSourceCreate, IsLive: true}, nil
}
func (m *MockWorkflowStore) List(ctx context.Context, tenantID, accountID string, request model.ListWorkflowRequest) ([]model.Workflow, int, error) {
	return nil, 0, nil
}
func (m *MockWorkflowStore) Find(ctx context.Context, tenantID, accountID, id string) (*model.Workflow, error) {
	return nil, nil
}
func (m *MockWorkflowStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	// Simulate finding a workflow by name
	if name == "test-child-workflow" {
		return &model.Workflow{
			ID:        "test-child-workflow-id",
			Name:      "test-child-workflow",
			TenantID:  tenantID,
			AccountID: accountID,
			Definition: model.WorkflowDefinition{
				Inputs: []model.Input{
					{ID: "message", Type: "string"},
				},
				Tasks: []model.Task{
					{ID: "echo_task", Type: "scripting.run_script", Params: map[string]any{"script": "echo {{ Inputs.message }}"}},
				},
				Output: map[string]any{"result": "{{ Tasks.echo_task.output.data }}"},
			},
		}, nil
	}
	return nil, fmt.Errorf("workflow '%s' not found", name)
}
func (m *MockWorkflowStore) FindByIntegrationName(ctx context.Context, tenantID, accountID, integrationName string) (*model.Workflow, error) {
	return nil, nil
}
func (m *MockWorkflowStore) ListByIntegrationName(ctx context.Context, tenantID, integrationName string) ([]model.Workflow, error) {
	return nil, nil
}
func (m *MockWorkflowStore) ListCallers(ctx context.Context, tenantID, accountID, calleeName string) ([]model.WorkflowCaller, error) {
	return nil, nil
}
func (m *MockWorkflowStore) Update(ctx context.Context, tenantID, accountID, id string, wf model.Workflow) error {
	return nil
}
func (m *MockWorkflowStore) Delete(ctx context.Context, tenantID, accountID, id string) error {
	return nil
}
func (m *MockWorkflowStore) UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowStatus) error {
	return nil
}
func (m *MockWorkflowStore) GetState(ctx context.Context, workflowID string) ([]model.WorkflowStateItem, error) {
	return nil, nil
}
func (m *MockWorkflowStore) SetState(ctx context.Context, workflowID string, updates []model.WorkflowStateUpdate) error {
	return nil
}
func (m *MockWorkflowStore) DeleteExpiredState(ctx context.Context, limit int) (int64, error) {
	return 0, nil
}
func (m *MockWorkflowStore) SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error {
	return nil
}
func (m *MockWorkflowStore) CountWorkflows(ctx context.Context, tenantID, accountID string, status model.WorkflowStatus, triggerType string) (int64, error) {
	return 0, nil
}
func (m *MockWorkflowStore) GetWorkflowNames(ctx context.Context, tenantID, accountID string, ids []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (m *MockWorkflowStore) ListWorkflowVersions(ctx context.Context, workflowID string, limit int) ([]model.WorkflowVersion, error) {
	return nil, nil
}
func (m *MockWorkflowStore) GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*model.WorkflowVersion, error) {
	return nil, nil
}
func (m *MockWorkflowStore) GetWorkflowVersionByID(ctx context.Context, versionID string) (*model.WorkflowVersion, error) {
	return nil, nil
}
func (m *MockWorkflowStore) GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*model.WorkflowVersion, error) {
	return nil, nil
}
func (m *MockWorkflowStore) PublishVersion(ctx context.Context, workflowID, createdBy string, source model.WorkflowVersionSource, name, description *string, restoredFromVersion *int, status model.WorkflowStatus) (*model.WorkflowVersion, error) {
	return &model.WorkflowVersion{ID: "mock-version-id", WorkflowID: workflowID, VersionNumber: 1, Source: source, Status: status}, nil
}
func (m *MockWorkflowStore) SetLiveVersion(ctx context.Context, tenantID, accountID, workflowID, versionID string) error {
	return nil
}
func (m *MockWorkflowStore) SetDraftVersionID(ctx context.Context, tenantID, accountID, workflowID, versionID string) error {
	return nil
}
func (m *MockWorkflowStore) UpdateVersionMetadata(ctx context.Context, workflowID string, versionNumber int, name, description *string) (*model.WorkflowVersion, error) {
	return nil, nil
}
func (m *MockWorkflowStore) UpdateVersionStatus(ctx context.Context, tenantID, accountID, workflowID, versionID string, status model.WorkflowStatus) (bool, error) {
	return false, nil
}
