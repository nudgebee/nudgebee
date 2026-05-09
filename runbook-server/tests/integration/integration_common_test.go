package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	net_url "net/url"
	"os"
	"testing"
	"time"

	"net/http/httptest"
	"nudgebee/runbook/api"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/events"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
	"nudgebee/runbook/internal/system"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/workflow"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/optimizer"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

func startMockAuthService(t *testing.T) *httptest.Server {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/authz/get_security_context", func(w http.ResponseWriter, r *http.Request) {
		securityContext := security.NewSecurityContextForTenantAccountAdmin(testTenantID, testUserID, []string{testAccountID})
		response := gin.H{
			"context": securityContext,
		}
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			t.Fatalf("Failed to encode mock auth response: %v", err)
		}
	})

	// Mock endpoint for PR creation (Hasura GitOps)
	handler.HandleFunc("/hasura/gitops", func(w http.ResponseWriter, r *http.Request) {
		// Verify expected headers if needed
		if r.Header.Get("X-ACTION-TOKEN") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Decode request to ensure it's valid
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return mock success response
		response := map[string]any{
			"data": map[string]any{
				"pull_request_url": "https://github.com/nudgebee/test-repo/pull/1",
				"id":               "pr-123",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("Failed to encode mock gitops response: %v", err)
		}
	})

	return httptest.NewServer(handler)
}

var (
	apiBaseURL       = "http://localhost:8002"
	temporalGRPCAddr = "localhost:7233"
	testTenantID     = os.Getenv("TEST_TENANT_ID")
	testAccountID    = os.Getenv("TEST_ACCOUNT_ID")
	testUserID       = os.Getenv("TEST_USER_ID")
)

type IntegrationTestSuite struct {
	suite.Suite
	temporalClient   client.Client
	mockAuthServer   *httptest.Server
	apiServer        *httptest.Server
	eventRegistry    *events.EventRegistry
	eventConsumer    *events.Consumer
	testWorkflowDao  *storage.WorkflowDao
	testOptimizerDao *storage.OptimizerDao
	configService    configSvc.ConfigService
	optimizerService optimizer.Service
	testLogger       *slog.Logger
	workflowService  *workflow.Service
	workflowExecutor *workflow.WorkflowExecutor
}

func (s *IntegrationTestSuite) SetupSuite() {
	s.T().Log("Setting up integration test suite...")
	s.testLogger = slog.Default()

	if os.Getenv("TEST_MOCK_SERVICES") == "true" {
		s.mockAuthServer = startMockAuthService(s.T())
		config.Config.ServiceEndpoint = s.mockAuthServer.URL
	}

	var err error
	dc := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		workflow.NewCompressionCodec(1024),
	)
	s.temporalClient, err = client.Dial(client.Options{
		HostPort:      temporalGRPCAddr,
		DataConverter: dc,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64 * 1024 * 1024)),
			},
		},
	})
	s.Require().NoError(err, "Failed to create Temporal client")
	s.T().Log("Temporal client initialized.")

	err = system.EnsureSearchAttributes(context.Background(), s.temporalClient, s.testLogger, "default")
	if err != nil {
		s.T().Logf("Warning: Failed to ensure search attributes: %v. This may cause tests to fail if they rely on new attributes.", err)
	}

	s.testWorkflowDao, err = storage.NewWorkflowDao()
	s.Require().NoError(err, "Failed to create WorkflowDao")

	s.testOptimizerDao, err = storage.NewOptimizerDao()
	s.Require().NoError(err, "Failed to create OptimizerDao")
	s.applySQLSchema("sql/optimizer_schema.sql")

	s.configService, err = configSvc.NewService()
	s.Require().NoError(err, "Failed to create ConfigService")

	s.optimizerService = optimizer.NewService(s.testOptimizerDao, s.temporalClient)

	s.eventRegistry = events.NewEventRegistry(s.testWorkflowDao, s.testLogger)
	go s.eventRegistry.StartSync(context.Background(), 1*time.Second)

	s.workflowExecutor, err = workflow.NewWorkflowExecutor(s.testWorkflowDao, s.configService.(*configSvc.Service), s.temporalClient, dc)
	s.Require().NoError(err, "Failed to create WorkflowExecutor")

	taskRegistry := tasks.NewInitializedTaskRegistry()
	taskWorker := s.workflowExecutor.GetWorker()
	for _, task := range taskRegistry.ListTasks() {
		wrapper := &tasks.TaskWrapper{Task: task, TemporalClient: s.workflowExecutor.GetClient(), Store: s.testWorkflowDao, Converter: dc}
		taskWorker.RegisterActivityWithOptions(wrapper.Execute, activity.RegisterOptions{
			Name: task.GetName(),
		})
	}

	go func() {
		if err := s.workflowExecutor.Start(); err != nil {
			s.T().Logf("Local worker stopped: %v", err)
		}
	}()

	s.workflowService = workflow.NewService(s.temporalClient, s.testWorkflowDao, dc, taskRegistry, s.workflowExecutor, s.configService)
	s.eventConsumer = events.NewConsumer(s.eventRegistry, s.workflowService, s.testLogger)

	apiSrv := api.NewServer(s.workflowService, s.configService)
	apiSrv.SetWorkflowService(s.workflowService) // This line is crucial
	apiSrv.SetOptimizerService(s.optimizerService)
	s.apiServer = httptest.NewServer(apiSrv.GetHandler())
	apiBaseURL = s.apiServer.URL
	s.T().Logf("In-process API server started at %s", apiBaseURL)
}

