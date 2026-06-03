package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"nudgebee/runbook/internal/model"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func (s *IntegrationTestSuite) TestScheduledWorkflowExecution() {
	s.T().Log("Running TestScheduledWorkflowExecution...")

	// 1. Create a scheduled workflow
	workflow := s.loadWorkflowFromFile("testdata/test-scheduled-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create scheduled workflow")
	s.T().Logf("Created scheduled workflow with ID: %s", createdWorkflow.ID)

	// 2. Trigger the schedule immediately using the Temporal Schedule API
	// This avoids waiting for the next cron tick.
	// Note: Schedules are now indexed, starting at 0.
	scheduleID := fmt.Sprintf("workflow-schedule-%s-0", createdWorkflow.ID)
	handle := s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID)
	err = handle.Trigger(context.Background(), client.ScheduleTriggerOptions{
		Overlap: enums.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
	})
	s.Require().NoError(err, "Failed to manually trigger schedule")

	// 3. Wait for the workflow execution to start
	var runID string
	var temporalWorkflowID string
	s.Require().Eventually(func() bool {
		// List executions using the API to verify filtering works as expected
		// This implicitly checks if SearchAttributes were correctly propagated
		listResp := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{
			"type": "schedule",
		})
		if len(listResp.Executions) > 0 {
			runID = listResp.Executions[0].ID
			temporalWorkflowID = listResp.Executions[0].WorkflowID
			return true
		}
		return false
	}, 10*time.Second, 1*time.Second, "Scheduled workflow execution should have started and be visible via API")

	s.T().Logf("Scheduled workflow run ID: %s, Temporal Workflow ID: %s", runID, temporalWorkflowID)

	// 4. Wait for workflow to complete
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalWorkflowID, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Scheduled workflow execution failed or timed out")

	// 5. Verify the execution details
	details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Assert().Equal(model.WorkflowExecutionStatusCompleted, details.Status)
	// Note: TriggeredBy is intentionally empty for scheduled workflows as they don't have a user trigger.

	// Checking SearchAttributes propagation:
	s.Assert().Equal(createdWorkflow.ID, details.SearchAttributes[model.SearchAttrWorkflowID], "SearchAttribute WorkflowID mismatch")
	s.Assert().Equal("schedule", details.SearchAttributes[model.SearchAttrWorkflowTrigger], "SearchAttribute WorkflowTrigger mismatch")
}

func (s *IntegrationTestSuite) TestManualWorkflowExecution() {
	s.T().Log("Running TestManualWorkflowExecution...")

	// 1. Create a simple workflow
	workflow := s.loadWorkflowFromFile("testdata/test-manual-workflow.yaml")

	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow")
	s.Require().NotEmpty(createdWorkflow.ID, "Created workflow ID should not be empty")
	s.T().Logf("Created workflow with ID: %s", createdWorkflow.ID)

	// 2. Execute the workflow manually
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow")
	s.Require().NotEmpty(runID, "Workflow run ID should not be empty")
	s.T().Logf("Executed workflow %s with Run ID: %s", createdWorkflow.ID, runID)

	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)

	// Wait for workflow to complete
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Workflow execution failed or timed out")
	s.T().Logf("Workflow %s (Run ID: %s) completed with result: %s", createdWorkflow.ID, runID, result)

	// 4. Verify workflow status in the database (via API)
	s.T().Log("Verifying workflow status via API...")
	details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Assert().Equal(model.WorkflowExecutionStatusCompleted, details.Status)
}

