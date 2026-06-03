package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) retriggerWorkflow(workflowID, executionID string, inputs map[string]any) (string, error) {
	var body io.Reader
	if inputs != nil {
		bodyBytes, err := json.Marshal(inputs)
		s.Require().NoError(err)
		body = bytes.NewBuffer(bodyBytes)
	} else {
		body = bytes.NewBufferString("{}")
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/executions/%s/retrigger", apiBaseURL, workflowID, executionID), body)
	s.Require().NoError(err, "Failed to create request")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request")
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	s.Require().NoError(err, "Failed to read response body")

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("retrigger failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]string
	err = json.Unmarshal(respBody, &result)
	s.Require().NoError(err, "Failed to unmarshal response body")

	runID, ok := result["execution_id"]
	s.Require().True(ok, "Expected 'execution_id' in response")

	return runID, nil
}

func (s *IntegrationTestSuite) TestRetriggerWorkflowExecution() {
	s.T().Log("Running TestRetriggerWorkflowExecution...")

	// 1. Create and Activate a simple workflow
	wf := model.Workflow{
		Name: "retrigger-test-workflow",
		Definition: model.WorkflowDefinition{
			Inputs: []model.Input{
				{ID: "message", Type: "string", Required: true},
				{ID: "other", Type: "string", Default: "default"},
			},
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks: []model.Task{
				{
					ID:   "task1",
					Type: "core.print",
					Params: map[string]any{
						"message": "hello",
					},
				},
			},
		},
	}

	createdWf, _, err := s.createAndActivateWorkflow(wf)
	s.Require().NoError(err)

	// 2. Execute it once
	initialInputs := map[string]any{"message": "hello first"}
	runID, err := s.executeWorkflow(createdWf.ID, initialInputs)
	s.Require().NoError(err)

	// 3. Wait for it to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWf.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	err = workflowExecution.Get(context.Background(), nil)
	s.Require().NoError(err)

	// 4. Retrigger it without overrides
	retriggeredRunID, err := s.retriggerWorkflow(createdWf.ID, runID, nil)
	s.Require().NoError(err)
	s.Require().NotEqual(runID, retriggeredRunID)

	// 5. Verify retriggered execution has same inputs
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/workflows/%s/executions/%s", apiBaseURL, createdWf.ID, retriggeredRunID), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() { _ = resp.Body.Close() }()
	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	var retriggeredDetails model.WorkflowExecutionDetails
	err = json.NewDecoder(resp.Body).Decode(&retriggeredDetails)
	s.Require().NoError(err)

	s.Assert().Equal("hello first", retriggeredDetails.Inputs["message"])
	s.Assert().Equal("default", retriggeredDetails.Inputs["other"])
	// 6. Retrigger it with overrides
	overrideInputs := map[string]any{"message": "hello retriggered", "other": "new value"}
	retriggeredRunID2, err := s.retriggerWorkflow(createdWf.ID, runID, overrideInputs)
	s.Require().NoError(err)

	// 7. Verify overrides are applied
	req2, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/workflows/%s/executions/%s", apiBaseURL, createdWf.ID, retriggeredRunID2), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req2)
	resp2, err := http.DefaultClient.Do(req2)
	s.Require().NoError(err)
	defer func() { _ = resp2.Body.Close() }()
	s.Assert().Equal(http.StatusOK, resp2.StatusCode)

	var retriggeredDetails2 model.WorkflowExecutionDetails
	err = json.NewDecoder(resp2.Body).Decode(&retriggeredDetails2)
	s.Require().NoError(err)

	s.Assert().Equal("hello retriggered", retriggeredDetails2.Inputs["message"])
	s.Assert().Equal("new value", retriggeredDetails2.Inputs["other"])
}