func (s *IntegrationTestSuite) applySQLSchema(path string) {
	content, err := os.ReadFile(path)
	s.Require().NoError(err, "Failed to read schema file: "+path)

	_, err = s.testWorkflowDao.Db().Exec(string(content))
	s.Require().NoError(err, "Failed to execute schema: "+path)
}

func (s *IntegrationTestSuite) TearDownSuite() {
	s.T().Log("Tearing down integration test suite...")

	if s.workflowExecutor != nil {
		s.workflowExecutor.Stop()
	}

	// Close mock auth service
	if s.mockAuthServer != nil {
		s.mockAuthServer.Close()
	}

	if s.apiServer != nil {
		s.apiServer.Close()
	}

	if s.temporalClient != nil {
		s.temporalClient.Close()
	}
	s.T().Log("Integration test suite torn down.")
}

func (s *IntegrationTestSuite) addRequestHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", testTenantID)
	req.Header.Set("X-Account-ID", testAccountID)
	req.Header.Set("X-User-ID", testUserID)
}

func (s *IntegrationTestSuite) resolveTemporalWorkflowID(reqCtx context.Context, workflowDefinitionID, runID string) (string, error) {
	for range 3 {
		query := fmt.Sprintf("%s='%s' and RunId='%s' and %s='%s' and %s='%s'",
			model.SearchAttrWorkflowID, workflowDefinitionID, runID,
			model.SearchAttrTenantID, testTenantID,
			model.SearchAttrAccountID, testAccountID)

		listResp, err := s.temporalClient.ListWorkflow(reqCtx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:    query,
			PageSize: 1, // We only expect one result
		})
		if err != nil {
			return "", fmt.Errorf("failed to list workflow executions to resolve Temporal Workflow ID: %w", err)
		}
		if len(listResp.Executions) == 0 {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		return listResp.Executions[0].Execution.GetWorkflowId(), nil
	}

	return "", common.ErrorNotFound(fmt.Sprintf("workflow execution not found for definition ID '%s' and run ID '%s'", workflowDefinitionID, runID))

}

func (s *IntegrationTestSuite) createWorkflow(workflow model.Workflow) (model.Workflow, string, error) {
	s.deleteWorkflowByName(workflow.Name, true)

	body, err := json.Marshal(workflow)
	s.Require().NoError(err, "Failed to marshal workflow")

	req, err := http.NewRequest(http.MethodPost, apiBaseURL+"/workflows", bytes.NewBuffer(body))
	s.Require().NoError(err, "Failed to create request")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusCreated, resp.StatusCode, "Expected 201 Created")
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.T().Logf("createWorkflow: Response Body: %s", string(bodyBytes))
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	var createResponse struct {
		WorkflowId string `json:"id"`
		Token      string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&createResponse)
	s.Require().NoError(err, "Failed to decode create workflow response body")

	workflow.ID = createResponse.WorkflowId
	return workflow, createResponse.Token, nil
}