func (s *IntegrationTestSuite) TestWebhookTriggerWorkflow() {
	s.T().Log("Running TestWebhookTriggerWorkflow...")

	// 1. Create a workflow with a webhook trigger
	workflow := s.loadWorkflowFromFile("testdata/test-webhook-workflow.yaml")

	createdWorkflow, token, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create webhook workflow")
	s.Require().NotEmpty(createdWorkflow.ID, "Created workflow ID should not be empty")
	s.Require().NotEmpty(token, "Webhook token should not be empty")
	s.T().Logf("Created webhook workflow with ID: %s, Token: %s", createdWorkflow.ID, token)

	// 2. Trigger the workflow via webhook
	payload := "Hello from webhook test"
	err = s.triggerWebhook(createdWorkflow.ID, payload, token)
	s.Require().NoError(err, "Failed to trigger webhook")

	// 3. Wait for workflow execution to start
	var runID string
	s.Require().Eventually(func() bool {
		listResp := s.listWorkflowExecutions(createdWorkflow.ID)
		if len(listResp.Executions) > 0 {
			runID = listResp.Executions[0].ID
			return true
		}
		return false
	}, 10*time.Second, 1*time.Second, "Workflow execution should have started")

	s.T().Logf("Webhook triggered workflow run ID: %s", runID)

	// 4. Wait for workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Webhook workflow execution failed or timed out")
	s.T().Logf("Webhook workflow completed with result: %s", result)

	// 5. Verify output contains the payload
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Require().NotEmpty(executionDetails.Tasks, "Should have executed tasks")
	// Assuming echo task is the first one
	echoTask := executionDetails.Tasks[0]
	s.Assert().Contains(echoTask.Output.(map[string]any)["data"], "Received payload: Hello from webhook test")

	// 6. Test Paused Workflow
	s.T().Log("Testing webhook trigger for PAUSED workflow...")

	// Pause the workflow
	pauseResp := s.request(http.MethodPost, fmt.Sprintf("/workflows/%s/pause", createdWorkflow.ID), nil)
	s.Require().Equal(http.StatusOK, pauseResp.StatusCode, "Failed to pause workflow")
	_ = pauseResp.Body.Close()

	// Verify status is PAUSED via API
	wf := s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusPaused, wf.Status, "Workflow status should be PAUSED")

	// Attempt to trigger webhook (should fail)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/webhook/%s", apiBaseURL, createdWorkflow.ID), bytes.NewBufferString(payload))
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	req.Header.Add("X-Webhook-Secret", token)
	failResp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() { _ = failResp.Body.Close() }()

	s.Assert().Equal(http.StatusConflict, failResp.StatusCode, "Expected 409 Conflict when triggering paused workflow via webhook")

	// Resume the workflow
	resumeResp := s.request(http.MethodPost, fmt.Sprintf("/workflows/%s/resume", createdWorkflow.ID), nil)
	s.Require().Equal(http.StatusOK, resumeResp.StatusCode, "Failed to resume workflow")
	_ = resumeResp.Body.Close()

	// 7. Test Webhook Cleanup on Delete
	s.T().Log("Testing webhook cleanup on workflow delete...")

	// Delete the workflow
	s.deleteWorkflow(createdWorkflow.ID, false)

	// Verify that the webhook is no longer active
	// Since the workflow is deleted, the webhook endpoint should return 404 Not Found (or 410 Gone)
	// The current handler implementation checks if the workflow exists first.
	// If the workflow is gone, it returns 404.

	reqCleanup, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/webhook/%s", apiBaseURL, createdWorkflow.ID), bytes.NewBufferString(payload))
	s.Require().NoError(err)
	s.addRequestHeaders(reqCleanup)
	reqCleanup.Header.Add("X-Webhook-Secret", token)
	cleanupResp, err := http.DefaultClient.Do(reqCleanup)
	s.Require().NoError(err)
	defer func() { _ = cleanupResp.Body.Close() }()

	// We expect 404 because the workflow record is gone from DB
	s.Assert().Equal(http.StatusNotFound, cleanupResp.StatusCode, "Expected 404 Not Found when triggering deleted workflow webhook")
}

