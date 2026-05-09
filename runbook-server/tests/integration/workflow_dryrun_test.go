package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) dryRunWorkflow(req model.DryRunWorkflowRequest) (*model.DryRunWorkflowResponse, error) {
	url := fmt.Sprintf("%s/workflows/dry-run", apiBaseURL)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	s.addRequestHeaders(httpReq)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.T().Error("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var dryRunResp model.DryRunWorkflowResponse
	if err := json.NewDecoder(resp.Body).Decode(&dryRunResp); err != nil {
		return nil, err
	}

	return &dryRunResp, nil
}

func (s *IntegrationTestSuite) TestDryRunWorkflow() {
	s.T().Log("Running TestDryRunWorkflow...")

	s.T().Run("Successful Dry Run", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Inputs: []model.Input{
					{ID: "name", Type: "string", Default: "World"},
				},
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{
						ID:   "task1",
						Type: "core.print", // Assuming core.print or similar exists, or using scripting
						Params: map[string]any{
							"message": "Hello {{ Inputs.name }}",
						},
					},
				},
				Output: map[string]any{
					"greeting": "Hello {{ Inputs.name }}",
				},
			},
			Inputs: map[string]any{
				"name": "DryRun",
			},
		}

		resp, err := s.dryRunWorkflow(req)
		s.Require().NoError(err)
		s.Require().NotNil(resp)
		s.Assert().Equal(model.WorkflowExecutionStatusCompleted, resp.Status)

		outputMap, ok := resp.Output.(map[string]any)
		s.Assert().True(ok, "Output should be a map")
		s.Assert().Equal("Hello DryRun", outputMap["greeting"])

		// Verify Tasks and Inputs
		s.Assert().NotEmpty(resp.Tasks, "Tasks should not be empty")
		s.Assert().Equal(1, len(resp.Tasks), "Should have exactly 1 task")
		s.Assert().Equal("task1", resp.Tasks[0].ID)
		s.Assert().Equal(model.TaskStatusCompleted, resp.Tasks[0].Status)

		s.Assert().NotEmpty(resp.Inputs, "Inputs should not be empty")
		s.Assert().Equal("DryRun", resp.Inputs["name"])
	})

	s.T().Run("Validation Error", func(t *testing.T) {
		req := model.DryRunWorkflowRequest{
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Tasks: []model.Task{
					{
						ID:   "task1",
						Type: "unknown-task-type",
					},
				},
			},
		}

		_, err := s.dryRunWorkflow(req)
		s.Assert().Error(err)
		s.Assert().Contains(err.Error(), "unexpected status code: 400") // Validation failure
	})
}
