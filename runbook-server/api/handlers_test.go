package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/config"
	"nudgebee/runbook/services/optimizer"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/testsuite"
)

// MockWorkflowService is a mock of workflow.Service
type MockWorkflowService struct {
	mock.Mock
}

func (m *MockWorkflowService) CreateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) (string, string, error) {
	args := m.Called(ctx, accountId, wf)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockWorkflowService) ListWorkflows(ctx *security.RequestContext, accountId string, request model.ListWorkflowRequest) (model.ListWorkflowResponse, error) {
	args := m.Called(ctx, accountId, request)
	return args.Get(0).(model.ListWorkflowResponse), args.Error(1)
}

func (m *MockWorkflowService) GetWorkflow(ctx *security.RequestContext, accountId, id string) (*model.Workflow, error) {
	args := m.Called(ctx, accountId, id)
	return args.Get(0).(*model.Workflow), args.Error(1)
}

func (m *MockWorkflowService) GetWorkflowState(ctx *security.RequestContext, accountId, id string) ([]model.WorkflowStateItem, error) {
	args := m.Called(ctx, accountId, id)
	return args.Get(0).([]model.WorkflowStateItem), args.Error(1)
}

func (m *MockWorkflowService) UpdateWorkflow(ctx *security.RequestContext, accountId, id string, wf model.Workflow) (model.Workflow, error) {
	args := m.Called(ctx, accountId, id, wf)
	return args.Get(0).(model.Workflow), args.Error(1)
}

func (m *MockWorkflowService) ExecuteWorkflow(ctx *security.RequestContext, accountId, id string, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error) {
	args := m.Called(ctx, accountId, id, triggerType, inputs)
	return args.String(0), args.Error(1)
}

func (m *MockWorkflowService) RetriggerWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) (string, error) {
	args := m.Called(ctx, accountId, workflowId, executionId, inputs)
	return args.String(0), args.Error(1)
}

func (m *MockWorkflowService) ListWorkflowExecutions(ctx *security.RequestContext, accountId, workflowId string, request model.ListWorkflowExecutionRequest) (model.ListWorkflowExecutionResponse, error) {
	args := m.Called(ctx, accountId, workflowId, request)
	return args.Get(0).(model.ListWorkflowExecutionResponse), args.Error(1)
}

func (m *MockWorkflowService) GetWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	args := m.Called(ctx, accountId, workflowId, executionId)
	return args.Get(0).(*workflowservice.DescribeWorkflowExecutionResponse), args.Error(1)
}

func (m *MockWorkflowService) GetDetailedWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) (*model.WorkflowExecutionDetails, error) {
	args := m.Called(ctx, accountId, workflowId, executionId)
	return args.Get(0).(*model.WorkflowExecutionDetails), args.Error(1)
}

func (m *MockWorkflowService) UpdateWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string, inputs map[string]any) error {
	args := m.Called(ctx, accountId, workflowId, executionId, inputs)
	return args.Error(0)
}

func (m *MockWorkflowService) UpdateWorkflowStatus(ctx *security.RequestContext, accountId, id string, status model.WorkflowStatus) error {
	args := m.Called(ctx, accountId, id, status)
	return args.Error(0)
}

func (m *MockWorkflowService) DeleteWorkflow(ctx *security.RequestContext, accountId, id string) error {
	args := m.Called(ctx, accountId, id)
	return args.Error(0)
}

func (m *MockWorkflowService) CompleteApprovalTask(ctx *security.RequestContext, token, status string, result any) error {
	args := m.Called(ctx, token, status, result)
	return args.Error(0)
}

func (m *MockWorkflowService) CancelWorkflowExecution(ctx *security.RequestContext, accountId, workflowId, executionId string) error {
	args := m.Called(ctx, accountId, workflowId, executionId)
	return args.Error(0)
}

func (m *MockWorkflowService) PauseWorkflow(ctx *security.RequestContext, accountId, id string) error {
	args := m.Called(ctx, accountId, id)
	return args.Error(0)
}

func (m *MockWorkflowService) ResumeWorkflow(ctx *security.RequestContext, accountId, id string) error {
	args := m.Called(ctx, accountId, id)
	return args.Error(0)
}