func (s *IntegrationTestSuite) TestEventTriggerWorkflow() {
	s.T().Log("Running TestEventTriggerWorkflow...")

	// 1. Create a workflow with an event trigger
	workflow := s.loadWorkflowFromFile("testdata/test-event-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create event trigger workflow")
	s.Require().NotEmpty(createdWorkflow.ID, "Created workflow ID should not be empty")
	s.T().Logf("Created event trigger workflow with ID: %s", createdWorkflow.ID)

	// Ensure the event registry has refreshed and picked up the new workflow
	// It's set to 1-second sync, so wait a bit more than that.
	s.Eventually(func() bool {
		_ = s.eventRegistry.Refresh(context.Background()) // Force refresh
		matches := s.eventRegistry.Match("my.custom.event", testAccountID, map[string]any{"source": "integration-test", "account_id": testAccountID})
		return len(matches) > 0
	}, 5*time.Second, 500*time.Millisecond, "Event registry did not pick up the new workflow")
	s.T().Log("Event registry refreshed and contains the new workflow.")

	// 2. Simulate an incoming event
	eventPayload := map[string]any{
		"event_type": "my.custom.event",
		"account_id": testAccountID, // Ensure account_id is present for tenancy check
		"source":     "integration-test",
		"data":       "some important data",
	}
	eventData, err := json.Marshal(eventPayload)
	s.Require().NoError(err, "Failed to marshal event payload")

	err = s.eventConsumer.ProcessMessage(eventData)
	s.Require().NoError(err, "Failed to process event message")

	// 3. Verify that a workflow execution was triggered
	var runID string
	s.Require().Eventually(func() bool {
		listResp := s.listWorkflowExecutions(createdWorkflow.ID)
		if len(listResp.Executions) > 0 {
			runID = listResp.Executions[0].ID
			return true
		}
		return false
	}, 10*time.Second, 1*time.Second, "Workflow execution should have started")
	s.T().Logf("Event triggered workflow run ID: %s", runID)

	// 4. Wait for workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Event workflow execution failed or timed out")
	s.T().Logf("Event workflow completed with result: %s", result)

	// 5. Verify output contains the payload data
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Require().NotNil(executionDetails, "Execution details should not be nil")
	s.Require().NotEmpty(executionDetails.Tasks, "Should have executed tasks")

	// Assuming 'print-event' task is present and its output contains the message
	var printTaskOutput map[string]any
	for _, task := range executionDetails.Tasks {
		if task.ID == "print-event" {
			printTaskOutput = task.Output.(map[string]any)
			break
		}
	}
	s.Require().NotNil(printTaskOutput, "print-event task output not found")
	s.Assert().Contains(printTaskOutput["data"], "Received event: my.custom.event from integration-test. Full payload: ", "Print task output should contain event data")
	s.Assert().Contains(printTaskOutput["data"], `"some important data"`, "Print task output should contain event data")

	// 6. Verify searching by execution tags
	// We search for 'RunbookTags' (the Temporal search attribute name) containing our tag
	// Note: In real Temporal, Keyword Arrays are queried using operators.
	// Since we are using a real client, we can try to list.
	// However, usually custom Search Attributes need to be registered in the cluster for queries to work.
	// Assuming our test environment (e.g. temporal-setup-job) or EnsureSearchAttributes handles this.

	// For this test, we'll just assume the query syntax is standard.
	// "nb_execution_tags = 'custom_id:some important data'"
	// We search for the custom tag we set in the YAML
	searchQuery := "nb_execution_tags = 'custom_id:some important data'"

	// Wait a bit for visibility to index (ES consistency)
	s.Eventually(func() bool {
		resp, err := s.temporalClient.ListWorkflow(context.Background(), &workflowservice.ListWorkflowExecutionsRequest{
			Query: searchQuery,
		})
		if err != nil {
			s.T().Logf("Search query failed: %v", err)
			return false
		}
		for _, exec := range resp.Executions {
			// Also check for the specific run ID, as there might be multiple workflows with the same event type
			if exec.Execution.GetRunId() == runID {
				return true
			}
		}
		return false
	}, 5*time.Second, 500*time.Millisecond, "Failed to find workflow by nb_event_type")

	// 7. Verify searching by custom execution tags (nb_execution_tags)
	// Example query for tags that were set dynamically
	// We verify both custom tag and auto-injected tag (nb_event_source)
	searchQueryTags := fmt.Sprintf("%s = '%s' AND %s = '%s'",
		model.SearchAttrExecutionTags, "nb_event_source:integration-test",
		model.SearchAttrExecutionTags, "custom_id:some important data",
	)
	s.Eventually(func() bool {
		resp, err := s.temporalClient.ListWorkflow(context.Background(), &workflowservice.ListWorkflowExecutionsRequest{
			Query: searchQueryTags,
		})
		if err != nil {
			s.T().Logf("Search query for tags failed: %v", err)
			return false
		}
		for _, exec := range resp.Executions {
			if exec.Execution.GetRunId() == runID {
				return true
			}
		}
		return false
	}, 5*time.Second, 500*time.Millisecond, "Failed to find workflow by custom execution tags")

	// Assert that the mock executor expectations were met
	// Removed mock expectations as we are now using the real service.
}