func (s *IntegrationTestSuite) createAndActivateWorkflow(workflow model.Workflow) (model.Workflow, string, error) {
	wf, token, err := s.createWorkflow(workflow)
	if err != nil {
		return wf, token, err
	}

	wf.Status = model.WorkflowStatusActive
	updatedWf, err := s.updateWorkflow(wf)
	if err != nil {
		return wf, token, err
	}
	return updatedWf, token, nil
}

func (s *IntegrationTestSuite) updateWorkflow(workflow model.Workflow) (model.Workflow, error) {
	body, err := json.Marshal(workflow)
	s.Require().NoError(err, "Failed to marshal workflow for update")

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/workflows/%s", apiBaseURL, workflow.ID), bytes.NewBuffer(body))
	s.Require().NoError(err, "Failed to create update request")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send update request")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for update workflow")
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.T().Logf("updateWorkflow: Response Body: %s", string(bodyBytes))
		return model.Workflow{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var updatedWorkflow model.Workflow
	err = json.NewDecoder(resp.Body).Decode(&updatedWorkflow)
	s.Require().NoError(err, "Failed to decode update workflow response")

	return updatedWorkflow, nil
}

func (s *IntegrationTestSuite) UpsertWorkflow(workflow model.Workflow) (model.Workflow, string, error) {
	// 1. Search for workflow by name
	listResponse := s.listWorkflows()
	var existingWorkflow *model.Workflow
	for _, wf := range listResponse.Workflows {
		if wf.Name == workflow.Name {
			existingWorkflow = &wf
			break
		}
	}

	if existingWorkflow != nil {
		workflow.ID = existingWorkflow.ID
		updatedWf, err := s.updateWorkflow(workflow)
		return updatedWf, "", err
	}

	return s.createWorkflow(workflow)
}

func (s *IntegrationTestSuite) triggerWebhook(workflowID string, payload string, token string) error {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/webhook/%s", apiBaseURL, workflowID), bytes.NewBufferString(payload))
	s.Require().NoError(err, "Failed to create webhook request")
	s.addRequestHeaders(req)
	req.Header.Add("X-Webhook-Secret", token)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send webhook request")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.T().Logf("triggerWebhook: Response Body: %s", string(bodyBytes))
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	s.Assert().Equal(http.StatusAccepted, resp.StatusCode, "Expected 202 Accepted for webhook trigger")
	return nil
}

func (s *IntegrationTestSuite) deleteWorkflow(workflowID string, ignoreNotFound bool) {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/workflows/%s", apiBaseURL, workflowID), nil)
	s.Require().NoError(err, "Failed to create delete request")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send delete request")
	defer func() { _ = resp.Body.Close() }()

	if ignoreNotFound && resp.StatusCode == http.StatusNotFound {
		s.T().Logf("Attempted to delete workflow %s, but it was not found. (ignoreNotFound=true)", workflowID)
		return
	}

	s.Assert().Equal(http.StatusNoContent, resp.StatusCode, "Expected 204 No Content for delete workflow")
	s.T().Logf("Successfully deleted workflow %s", workflowID)
}

func (s *IntegrationTestSuite) deleteWorkflowByName(workflowName string, ignoreNotFound bool) {
	listResponse := s.listWorkflows()
	var workflowIDToDelete string
	for _, wf := range listResponse.Workflows {
		if wf.Name == workflowName {
			workflowIDToDelete = wf.ID
			break
		}
	}

	if workflowIDToDelete == "" {
		if ignoreNotFound {
			s.T().Logf("Attempted to delete workflow by name %s, but it was not found. (ignoreNotFound=true)", workflowName)
			return
		}
		s.FailNow("Workflow not found for deletion by name", workflowName)
	}

	s.deleteWorkflow(workflowIDToDelete, ignoreNotFound)
}