func (m *MockWorkflowService) ListAllTasks(ctx *security.RequestContext) model.ListTaskDefinitionResponse {
	args := m.Called()
	return args.Get(0).(model.ListTaskDefinitionResponse)
}

func (m *MockWorkflowService) ExecuteTask(ctx *security.RequestContext, accountId, taskType string, params map[string]any) (any, error) {
	args := m.Called(ctx, accountId, taskType, params)
	return args.Get(0), args.Error(1)
}

func (m *MockWorkflowService) ListMCPTools(ctx *security.RequestContext, accountId string, params map[string]any) (any, error) {
	args := m.Called(ctx, accountId, params)
	return args.Get(0), args.Error(1)
}

func (m *MockWorkflowService) ValidateWorkflow(ctx *security.RequestContext, accountId string, wf model.Workflow) error {
	args := m.Called(ctx, accountId, wf)
	return args.Error(0)
}

func (m *MockWorkflowService) DryRunWorkflow(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (model.DryRunWorkflowResponse, error) {
	args := m.Called(ctx, accountId, request)
	return args.Get(0).(model.DryRunWorkflowResponse), args.Error(1)
}

func (m *MockWorkflowService) DryRunWorkflowAsync(ctx *security.RequestContext, accountId string, request model.DryRunWorkflowRequest) (string, string, error) {
	args := m.Called(ctx, accountId, request)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockWorkflowService) CountWorkflowExecutions(ctx *security.RequestContext, req model.WorkflowExecutionCountRequest) (model.WorkflowExecutionCountResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(model.WorkflowExecutionCountResponse), args.Error(1)
}

func (m *MockWorkflowService) CountWorkflows(ctx *security.RequestContext, req model.WorkflowCountRequest) (model.WorkflowCountResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(model.WorkflowCountResponse), args.Error(1)
}

func (m *MockWorkflowService) ListTemplates(ctx *security.RequestContext, request model.ListWorkflowTemplateRequest) (model.ListWorkflowTemplateResponse, error) {
	args := m.Called(ctx, request)
	return args.Get(0).(model.ListWorkflowTemplateResponse), args.Error(1)
}

func (m *MockWorkflowService) GetTemplate(ctx *security.RequestContext, id string) (*model.WorkflowTemplate, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.WorkflowTemplate), args.Error(1)
}

// MockConfigService is a mock of config.ConfigService interface
type MockConfigService struct {
	mock.Mock
}

func (m *MockConfigService) SaveConfig(ctx context.Context, cfg model.Config) (string, error) {
	args := m.Called(ctx, cfg)
	return args.String(0), args.Error(1)
}

func (m *MockConfigService) GetConfig(ctx context.Context, tenantID string, accountID *string, key string, decrypt bool) (*model.Config, error) {
	args := m.Called(ctx, tenantID, accountID, key, decrypt)
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

// Ensure MockConfigService implements config.ConfigService interface
var _ config.ConfigService = (*MockConfigService)(nil)

// MockSecurityContextBuilder is a mock implementation of SecurityContextBuilder.
type MockSecurityContextBuilder struct {
	mock.Mock
}

// BuildContextFromRequestPayload implements SecurityContextBuilder.
func (m *MockSecurityContextBuilder) BuildContextFromRequestPayload(ctx context.Context, c *gin.Context, request map[string]string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	args := m.Called(ctx, c, request, tracer, meter, logger)
	return args.Get(0).(*security.RequestContext), args.Error(1)
}

// BuildContextFromHasuraPayload implements SecurityContextBuilder.
func (m *MockSecurityContextBuilder) BuildContextFromHasuraPayload(ctx context.Context, c *gin.Context, h *HasuraActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	args := m.Called(ctx, c, h, tracer, meter, logger)
	return args.Get(0).(*security.RequestContext), args.Error(1)
}

type APITestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env                    *testsuite.TestWorkflowEnvironment
	server                 *Server
	workflowService        *MockWorkflowService // Use the mock service
	configService          config.ConfigService // Use the interface type
	optimizerService       *optimizer.MockOptimizerService
	securityContextBuilder *MockSecurityContextBuilder
}