func (s *IntegrationTestSuite) TestMultipleScheduledWorkflowExecution() {
	s.T().Log("Running TestMultipleScheduledWorkflowExecution...")

	// 1. Create a workflow with multiple schedules
	workflow := s.loadWorkflowFromFile("testdata/test-multiple-schedule-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create multiple schedule workflow")
	s.T().Logf("Created workflow with ID: %s", createdWorkflow.ID)

	// 2. Verify schedules are created and trigger them
	scheduleIndices := []struct {
		index          int
		expectedAction string
		expectedMode   string
	}{
		{0, "start", "standard"},
		{1, "stop", "maintenance"},
	}

	for _, item := range scheduleIndices {
		scheduleID := fmt.Sprintf("workflow-schedule-%s-%d", createdWorkflow.ID, item.index)

		// Check existence
		handle := s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID)
		_, err := handle.Describe(context.Background())
		s.Require().NoError(err, fmt.Sprintf("Schedule %d not found", item.index))

		// Trigger manually
		err = handle.Trigger(context.Background(), client.ScheduleTriggerOptions{
			Overlap: enums.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
		})
		s.Require().NoError(err, fmt.Sprintf("Failed to manually trigger schedule %d", item.index))

		// Wait for execution
		var runID string
		var temporalWorkflowID string
		s.Require().Eventually(func() bool {
			listResp := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{
				"type": "schedule",
			})

			for _, exec := range listResp.Executions {
				details := s.getWorkflowExecutionDetails(createdWorkflow.ID, exec.ID)
				// Match strictly on action to distinguish between the two runs
				if val, ok := details.Inputs["action"].(string); ok && val == item.expectedAction {
					runID = exec.ID
					temporalWorkflowID = exec.WorkflowID // Use the Temporal Workflow ID from the list response
					return true
				}
			}
			return false
		}, 10*time.Second, 1*time.Second, fmt.Sprintf("Scheduled workflow execution for action '%s' not found", item.expectedAction))

		s.T().Logf("Found execution for action '%s': RunID=%s", item.expectedAction, runID)

		// Wait for completion
		workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalWorkflowID, runID)
		var result string
		err = workflowExecution.Get(context.Background(), &result)
		s.Require().NoError(err, "Workflow execution failed")

		// Verify task output
		details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
		s.Require().NotEmpty(details.Tasks)
		taskOut := details.Tasks[0].Output.(map[string]any)
		s.Assert().Contains(taskOut["data"], fmt.Sprintf("Action is: %s", item.expectedAction))
		s.Assert().Contains(taskOut["data"], fmt.Sprintf("Mode is: %s", item.expectedMode))
	}

	// 3. Test Update & Cleanup
	s.T().Log("Testing update and schedule cleanup...")

	// Remove the second trigger (index 1)
	workflow.Definition.Triggers = []model.Trigger{
		workflow.Definition.Triggers[0],
	}
	workflow.ID = createdWorkflow.ID // Ensure ID is set for update

	updatedWorkflow, err := s.updateWorkflow(workflow)
	s.Require().NoError(err, "Failed to update workflow")
	s.Require().Len(updatedWorkflow.Definition.Triggers, 1)

	// Verify schedule 0 still exists
	schedule0 := fmt.Sprintf("workflow-schedule-%s-0", createdWorkflow.ID)
	_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), schedule0).Describe(context.Background())
	s.Require().NoError(err, "Schedule 0 should still exist")

	// Verify schedule 1 is gone
	schedule1 := fmt.Sprintf("workflow-schedule-%s-1", createdWorkflow.ID)
	s.Require().Eventually(func() bool {
		_, err = s.temporalClient.ScheduleClient().GetHandle(context.Background(), schedule1).Describe(context.Background())
		// We expect an error (NotFound)
		return err != nil
	}, 5*time.Second, 500*time.Millisecond, "Schedule 1 should be deleted")
}

