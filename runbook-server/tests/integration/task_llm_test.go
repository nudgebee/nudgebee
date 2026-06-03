package integration_test

import (
	"context"
	"encoding/json"
)

func (s *IntegrationTestSuite) TestAIRouterRefWorkflow() {
	s.T().Log("Running TestAIRouterRefWorkflow...")

	// 1. Create the workflow
	workflow := s.loadWorkflowFromFile("testdata/test-ai-router-ref-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create AI router ref workflow")

	// 2. Execute the workflow with a "database" related query
	inputs := map[string]any{
		"user_request": "My database is slow",
	}
	runID, err := s.executeWorkflow(createdWorkflow.ID, inputs)
	s.Require().NoError(err, "Failed to execute AI router ref workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "AI router ref workflow execution failed or timed out")

	// 4. Verify the output
	var result map[string]any
	s.Require().NoError(json.Unmarshal([]byte(resultStr), &result), "Failed to unmarshal workflow result")
	s.Assert().Equal("database_issue", result["selected_branch"], "The AI should have selected 'database_issue'")

	// 5. Verify that the externally defined task was executed
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	var dbTaskExecuted bool
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if msg, ok := p["message"].(string); ok {
					if msg == "Handling DB issue via Ref" {
						dbTaskExecuted = true
					}
				}
			}
		}
	}
	s.Assert().True(dbTaskExecuted, "The 'handle_db_ref' task (defined externally) should have been executed")
}