func (s *APITestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	mockWorkflowService := new(MockWorkflowService)
	s.workflowService = mockWorkflowService // Assign the mock service
	mockConfigService := new(MockConfigService)
	s.configService = mockConfigService
	mockOptimizerService := new(optimizer.MockOptimizerService)
	s.optimizerService = mockOptimizerService
	mockSecurityBuilder := new(MockSecurityContextBuilder)
	s.securityContextBuilder = mockSecurityBuilder
	s.server = NewServer(mockWorkflowService, mockConfigService) // Pass the mock services to NewServer
	s.server.SetOptimizerService(s.optimizerService)             // Inject the mock optimizer service
	s.server.securityContextBuilder = s.securityContextBuilder   // Inject the mock security builder
	s.server.SetWorkflowService(s.workflowService)               // Set the workflow service to ensure routes are set up
	gin.SetMode(gin.TestMode)

	// Default mock for security context builder
	dummySecurityContext := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"}), // Provide accountIds
		slog.Default(),
		nil, // tracer
		nil, // meter
	)
	s.securityContextBuilder.On("BuildContextFromRequestPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(dummySecurityContext, nil).Maybe()
	s.securityContextBuilder.On("BuildContextFromHasuraPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(dummySecurityContext, nil).Maybe()
}

func (s *APITestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	s.workflowService.AssertExpectations(s.T()) // Assert expectations on the mock service
	s.configService.(*MockConfigService).AssertExpectations(s.T())
	s.securityContextBuilder.AssertExpectations(s.T())
}

func TestAPISuite(t *testing.T) {
	suite.Run(t, new(APITestSuite))
}

func (s *APITestSuite) TestGetWorkflowHistory() {
	mockService := s.workflowService
	mockService.On("ListWorkflowExecutions", mock.Anything, "test-account", "test", mock.AnythingOfType("model.ListWorkflowExecutionRequest")).Return(model.ListWorkflowExecutionResponse{}, nil)

	req, _ := http.NewRequest(http.MethodGet, "/workflows/test/runs", http.NoBody)
	req.Header.Set("X-Tenant-ID", "test-tenant")
	req.Header.Set("X-Account-ID", "test-account")
	w := httptest.NewRecorder()
	s.server.router.ServeHTTP(w, req)

	s.Equal(http.StatusOK, w.Code)
}

func (s *APITestSuite) TestListTasks() {
	mockService := s.workflowService
	expectedTasks := []model.TaskDefinition{
		{Name: "task1", InputSchema: map[string]any{"param1": "string"}},
		{Name: "task2", InputSchema: map[string]any{"param2": "int"}},
	}
	mockService.On("ListAllTasks", mock.Anything).Return(model.ListTaskDefinitionResponse{Tasks: expectedTasks})

	req, _ := http.NewRequest(http.MethodGet, "/tasks", http.NoBody)
	req.Header.Set("X-Tenant-ID", "test-tenant")
	req.Header.Set("X-Account-ID", "test-account")
	w := httptest.NewRecorder()
	s.server.router.ServeHTTP(w, req)

	s.Equal(http.StatusOK, w.Code)
	// Further assertions to check the response body can be added here
}

func (s *APITestSuite) TestValidateWorkflow() {
	mockService := s.workflowService

	// Test case for a valid workflow
	s.T().Run("Valid Workflow", func(t *testing.T) {
		validWorkflow := model.Workflow{
			Name: "valid-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}}},
			},
		}
		mockService.On("ValidateWorkflow", mock.Anything, "test-account", validWorkflow).Return(nil).Once()

		body, _ := json.Marshal(model.Workflow{
			Name: "valid-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}}},
			},
		})
		req, _ := http.NewRequest(http.MethodPost, "/workflows/validate", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Account-ID", "test-account")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		mockService.AssertExpectations(t)
	})

	// Test case for an invalid workflow
	s.T().Run("Invalid Workflow", func(t *testing.T) {
		invalidWorkflow := model.Workflow{
			Name: "invalid-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "unknown-task"}},
			},
		}
		mockService.On("ValidateWorkflow", mock.Anything, "test-account", invalidWorkflow).Return(fmt.Errorf("unknown task type")).Once()

		body, _ := json.Marshal(model.Workflow{
			Name: "invalid-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "unknown-task"}},
			},
		})
		req, _ := http.NewRequest(http.MethodPost, "/workflows/validate", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Account-ID", "test-account")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		mockService.AssertExpectations(t)
	})
}

