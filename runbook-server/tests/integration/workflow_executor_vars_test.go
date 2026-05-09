package integration_test

import (
	"encoding/json"
	"nudgebee/runbook/internal/model"
	"time"
)

func (s *IntegrationTestSuite) TestWorkflowContextVariables() {
	s.T().Log("Running TestWorkflowContextVariables...")

	// 1. Define a workflow that outputs the injected inputs
	wfDef := model.Workflow{
		Name: "test-context-vars",
		Definition: model.WorkflowDefinition{
			Inputs: []model.Input{},
			Triggers: []model.Trigger{
				{Type: model.WorkflowTriggerManual},
			},
			Tasks: []model.Task{
				{
					ID:   "output-vars",
					Type: "core.print",
					Params: map[string]any{
						"message": "logging vars",
					},
				},
			},
			Output: map[string]any{
				"exec_time":  "{{ Inputs.workflow_execution_time }}",
				"sched_time": "{{ Inputs.workflow_scheduled_time }}",
				"wf_exec_id": "{{ Inputs.workflow_execution_id }}",
				"wf_id":      "{{ Inputs.workflow_id }}",
				"wf_name":    "{{ Inputs.workflow_name }}",
				"last_exec":  "{{ Inputs.workflow_last_execution_time }}",
			},
		},
	}

	createdWf, _, err := s.createAndActivateWorkflow(wfDef)
	s.Require().NoError(err, "Failed to create workflow")
	s.T().Logf("Created workflow ID: %s", createdWf.ID)

	// 2. Execute the workflow (First Run)
	runID, err := s.executeWorkflow(createdWf.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow")

	// 3. Verify Output of First Run
	details := s.waitForWorkflowCompletion(createdWf.ID, runID)
	s.Require().Equal(model.WorkflowExecutionStatusCompleted, details.Status, "Workflow execution failed")

	resultBytes, err := json.Marshal(details.WorkflowResult)
	s.Require().NoError(err)
	var result map[string]string
	err = json.Unmarshal(resultBytes, &result)
	s.Require().NoError(err)

	s.Assert().NotEmpty(result["exec_time"], "workflow_execution_time should be present")
	s.Assert().NotEmpty(result["sched_time"], "workflow_scheduled_time should be present")
	s.Assert().Equal(runID, result["wf_exec_id"], "workflow_execution_id mismatch")
	s.Assert().Equal(createdWf.ID, result["wf_id"], "workflow_id mismatch")
	s.Assert().Equal(createdWf.Name, result["wf_name"], "workflow_name mismatch")
	s.Assert().Empty(result["last_exec"], "workflow_last_execution_time should be empty on first run")

	firstExecTime := result["exec_time"]

	// 4. Execute the workflow AGAIN (Second Run)
	time.Sleep(1 * time.Second)

	runID2, err := s.executeWorkflow(createdWf.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow 2nd time")

	// 5. Verify Output of Second Run
	details2 := s.waitForWorkflowCompletion(createdWf.ID, runID2)
	s.Require().Equal(model.WorkflowExecutionStatusCompleted, details2.Status, "Workflow execution 2 failed")

	resultBytes2, err := json.Marshal(details2.WorkflowResult)
	s.Require().NoError(err)
	var result2 map[string]string
	err = json.Unmarshal(resultBytes2, &result2)
	s.Require().NoError(err)

	s.Assert().NotEmpty(result2["exec_time"], "workflow_execution_time should be present")
	s.Assert().NotEqual(firstExecTime, result2["exec_time"], "workflow_execution_time should differ")
	// Verify last_execution_time equals the first run's execution_time
	s.Assert().Equal(firstExecTime, result2["last_exec"], "workflow_last_execution_time should match first run's time")
}
