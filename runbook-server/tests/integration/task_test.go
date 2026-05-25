package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestListTasks() {
	s.T().Log("Running TestListTasks...")

	req, err := http.NewRequest(http.MethodGet, apiBaseURL+"/tasks", nil)
	s.Require().NoError(err, "Failed to create request for listing tasks")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for listing tasks")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for list tasks")

	body, err := io.ReadAll(resp.Body)
	s.Require().NoError(err, "Failed to decode list tasks response")
	var tasks model.ListTaskDefinitionResponse
	err = json.Unmarshal(body, &tasks)
	s.Require().NoError(err, "Failed to decode list tasks response")
	s.Assert().NotEmpty(tasks.Tasks, "Expected a non-empty list of tasks")
}

func (s *IntegrationTestSuite) TestExecuteTask() {
	s.T().Log("Running TestExecuteTask...")

	taskType := "core.print"
	params := map[string]any{
		"message": "Hello from integration test",
	}
	body, err := json.Marshal(params)
	s.Require().NoError(err, "Failed to marshal task params")

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tasks/%s/execute", apiBaseURL, taskType), bytes.NewBuffer(body))
	s.Require().NoError(err, "Failed to create request for executing task")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for executing task")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for execute task")

	var response map[string]any
	err = json.NewDecoder(resp.Body).Decode(&response)
	s.Require().NoError(err, "Failed to decode execute task response")
	s.Assert().Contains(response["result"].(map[string]any)["data"], "Hello from integration test", "Expected the result to contain the script output")
}