func (s *APITestSuite) TestCreateWorkflow() {
	mockService := s.workflowService

	s.T().Run("Successful Creation", func(t *testing.T) {
		workflow := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}}},
			},
		}
		mockService.On("CreateWorkflow", mock.Anything, "test-account", mock.AnythingOfType("model.Workflow")).Return("new-workflow-id", "new-webhook-token", nil).Once()

		body, _ := json.Marshal(workflow)
		req, _ := http.NewRequest(http.MethodPost, "/workflows", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", "test-tenant")
		req.Header.Set("X-Account-ID", "test-account")
		req.Header.Set("X-User-ID", "test-user")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		// Add assertions for the token
		var response map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "new-workflow-id", response["id"])
		assert.Equal(t, "new-webhook-token", response["token"])

		mockService.AssertExpectations(t)
	})
}

func (s *APITestSuite) TestUpdateWorkflow() {
	mockService := s.workflowService

	s.T().Run("Successful Update", func(t *testing.T) {
		workflow := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task-1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello world'"}}},
			},
		}
		mockService.On("UpdateWorkflow", mock.Anything, "test-account", "existing-workflow-id", workflow).Return(workflow, nil).Once()

		body, _ := json.Marshal(workflow)
		req, _ := http.NewRequest(http.MethodPut, "/workflows/existing-workflow-id", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", "test-tenant")
		req.Header.Set("X-Account-ID", "test-account")
		req.Header.Set("X-User-ID", "test-user")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		mockService.AssertExpectations(t)
	})
}

func (s *APITestSuite) TestTriggerWorkflow() {
	mockService := s.workflowService

	s.T().Run("Successful Trigger", func(t *testing.T) {
		inputs := map[string]any{"param1": "value1"}
		mockService.On("ExecuteWorkflow", mock.Anything, "test-account", "existing-workflow-id", model.WorkflowTriggerManual, inputs).Return("new-run-id", nil).Once()

		body, _ := json.Marshal(inputs)
		req, _ := http.NewRequest(http.MethodPost, "/workflows/existing-workflow-id/trigger", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", "test-tenant")
		req.Header.Set("X-Account-ID", "test-account")
		req.Header.Set("X-User-ID", "test-user")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		mockService.AssertExpectations(t)
	})
}

func (s *APITestSuite) TestGetWorkflowExecution() {
	mockService := s.workflowService

	s.T().Run("common.Error maps to its Code", func(t *testing.T) {
		mockService.On("GetDetailedWorkflowExecution", mock.Anything, "test-account", "wf-id", "run-id").
			Return((*model.WorkflowExecutionDetails)(nil), common.ErrorNotFound("workflow execution not found")).Once()

		req, _ := http.NewRequest(http.MethodGet, "/workflows/wf-id/executions/run-id", http.NoBody)
		req.Header.Set("X-Tenant-ID", "test-tenant")
		req.Header.Set("X-Account-ID", "test-account")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		mockService.AssertExpectations(t)
	})

	s.T().Run("unknown error falls through to 500", func(t *testing.T) {
		mockService.On("GetDetailedWorkflowExecution", mock.Anything, "test-account", "wf-2", "run-2").
			Return((*model.WorkflowExecutionDetails)(nil), fmt.Errorf("temporal unreachable")).Once()

		req, _ := http.NewRequest(http.MethodGet, "/workflows/wf-2/executions/run-2", http.NoBody)
		req.Header.Set("X-Tenant-ID", "test-tenant")
		req.Header.Set("X-Account-ID", "test-account")
		w := httptest.NewRecorder()
		s.server.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		mockService.AssertExpectations(t)
	})
}

func (s *APITestSuite) TestExecuteTask() {
	mockService := s.workflowService
	taskType := "test-task"
	params := map[string]any{"input": "value"}
	expectedResult := map[string]any{"output": "result"}
	mockService.On("ExecuteTask", mock.Anything, "test-account", taskType, params).Return(expectedResult, nil)

	reqBody := `{"input": "value"}`
	req, _ := http.NewRequest(http.MethodPost, "/tasks/"+taskType+"/execute", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Account-ID", "test-account")
	w := httptest.NewRecorder()
	s.server.router.ServeHTTP(w, req)

	s.Equal(http.StatusOK, w.Code)
	// Further assertions to check the response body can be added here
}