func (s *IntegrationTestSuite) executeWorkflow(workflowID string, inputs map[string]any) (string, error) {
	var body io.Reader
	if inputs != nil {
		bodyBytes, err := json.Marshal(inputs)
		s.Require().NoError(err)
		body = bytes.NewBuffer(bodyBytes)
	} else {
		body = bytes.NewBufferString("{}")
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/trigger", apiBaseURL, workflowID), body)
	s.Require().NoError(err, "Failed to create request")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.T().Logf("executeWorkflow: Response Body: %s", string(bodyBytes))
		resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for further decoding if needed
	}

	respBody, err := io.ReadAll(resp.Body)
	s.Require().NoError(err, "Failed to read response body")

	var result map[string]string
	err = json.Unmarshal(respBody, &result)
	s.Require().NoError(err, "Failed to unmarshal response body")

	runID, ok := result["execution_id"]
	s.Require().True(ok, "Expected 'execution_id' in response")

	return runID, nil
}

func (s *IntegrationTestSuite) getWorkflow(workflowID string) model.Workflow {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/workflows/%s", apiBaseURL, workflowID), nil)
	s.Require().NoError(err, "Failed to create request for getting workflow")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for getting workflow")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for get workflow")

	var retrievedWorkflow model.Workflow
	err = json.NewDecoder(resp.Body).Decode(&retrievedWorkflow)
	s.Require().NoError(err, "Failed to decode get workflow response")
	return retrievedWorkflow
}

func (s *IntegrationTestSuite) getWorkflowExecutionDetails(workflowID string, runID string) *model.WorkflowExecutionDetails {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/workflows/%s/runs/%s", apiBaseURL, workflowID, runID), nil)
	s.Require().NoError(err, "Failed to create request for getting workflow execution details")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for getting workflow execution details")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	s.Assert().NoError(err)

	if resp.StatusCode != http.StatusOK {
		s.T().Log("getWorkflowExecutionDetails, error", string(body))
	}

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for get workflow execution details")

	var executionDetails model.WorkflowExecutionDetails
	err = common.UnmarshalJson(body, &executionDetails)
	s.Require().NoError(err, "Failed to decode get workflow execution details response")
	return &executionDetails
}

// waitForWorkflowCompletion waits for a workflow execution to complete.
func (s *IntegrationTestSuite) waitForWorkflowCompletion(workflowID, runID string) *model.WorkflowExecutionDetails {
	s.T().Logf("Waiting for workflow %s (run %s) to complete...", workflowID, runID)
	timeout := 60 * time.Second
	pollInterval := 1 * time.Second
	startTime := time.Now()

	for time.Since(startTime) < timeout {
		details := s.getWorkflowExecutionDetails(workflowID, runID)
		if details.Status == model.WorkflowExecutionStatusCompleted ||
			details.Status == model.WorkflowExecutionStatusFailed ||
			details.Status == model.WorkflowExecutionStatusCanceled ||
			details.Status == model.WorkflowExecutionStatusTerminated ||
			details.Status == model.WorkflowExecutionStatusTimedOut {
			s.T().Logf("Workflow %s (run %s) completed with status: %s", workflowID, runID, details.Status)
			return details
		}
		time.Sleep(pollInterval)
	}
	s.FailNow("Workflow did not complete within timeout", workflowID, runID)
	return nil // Should not be reached
}

func (s *IntegrationTestSuite) getWorkflowState(workflowID string) []model.WorkflowStateItem {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/workflows/%s/state", apiBaseURL, workflowID), nil)
	s.Require().NoError(err, "Failed to create request for getting workflow state")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for getting workflow state")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for get workflow state")

	var state []model.WorkflowStateItem
	err = json.NewDecoder(resp.Body).Decode(&state)
	s.Require().NoError(err, "Failed to decode get workflow state response")
	return state
}
func (s *IntegrationTestSuite) loadWorkflowFromFile(filePath string) model.Workflow {
	data, err := os.ReadFile(filePath)
	s.Require().NoError(err, "Failed to read workflow file: %s", filePath)

	var workflow model.Workflow
	err = yaml.Unmarshal(data, &workflow)
	s.Require().NoError(err, "Failed to unmarshal workflow from file: %s", filePath)

	return workflow
}