// fireEventAndAwaitNewRun publishes an event through the consumer and waits for a fresh execution
// of the given workflow that wasn't already in seenRunIDs. Returns the new run ID.
func (s *IntegrationTestSuite) fireEventAndAwaitNewRun(workflowID string, payload map[string]any, seenRunIDs map[string]bool) string {
	eventData, err := json.Marshal(payload)
	s.Require().NoError(err, "Failed to marshal event payload")

	err = s.eventConsumer.ProcessMessage(eventData)
	s.Require().NoError(err, "Failed to process event message")

	var runID string
	s.Require().Eventually(func() bool {
		listResp := s.listWorkflowExecutions(workflowID)
		for _, exec := range listResp.Executions {
			if !seenRunIDs[exec.ID] {
				runID = exec.ID
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "Workflow execution should have started for payload %v", payload)
	return runID
}

func (s *IntegrationTestSuite) waitForRegistryMatch(eventType string, payload map[string]any, expectMatch bool) {
	s.Require().Eventually(func() bool {
		_ = s.eventRegistry.Refresh(context.Background())
		matches := s.eventRegistry.Match(eventType, testAccountID, payload)
		if expectMatch {
			return len(matches) > 0
		}
		return len(matches) == 0
	}, 5*time.Second, 500*time.Millisecond, "Event registry did not reach expected state for event_type=%s", eventType)
}

func (s *IntegrationTestSuite) TestEventTriggerWorkflow_ArrayEventType() {
	s.T().Log("Running TestEventTriggerWorkflow_ArrayEventType...")

	workflow := s.loadWorkflowFromFile("testdata/test-event-workflow-array.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create array event trigger workflow")
	s.T().Logf("Created array event trigger workflow with ID: %s", createdWorkflow.ID)

	// Both event types must register against the SAME workflow.
	for _, et := range []string{"alert.fired", "incident.opened"} {
		s.waitForRegistryMatch(et, map[string]any{"source": "integration-test-array", "account_id": testAccountID}, true)
	}

	seen := map[string]bool{}

	// 1. First event type fires the workflow.
	runID1 := s.fireEventAndAwaitNewRun(createdWorkflow.ID, map[string]any{
		"event_type": "alert.fired",
		"account_id": testAccountID,
		"source":     "integration-test-array",
	}, seen)
	seen[runID1] = true
	s.T().Logf("alert.fired triggered run ID: %s", runID1)

	// 2. Second event type fires the same workflow.
	runID2 := s.fireEventAndAwaitNewRun(createdWorkflow.ID, map[string]any{
		"event_type": "incident.opened",
		"account_id": testAccountID,
		"source":     "integration-test-array",
	}, seen)
	seen[runID2] = true
	s.T().Logf("incident.opened triggered run ID: %s", runID2)
	s.Require().NotEqual(runID1, runID2, "Each event should produce a distinct run")

	// 3. Mismatched source must NOT fire (filter rejects it).
	mismatchPayload, err := json.Marshal(map[string]any{
		"event_type": "alert.fired",
		"account_id": testAccountID,
		"source":     "some-other-source",
	})
	s.Require().NoError(err)
	err = s.eventConsumer.ProcessMessage(mismatchPayload)
	s.Require().NoError(err)

	// 4. Unrelated event type must NOT fire.
	unrelatedPayload, err := json.Marshal(map[string]any{
		"event_type": "deployment.success",
		"account_id": testAccountID,
		"source":     "integration-test-array",
	})
	s.Require().NoError(err)
	err = s.eventConsumer.ProcessMessage(unrelatedPayload)
	s.Require().NoError(err)

	// Give the system a moment in case a stray run would land — then assert exactly 2 executions.
	time.Sleep(2 * time.Second)
	listResp := s.listWorkflowExecutions(createdWorkflow.ID)
	s.Assert().Len(listResp.Executions, 2, "Only matching event_types with matching filter should have fired")

	// 5. Wait for both runs to complete successfully.
	for _, runID := range []string{runID1, runID2} {
		temporalID, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
		s.Require().NoError(err)
		var result string
		err = s.temporalClient.GetWorkflow(context.Background(), temporalID, runID).Get(context.Background(), &result)
		s.Require().NoError(err, "Run %s should complete successfully", runID)
	}
}

func (s *IntegrationTestSuite) TestEventTriggerWorkflow_WildcardFilterOnly() {
	s.T().Log("Running TestEventTriggerWorkflow_WildcardFilterOnly...")

	workflow := s.loadWorkflowFromFile("testdata/test-event-workflow-wildcard.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create wildcard event trigger workflow")
	s.T().Logf("Created wildcard event trigger workflow with ID: %s", createdWorkflow.ID)

	// The rule is registered in the wildcard bucket — Match should hit it for any event_type when the filter passes.
	s.Require().Eventually(func() bool {
		_ = s.eventRegistry.Refresh(context.Background())
		matches := s.eventRegistry.Match("anything.goes", testAccountID, map[string]any{
			"source":     "integration-test-wildcard",
			"account_id": testAccountID,
		})
		return len(matches) > 0
	}, 5*time.Second, 500*time.Millisecond, "Wildcard rule should match arbitrary event_type with passing filter")

	seen := map[string]bool{}

	// 1. Arbitrary event_type fires the workflow because the filter passes.
	runID1 := s.fireEventAndAwaitNewRun(createdWorkflow.ID, map[string]any{
		"event_type": "first.unknown.type",
		"account_id": testAccountID,
		"source":     "integration-test-wildcard",
	}, seen)
	seen[runID1] = true

	// 2. A different event_type also fires (still wildcard, still passing filter).
	runID2 := s.fireEventAndAwaitNewRun(createdWorkflow.ID, map[string]any{
		"event_type": "second.unknown.type",
		"account_id": testAccountID,
		"source":     "integration-test-wildcard",
	}, seen)
	seen[runID2] = true
	s.Require().NotEqual(runID1, runID2)

	// 3. Mismatched source must NOT fire (filter rejects, even though event_type bucket would accept anything).
	mismatchPayload, err := json.Marshal(map[string]any{
		"event_type": "first.unknown.type",
		"account_id": testAccountID,
		"source":     "wrong-source",
	})
	s.Require().NoError(err)
	err = s.eventConsumer.ProcessMessage(mismatchPayload)
	s.Require().NoError(err)

	// 4. Other tenant must NOT fire.
	otherTenantPayload, err := json.Marshal(map[string]any{
		"event_type": "first.unknown.type",
		"account_id": "some-other-account",
		"source":     "integration-test-wildcard",
	})
	s.Require().NoError(err)
	err = s.eventConsumer.ProcessMessage(otherTenantPayload)
	s.Require().NoError(err)

	time.Sleep(2 * time.Second)
	listResp := s.listWorkflowExecutions(createdWorkflow.ID)
	s.Assert().Len(listResp.Executions, 2, "Only events with matching filter and tenant should have fired")
}

func (s *IntegrationTestSuite) TestEventTriggerSaveRejection() {
	s.T().Log("Running TestEventTriggerSaveRejection...")

	cases := []struct {
		name           string
		nameSlug       string
		params         map[string]any
		expectErrToken string
	}{
		{
			name:           "no event_type and no filter",
			nameSlug:       "empty",
			params:         map[string]any{},
			expectErrToken: "event_trigger_needs_filter",
		},
		{
			name:           "empty event_type and whitespace filter",
			nameSlug:       "empty-and-ws",
			params:         map[string]any{"event_type": "", "filter": "   "},
			expectErrToken: "event_trigger_needs_filter",
		},
		{
			name:           "non-string array item",
			nameSlug:       "bad-arr-item",
			params:         map[string]any{"event_type": []any{"alert", 42}},
			expectErrToken: "event_type_invalid_item",
		},
		{
			name:           "wrong type for event_type",
			nameSlug:       "bad-type",
			params:         map[string]any{"event_type": 42, "filter": "{{ true }}"},
			expectErrToken: "event_type_invalid_type",
		},
		{
			name:           "unsupported param",
			nameSlug:       "bad-param",
			params:         map[string]any{"event_type": "alert", "unknown": true},
			expectErrToken: "unsupported_event_param",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			wf := model.Workflow{
				Name: "test-evt-rej-" + tc.nameSlug,
				Definition: model.WorkflowDefinition{
					Version: "v1",
					Triggers: []model.Trigger{
						{Type: model.WorkflowTriggerEvent, Params: tc.params},
					},
					Tasks: []model.Task{
						{ID: "noop", Type: "core.print", Params: map[string]any{"message": "hello"}},
					},
				},
			}

			s.deleteWorkflowByName(wf.Name, true)

			body, err := json.Marshal(wf)
			s.Require().NoError(err)

			req, err := http.NewRequest(http.MethodPost, apiBaseURL+"/workflows", bytes.NewBuffer(body))
			s.Require().NoError(err)
			s.addRequestHeaders(req)

			resp, err := http.DefaultClient.Do(req)
			s.Require().NoError(err)
			defer func() { _ = resp.Body.Close() }()

			respBody, _ := io.ReadAll(resp.Body)
			s.Assert().GreaterOrEqual(resp.StatusCode, 400, "expected 4xx for invalid event trigger; body: %s", string(respBody))
			s.Assert().Less(resp.StatusCode, 500, "expected client error, not server error; body: %s", string(respBody))
			s.Assert().Contains(string(respBody), tc.expectErrToken, "response should mention validation tag")
		})
	}
}
