package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestGetWorkflowHasura_NotFound() {
	s.T().Log("Running TestGetWorkflowHasura_NotFound...")

	// Construct Hasura Action Request
	payload := map[string]any{
		"action": map[string]string{
			"name": "workflow_get",
		},
		"input": map[string]any{
			"request": map[string]any{
				"account_id": testAccountID,
				"id":         "non-existent-workflow-id",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
	s.Require().NoError(err)

	// Add headers. Note: Hasura actions usually come with their own set of headers or
	// rely on session variables passed in the body. But our code checks headers too.
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Logf("failed to close response body: %v", err)
		}
	}()

	// Verify we get a 404 Not Found
	s.Assert().Equal(http.StatusNotFound, resp.StatusCode, "Expected 404 Not Found for non-existent workflow via Hasura action")
}

func (s *IntegrationTestSuite) TestWorkflowCountHasura() {
	s.T().Log("Running TestWorkflowCountHasura...")

	// Create a test workflow first to ensure we have something to count
	testWorkflow := model.Workflow{
		Name:   "test-workflow-count",
		Status: model.WorkflowStatusActive,
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks: []model.Task{
				{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
			},
		},
	}
	createdWf, _, err := s.createAndActivateWorkflow(testWorkflow)
	s.Require().NoError(err)
	defer s.deleteWorkflow(createdWf.ID, true)

	s.T().Run("Count all workflows", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id": testAccountID,
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for workflow count")

		var countResponse model.WorkflowCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 workflow")
	})

	s.T().Run("Count workflows with status filter", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id": testAccountID,
					"status":     "ACTIVE",
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for workflow count with status filter")

		var countResponse model.WorkflowCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 active workflow")
	})

	s.T().Run("Count workflows with trigger_type filter", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id":   testAccountID,
					"trigger_type": "manual",
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for workflow count with trigger_type filter")

		var countResponse model.WorkflowCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 manual trigger workflow")
	})
}

func (s *IntegrationTestSuite) TestWorkflowExecutionCountHasura() {
	s.T().Log("Running TestWorkflowExecutionCountHasura...")

	// Create and execute a test workflow to ensure we have executions to count
	testWorkflow := model.Workflow{
		Name:   "test-execution-count",
		Status: model.WorkflowStatusActive,
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks: []model.Task{
				{ID: "task1", Type: "scripting.run_script", Params: map[string]any{"script": "echo 'hello'"}},
			},
		},
	}
	createdWf, _, err := s.createAndActivateWorkflow(testWorkflow)
	s.Require().NoError(err)
	defer s.deleteWorkflow(createdWf.ID, true)

	// Execute the workflow to create an execution
	runID, err := s.executeWorkflow(createdWf.ID, nil)
	s.Require().NoError(err)
	s.waitForWorkflowCompletion(createdWf.ID, runID)

	s.T().Run("Count all executions", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_execution_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id": testAccountID,
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for execution count")

		var countResponse model.WorkflowExecutionCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 execution")
	})

	s.T().Run("Count executions with workflow_id filter", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_execution_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id":  testAccountID,
					"workflow_id": createdWf.ID,
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for execution count with workflow_id filter")

		var countResponse model.WorkflowExecutionCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 execution for this workflow")
	})

	s.T().Run("Count executions with status filter", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_execution_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id": testAccountID,
					"status":     "COMPLETED",
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for execution count with status filter")

		var countResponse model.WorkflowExecutionCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 completed execution")
	})

	s.T().Run("Count executions with trigger_type filter", func(t *testing.T) {
		payload := map[string]any{
			"action": map[string]string{
				"name": "workflow_get_execution_count",
			},
			"input": map[string]any{
				"request": map[string]any{
					"account_id":   testAccountID,
					"trigger_type": "manual",
				},
			},
		}

		bodyBytes, err := json.Marshal(payload)
		s.Require().NoError(err)

		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/rpc", apiBaseURL), bytes.NewBuffer(bodyBytes))
		s.Require().NoError(err)
		s.addRequestHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		s.Require().NoError(err)
		defer func() { _ = resp.Body.Close() }()

		s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for execution count with trigger_type filter")

		var countResponse model.WorkflowExecutionCountResponse
		respBody, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(respBody, &countResponse)
		s.Require().NoError(err)
		s.Assert().GreaterOrEqual(countResponse.Count, int64(1), "Expected at least 1 manual trigger execution")
	})
}