func (s *IntegrationTestSuite) listWorkflows(filters ...map[string]string) model.ListWorkflowResponse {
	url := apiBaseURL + "/workflows"
	if len(filters) > 0 && filters[0] != nil {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		s.Require().NoError(err, "Failed to create request for listing workflows")
		q := req.URL.Query()
		for k, v := range filters[0] {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
		url = req.URL.String()
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	s.Require().NoError(err, "Failed to create request for listing workflows")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for listing workflows")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for list workflows")

	var listResponse model.ListWorkflowResponse
	err = json.NewDecoder(resp.Body).Decode(&listResponse)
	s.Require().NoError(err, "Failed to decode list workflows response")
	s.T().Logf("listWorkflows: Response: %+v", listResponse) // DEBUG
	return listResponse
}

func (s *IntegrationTestSuite) listWorkflowExecutions(workflowID string) model.ListWorkflowExecutionResponse {
	return s.listWorkflowExecutionsWithFilters(workflowID, nil)
}

func (s *IntegrationTestSuite) listWorkflowExecutionsWithFilters(workflowID string, filters map[string]string) model.ListWorkflowExecutionResponse {
	url := fmt.Sprintf("%s/workflows/%s/runs", apiBaseURL, workflowID)
	if len(filters) > 0 {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		s.Require().NoError(err, "Failed to create request object for query params")
		q := req.URL.Query()
		for k, v := range filters {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
		url = req.URL.String()
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	s.Require().NoError(err, "Failed to create request for listing workflow executions")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for listing workflow executions")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for list workflow executions")

	var listResponse model.ListWorkflowExecutionResponse
	err = json.NewDecoder(resp.Body).Decode(&listResponse)
	s.Require().NoError(err, "Failed to decode list workflow executions response")
	return listResponse
}

// Helper for making HTTP requests and getting response for tests
func (s *IntegrationTestSuite) request(method, path string, payload any) *http.Response {
	var body io.Reader
	if payload != nil {
		jsonPayload, err := json.Marshal(payload)
		s.Require().NoError(err, "Failed to marshal payload")
		body = bytes.NewBuffer(jsonPayload)
	}

	req, err := http.NewRequest(method, apiBaseURL+path, body)
	s.Require().NoError(err, "Failed to create HTTP request")
	s.addRequestHeaders(req)

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send HTTP request")

	return resp
}

func (s *IntegrationTestSuite) createConfig(key, value, typeStr string, labels map[string]string, metadata map[string]any) {
	payload := map[string]any{
		"key":      key,
		"value":    value,
		"type":     typeStr,
		"labels":   labels,
		"metadata": metadata,
	}
	resp := s.request("POST", "/configs", payload)
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		s.T().Logf("createConfig failed. Status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
	}
	s.Require().Equal(http.StatusCreated, resp.StatusCode)
	s.Require().NoError(resp.Body.Close())
}

func (s *IntegrationTestSuite) getConfig(key string) model.Config {
	resp := s.request("GET", "/configs/"+key, nil)
	s.Require().Equal(http.StatusOK, resp.StatusCode)
	defer func() {
		s.Require().NoError(resp.Body.Close())
	}()
	var config model.Config
	err := json.NewDecoder(resp.Body).Decode(&config)
	s.Require().NoError(err)
	return config
}

func (s *IntegrationTestSuite) listConfigs(labelsFilter map[string]string) []model.Config {
	url_path := "/configs"
	if len(labelsFilter) > 0 {
		params := net_url.Values{} // Use alias
		for k, v := range labelsFilter {
			params.Add("labels["+k+"]", v)
		}
		url_path += "?" + params.Encode()
	}
	resp := s.request("GET", url_path, nil)
	s.Require().Equal(http.StatusOK, resp.StatusCode)
	defer func() {
		s.Require().NoError(resp.Body.Close())
	}()
	var configs []model.Config
	err := json.NewDecoder(resp.Body).Decode(&configs)
	s.Require().NoError(err)
	return configs
}

func (s *IntegrationTestSuite) deleteConfig(key string) {
	resp := s.request("DELETE", "/configs/"+key, nil)
	s.Require().Equal(http.StatusNoContent, resp.StatusCode)
	s.Require().NoError(resp.Body.Close())
}

func TestIntegrationTestSuite(t *testing.T) {
	// Check if the integration tests are enabled
	if config.Config.RunIntegrationTests != "true" {
		t.Skip("Skipping integration tests. Set RUN_INTEGRATION_TESTS=true to enable.")
	}
	suite.Run(t, new(IntegrationTestSuite))
}
