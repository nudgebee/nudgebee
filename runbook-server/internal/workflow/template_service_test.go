package workflow

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/tasks/scripting"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
)

// MockWorkflowTemplateStore is a mock of model.WorkflowTemplateStore
type MockWorkflowTemplateStore struct {
	mock.Mock
}

func (m *MockWorkflowTemplateStore) ListGlobal(ctx context.Context, request model.ListWorkflowTemplateRequest) ([]model.WorkflowTemplate, int, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]model.WorkflowTemplate), args.Int(1), args.Error(2)
}

func (m *MockWorkflowTemplateStore) FindGlobal(ctx context.Context, id string) (*model.WorkflowTemplate, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowTemplate), args.Error(1)
}

// Ensure MockWorkflowTemplateStore implements model.WorkflowTemplateStore
var _ model.WorkflowTemplateStore = (*MockWorkflowTemplateStore)(nil)

// newTestServiceWithTemplateStore creates a Service with both workflow and template store mocks.
func newTestServiceWithTemplateStore() (*Service, *MockWorkflowStore, *MockWorkflowTemplateStore, *MockTemporalClient) {
	config.Config.ServiceEndpoint = "http://mock-service"
	config.Config.ServiceApiServerToken = "test-token"

	mockTemporalClient := &MockTemporalClient{}
	mockDataConverter := converter.GetDefaultDataConverter()
	mockStore := new(MockWorkflowStore)
	mockTemplateStore := new(MockWorkflowTemplateStore)
	mockTaskRegistry := tasks.NewInitializedTaskRegistry()
	mockConfigService := new(MockConfigService)
	mockTaskRegistry.RegisterTask(&scripting.RunScriptTask{})

	workflowExecutor := &WorkflowExecutor{
		temporalClient: mockTemporalClient,
		workflowStore:  mockStore,
		dataConverter:  mockDataConverter,
	}

	service := NewService(mockTemporalClient, mockStore, mockDataConverter, mockTaskRegistry, workflowExecutor, mockConfigService, mockTemplateStore)
	return service, mockStore, mockTemplateStore, mockTemporalClient
}

func TestListTemplates(t *testing.T) {
	service, _, mockTemplateStore, _ := newTestServiceWithTemplateStore()
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Success with results", func(t *testing.T) {
		templates := []model.WorkflowTemplate{
			{ID: "t1", Name: "Template A", Category: "kubernetes"},
			{ID: "t2", Name: "Template B", Category: "monitoring"},
		}

		req := model.ListWorkflowTemplateRequest{Limit: 10}
		mockTemplateStore.On("ListGlobal", mock.Anything, req).Return(templates, 2, nil).Once()

		resp, err := service.ListTemplates(sc, req)
		assert.NoError(t, err)
		assert.Equal(t, 2, resp.TotalCount)
		assert.Len(t, resp.Templates, 2)
		mockTemplateStore.AssertExpectations(t)
	})

	t.Run("Empty results", func(t *testing.T) {
		req := model.ListWorkflowTemplateRequest{Category: "nonexistent"}
		mockTemplateStore.On("ListGlobal", mock.Anything, req).Return([]model.WorkflowTemplate{}, 0, nil).Once()

		resp, err := service.ListTemplates(sc, req)
		assert.NoError(t, err)
		assert.Equal(t, 0, resp.TotalCount)
		assert.Empty(t, resp.Templates)
		mockTemplateStore.AssertExpectations(t)
	})

	t.Run("Store error propagated", func(t *testing.T) {
		req := model.ListWorkflowTemplateRequest{}
		mockTemplateStore.On("ListGlobal", mock.Anything, req).Return(nil, 0, errors.New("db error")).Once()

		_, err := service.ListTemplates(sc, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list templates")
		mockTemplateStore.AssertExpectations(t)
	})
}

func TestGetTemplate(t *testing.T) {
	service, _, mockTemplateStore, _ := newTestServiceWithTemplateStore()
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	t.Run("Success", func(t *testing.T) {
		expected := &model.WorkflowTemplate{
			ID:       "template-123",
			Name:     "Pod Restart Template",
			Category: "kubernetes",
		}

		mockTemplateStore.On("FindGlobal", mock.Anything, "template-123").Return(expected, nil).Once()

		tmpl, err := service.GetTemplate(sc, "template-123")
		assert.NoError(t, err)
		assert.Equal(t, "Pod Restart Template", tmpl.Name)
		mockTemplateStore.AssertExpectations(t)
	})

	t.Run("Not found", func(t *testing.T) {
		mockTemplateStore.On("FindGlobal", mock.Anything, "nonexistent").Return(nil, sql.ErrNoRows).Once()

		tmpl, err := service.GetTemplate(sc, "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, tmpl)
		assert.Contains(t, err.Error(), "failed to get template")
		mockTemplateStore.AssertExpectations(t)
	})
}
