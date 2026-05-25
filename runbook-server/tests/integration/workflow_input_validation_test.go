package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *IntegrationTestSuite) TestWorkflowInputValidation() {
	s.T().Log("Running TestWorkflowInputValidation...")

	// 1. Create the workflow
	workflow := s.loadWorkflowFromFile("testdata/test-input-validation-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "unable to create workflow")

	// 2. Case 1: Missing required input (required_string)
	s.T().Log("Case 1: Missing required input")
	params1 := map[string]any{
		"required_bool": true,
	}
	err = s.executeWorkflowExpectError(createdWorkflow.ID, params1)
	s.Require().NotNil(err, "Missing required input should return an error")
	s.Assert().Contains(err.Error(), "status 400: {\"error\":\"required input 'required_string' is missing\"}", "Expected specific error for missing required string")

	// 3. Case 2: Incorrect type (optional_int is string)
	s.T().Log("Case 2: Incorrect type")
	params2 := map[string]any{
		"required_string": "hello",
		"required_bool":   true,
		"optional_int":    "not_an_int",
	}
	err = s.executeWorkflowExpectError(createdWorkflow.ID, params2)
	s.Require().NotNil(err, "Incorrect type should return an error")
	s.Assert().Contains(err.Error(), "status 400: {\"error\":\"input 'optional_int' must be an integer, got string\"}", "Expected specific error for incorrect int type")

	// 4. Case 3: Empty required string
	s.T().Log("Case 4: Empty required string")
	params4 := map[string]any{
		"required_string": "",
		"required_bool":   true,
	}
	err = s.executeWorkflowExpectError(createdWorkflow.ID, params4)
	s.Require().NotNil(err, "Empty required string should return an error")
	s.Assert().Contains(err.Error(), "status 400: {\"error\":\"required string input 'required_string' cannot be empty\"}", "Expected specific error for empty required string")

	// 5. Case 5: Valid inputs
	s.T().Log("Case 5: Valid inputs")
	params3 := map[string]any{
		"required_string": "hello",
		"required_bool":   true,
		"optional_int":    123,
	}
	runID, err := s.executeWorkflow(createdWorkflow.ID, params3)
	s.Assert().Nil(err, "Valid inputs should succeed")
	s.T().Logf("Case 5: Valid execution RunID: %s", runID)

	// 6. Case 6: Missing required input that has a default value
	s.T().Log("Case 6: Missing required input with default value")
	params6 := map[string]any{
		"required_string": "hello",
		"required_bool":   true,
		// "required_with_default" is missing, but has default "default_value"
	}
	runID6, err := s.executeWorkflow(createdWorkflow.ID, params6)
	s.Assert().Nil(err, "Missing required input with default should succeed")
	s.T().Logf("Case 6: Valid execution RunID: %s", runID6)
}

// executeWorkflowExpectError executes a workflow and returns error if it fails, useful for testing validation.
func (s *IntegrationTestSuite) executeWorkflowExpectError(workflowID string, params map[string]any) error {
	reqBody, _ := json.Marshal(params)
	url := fmt.Sprintf("%s/workflows/%s/trigger", apiBaseURL, workflowID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
