package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestMultiTaskWorkflow() {
	s.T().Log("Running TestMultiTaskWorkflow...")

	// 1. Create a multi-task workflow
	workflow := s.loadWorkflowFromFile("testdata/test-multi-task-workflow.yaml")

	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create multi-task workflow")
	s.Require().NotEmpty(createdWorkflow.ID, "Created workflow ID should not be empty")
	s.T().Logf("Created multi-task workflow with ID: %s", createdWorkflow.ID)

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute multi-task workflow")
	s.Require().NotEmpty(runID, "Workflow run ID should not be empty")
	s.T().Logf("Executed multi-task workflow %s with Run ID: %s", createdWorkflow.ID, runID)

	// 3. Wait for workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Multi-task workflow execution failed or timed out")
	s.T().Logf("Multi-task workflow %s (Run ID: %s) completed with result: %s", createdWorkflow.ID, runID, result)

	// 4. Verify the outcome of the HTTP request
	// This requires fetching the workflow execution history and parsing activity results.
	// For now, we'll rely on the overall workflow completion and assume success.
	// A more robust check would involve inspecting the activity results from Temporal.
	s.T().Log("Verifying multi-task workflow outcome (simplified)...")
	// TODO: Implement detailed verification of activity results from Temporal history.
}

func (s *IntegrationTestSuite) TestConditionalTaskExecution() {
	s.T().Log("Running TestConditionalTaskExecution...")

	// --- Test Case 1: Condition is TRUE ---
	s.T().Log("Testing with condition: TRUE")
	workflowTrue := s.loadWorkflowFromFile("testdata/test-conditional-true-workflow.yaml")

	createdWorkflowTrue, _, err := s.createAndActivateWorkflow(workflowTrue)
	s.Require().NoError(err, "Failed to create workflow for true condition")

	runIDTrue, err := s.executeWorkflow(createdWorkflowTrue.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow for true condition")

	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflowTrue.ID, runIDTrue)
	s.Require().NoError(err)
	workflowExecutionTrue := s.temporalClient.GetWorkflow(context.Background(), temporalId, runIDTrue)
	var resultTrue string
	err = workflowExecutionTrue.Get(context.Background(), &resultTrue)
	s.Require().NoError(err, "Workflow (true condition) execution failed or timed out")
	s.T().Logf("Workflow (true condition) completed with result: %s", resultTrue)

	// Verify history for true condition
	historyIteratorTrue := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runIDTrue, false, 0)
	executedTaskIfTrue := false
	executedTaskIfFalse := false
	for historyIteratorTrue.HasNext() {
		event, err := historyIteratorTrue.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if script, ok := p["message"].(string); ok {
					if script == "Task if true executed" {
						executedTaskIfTrue = true
					}
					if script == "Task if false executed" {
						executedTaskIfFalse = true
					}
				}
			}
		}
	}
	s.Assert().True(executedTaskIfTrue, "Task 'task-if-true' should have executed when condition is true")
	s.Assert().False(executedTaskIfFalse, "Task 'task-if-false' should NOT have executed when condition is true")

	// --- Test Case 2: Condition is FALSE ---
	s.T().Log("Testing with condition: FALSE")
	workflowFalse := s.loadWorkflowFromFile("testdata/test-conditional-false-workflow.yaml")

	createdWorkflowFalse, _, err := s.createAndActivateWorkflow(workflowFalse)
	s.Require().NoError(err, "Failed to create workflow for false condition")

	runIDFalse, err := s.executeWorkflow(createdWorkflowFalse.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow for false condition")

	temporalId, err = s.resolveTemporalWorkflowID(context.Background(), createdWorkflowFalse.ID, runIDFalse)
	s.Require().NoError(err)
	workflowExecutionFalse := s.temporalClient.GetWorkflow(context.Background(), temporalId, runIDFalse)
	var resultFalse string
	err = workflowExecutionFalse.Get(context.Background(), &resultFalse)
	s.Require().NoError(err, "Workflow (false condition) execution failed or timed out")
	s.T().Logf("Workflow (false condition) completed with result: %s", resultFalse)

	// Verify history for false condition
	historyIteratorFalse := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runIDFalse, false, 0)
	executedTaskIfTrue = false
	executedTaskIfFalse = false
	for historyIteratorFalse.HasNext() {
		event, err := historyIteratorFalse.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if script, ok := p["message"].(string); ok {
					if script == "Task if true executed" {
						executedTaskIfTrue = true
					}
					if script == "Task if false executed" {
						executedTaskIfFalse = true
					}
				}
			}
		}
	}
	s.Assert().False(executedTaskIfTrue, "Task 'task-if-true' should NOT have executed when condition is false")
	s.Assert().True(executedTaskIfFalse, "Task 'task-if-false' should have executed when condition is false")
}

func (s *IntegrationTestSuite) TestTimeoutWorkflow() {
	s.T().Log("Running TestTimeoutWorkflow...")

	// 1. Create a workflow with a timeout
	workflow := s.loadWorkflowFromFile("testdata/test-timeout-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create timeout workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute timeout workflow")

	// 3. Wait for the workflow to complete (it should fail with a timeout)
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().Error(err, "Expected workflow execution to fail with a timeout")

	// 4. Verify that the workflow failed with a timeout error
	s.Contains(err.Error(), "timeout", "Expected the error message to contain 'timeout'")
}

func (s *IntegrationTestSuite) TestRetryWorkflow() {
	s.T().Log("Running TestRetryWorkflow...")

	// 1. Create a workflow with a retry policy
	workflow := s.loadWorkflowFromFile("testdata/test-retry-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create retry workflow")

	// remove temp file
	if err := os.Remove("/tmp/retry_attempt_count"); err != nil {
		s.T().Logf("No temp file to remove: %v", err)
	}

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute retry workflow")

	// 3. Wait for the workflow to complete (it should succeed after retries)
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Retry workflow should have eventually succeeded")

	// 4. Verify that the task was attempted 3 times
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	var finalAttemptCount int32
	retryTaskScheduledEventID := int64(0)

	// First pass: Find the scheduled event ID for "retry-task"
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if attrs := event.GetActivityTaskScheduledEventAttributes(); attrs != nil {
			if attrs.GetActivityType().GetName() == "scripting.run_script" && attrs.GetActivityId() == "retry-task" {
				retryTaskScheduledEventID = event.GetEventId()
				break // Found it, no need to continue this loop
			}
		}
	}

	// Reset iterator to go through history again
	historyIterator = s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)

	// Second pass: Find the ActivityTaskStartedEventAttributes corresponding to the retryTaskScheduledEventID
	// and get its attempt number. This will be the final attempt number.
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if attrs := event.GetActivityTaskStartedEventAttributes(); attrs != nil {
			if attrs.GetScheduledEventId() == retryTaskScheduledEventID {
				finalAttemptCount = attrs.GetAttempt()
			}
		}
	}
	s.Equal(int32(3), finalAttemptCount, "Expected the 'retry-task' to be attempted 3 times")
}

func (s *IntegrationTestSuite) TestMatrixWorkflow() {
	s.T().Log("Running TestMatrixWorkflow...")

	// 1. Create a workflow with a matrix task
	workflow := s.loadWorkflowFromFile("testdata/test-matrix-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create matrix workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute matrix workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Matrix workflow execution failed or timed out")

	// 4. Verify that the task was executed for each item in the matrix
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	executedScripts := make(map[string]bool)
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if script, ok := p["message"].(string); ok {
					executedScripts[script] = true
				}
			}
		}
	}

	expectedScripts := []string{"apple", "banana", "cherry"}
	s.Equal(len(expectedScripts), len(executedScripts), "The number of executed scripts should match the matrix size")
	for _, script := range expectedScripts {
		s.True(executedScripts[script], "Expected script '%s' to have been executed", script)
	}
}

func (s *IntegrationTestSuite) TestWorkflowOutput() {
	s.T().Log("Running TestWorkflowOutput...")

	// 1. Create a workflow that produces a specific output
	workflow := s.loadWorkflowFromFile("testdata/test-workflow-output.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow for output test")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow for output test")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	resultStr := ""
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "Workflow execution for output test failed or timed out")
	result := map[string]any{}
	err = json.Unmarshal([]byte(resultStr), &result)
	s.Assert().Nil(err)
	s.Assert().Equal("This is the expected output", result["final_message"], "The final workflow output did not match the expected value")
}

func (s *IntegrationTestSuite) TestFailureHandlingWorkflow() {
	s.T().Log("Running TestFailureHandlingWorkflow...")

	// 1. Create a workflow designed to fail
	workflow := s.loadWorkflowFromFile("testdata/test-failure-handling-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create failure handling workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute failure handling workflow")

	// 3. Wait for the workflow to complete (it should fail)
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().Error(err, "Expected workflow execution to fail")

	// 4. Verify that the on_failure task was executed
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	var failureHandlerExecuted bool
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if script, ok := p["message"].(string); ok {
					if script == "The task failed as expected!" {
						failureHandlerExecuted = true
					}
				}
			}
		}
	}
	s.Assert().True(failureHandlerExecuted, "The on_failure handler task should have been executed")
}

func (s *IntegrationTestSuite) TestApprovalWorkflow() {
	s.T().Log("Running TestApprovalWorkflow...")

	// 1. Create a workflow with an approval task
	workflow := s.loadWorkflowFromFile("testdata/test-approval-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create approval workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute approval workflow")

	// 3. Poll until the approval task is active and get its token via query
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	var taskToken string
	s.Require().Eventually(func() bool {
		// Check if the activity is pending
		desc, err := s.temporalClient.DescribeWorkflowExecution(context.Background(), temporalId, runID)
		s.Require().NoError(err)

		for _, activity := range desc.PendingActivities {
			if activity.ActivityType.Name == "core.approval" {
				// Activity is pending, now query for the token
				queryResult, err := s.temporalClient.QueryWorkflow(context.Background(), temporalId, runID, "getApprovalToken", "wait-for-approval")
				s.Require().NoError(err)

				var token string
				err = queryResult.Get(&token)
				s.Require().NoError(err)

				if token != "" {
					taskToken = token
					return true
				}
			}
		}
		return false
	}, 30*time.Second, 1*time.Second, "Failed to get approval token from workflow query")

	s.T().Logf("Retrieved approval task token: %s", taskToken)

	// 4. Approve the task via the API
	approvalBody := map[string]any{
		"status": "approved",
		"result": "Approved by integration test",
	}
	body, err := json.Marshal(approvalBody)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/approvals/%s", apiBaseURL, taskToken), bytes.NewBuffer(body))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		err := resp.Body.Close()
		s.Require().NoError(err, "Failed to close response body")
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for approval request")

	// 5. Wait for the workflow to complete
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Approval workflow should have completed successfully")

	// 6. Verify that the final task was executed
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Require().Len(executionDetails.Tasks, 3, "Should have three tasks in the execution history")

	var step2Task model.TaskExecutionDetails
	for _, task := range executionDetails.Tasks {
		if task.ID == "step-2" {
			step2Task = task
			break
		}
	}
	s.Assert().NotNil(step2Task, "Task 'step-2' should be present in the execution details")
	s.Assert().Equal(model.TaskStatusCompleted, step2Task.Status)

	// --- Part 2: Test Rejection Flow ---
	s.T().Log("Testing Rejection Flow...")

	// 1. Execute the workflow again
	runIDReject, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute approval workflow for rejection")

	// 2. Poll until the approval task is active
	temporalIdReject, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runIDReject)
	s.Require().NoError(err)
	var taskTokenReject string
	s.Require().Eventually(func() bool {
		desc, err := s.temporalClient.DescribeWorkflowExecution(context.Background(), temporalIdReject, runIDReject)
		s.Require().NoError(err)
		for _, activity := range desc.PendingActivities {
			if activity.ActivityType.Name == "core.approval" {
				queryResult, err := s.temporalClient.QueryWorkflow(context.Background(), temporalIdReject, runIDReject, "getApprovalToken", "wait-for-approval")
				s.Require().NoError(err)
				var token string
				err = queryResult.Get(&token)
				s.Require().NoError(err)
				if token != "" {
					taskTokenReject = token
					return true
				}
			}
		}
		return false
	}, 30*time.Second, 1*time.Second, "Failed to get approval token for rejection")

	// 3. Reject the task via the API
	rejectBody := map[string]any{
		"status": "rejected",
		"result": "Rejected by integration test",
	}
	bodyReject, err := json.Marshal(rejectBody)
	s.Require().NoError(err)

	reqReject, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/approvals/%s", apiBaseURL, taskTokenReject), bytes.NewBuffer(bodyReject))
	s.Require().NoError(err)
	reqReject.Header.Set("Content-Type", "application/json")

	respReject, err := http.DefaultClient.Do(reqReject)
	s.Require().NoError(err)
	defer func() { _ = respReject.Body.Close() }()
	s.Assert().Equal(http.StatusOK, respReject.StatusCode, "Expected 200 OK for rejection request")

	// 4. Wait for workflow completion
	workflowExecutionReject := s.temporalClient.GetWorkflow(context.Background(), temporalIdReject, runIDReject)
	var resultReject string
	err = workflowExecutionReject.Get(context.Background(), &resultReject)
	s.Require().NoError(err, "Rejection workflow should have completed successfully")

	// 5. Verify that step-2 was SKIPPED or Not Executed
	executionDetailsReject := s.getWorkflowExecutionDetails(createdWorkflow.ID, runIDReject)
	var step2TaskReject *model.TaskExecutionDetails
	for _, task := range executionDetailsReject.Tasks {
		if task.ID == "step-2" {
			t := task
			step2TaskReject = &t
			break
		}
	}

	if step2TaskReject != nil {
		s.Assert().Equal(model.TaskStatusSkipped, step2TaskReject.Status, "Task 'step-2' should be SKIPPED if present")
	} else {
		// If task is missing, it implies it wasn't scheduled/started, which is expected for skipped tasks in current implementation
		s.T().Log("Task 'step-2' was not found in execution history, implying it was skipped/not executed.")
	}
}

func (s *IntegrationTestSuite) TestApprovalIMWorkflow() {
	s.T().Log("Running TestApprovalIMWorkflow...")

	// 1. Create a workflow with an approval task using IM params
	workflow := s.loadWorkflowFromFile("testdata/test-approval-im-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create approval IM workflow")

	imChannel := os.Getenv("TEST_NOTIFICATION_SLACK_CHANNEL_ID")
	if imChannel == "" {
		imChannel = "C12345678"
	}

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"im_channel": imChannel})
	s.Require().NoError(err, "Failed to execute approval IM workflow")

	// 3. Poll until the approval task is active and get its token via query
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	var taskToken string
	s.Require().Eventually(func() bool {
		// Check if the activity is pending
		desc, err := s.temporalClient.DescribeWorkflowExecution(context.Background(), temporalId, runID)
		s.Require().NoError(err)

		for _, activity := range desc.PendingActivities {
			if activity.ActivityType.Name == "core.approval" {
				// Activity is pending, now query for the token
				queryResult, err := s.temporalClient.QueryWorkflow(context.Background(), temporalId, runID, "getApprovalToken", "im-wait-for-approval")
				s.Require().NoError(err)

				var token string
				err = queryResult.Get(&token)
				s.Require().NoError(err)

				if token != "" {
					taskToken = token
					return true
				}
			}
		}
		return false
	}, 30*time.Second, 1*time.Second, "Failed to get approval token from workflow query")

	s.T().Logf("Retrieved approval task token: %s", taskToken)

	// 4. Approve the task via the API
	approvalBody := map[string]any{
		"status": "approved",
		"result": "Approved via IM params test",
	}
	body, err := json.Marshal(approvalBody)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/approvals/%s", apiBaseURL, taskToken), bytes.NewBuffer(body))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		err := resp.Body.Close()
		s.Require().NoError(err, "Failed to close response body")
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for approval request")

	// 5. Wait for the workflow to complete
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Approval workflow should have completed successfully")
}

func (s *IntegrationTestSuite) TestTransformWorkflow() {
	s.T().Log("Running TestTransformWorkflow...")

	// 1. Create a workflow with a transform task
	workflow := s.loadWorkflowFromFile("testdata/test-transform-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create transform workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute transform workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Transform workflow should have completed successfully")

	// 4. Verify that the final task has the correct output
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Require().Len(executionDetails.Tasks, 3, "Should have three tasks in the execution history")

	var finalStepTask model.TaskExecutionDetails
	for _, task := range executionDetails.Tasks {
		if task.ID == "finalstep" {
			finalStepTask = task
			break
		}
	}
	s.Assert().NotNil(finalStepTask, "Task 'final-step' should be present in the execution details")
	s.Assert().Equal(model.TaskStatusCompleted, finalStepTask.Status)
	s.Assert().Contains(finalStepTask.Output.(map[string]any)["data"], "The admin is: Alice", "The final step should have the correct output")
}

func (s *IntegrationTestSuite) TestVarsContextWorkflow() {

	s.T().Log("Running TestVarsContextWorkflow...")
	// 1. Create the workflow with inputs and task outputs
	workflow := s.loadWorkflowFromFile("testdata/test-vars-context-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create vars context workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute vars context workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "Vars context workflow execution failed or timed out")

	// 4. Verify the final output
	result := map[string]any{}
	err = json.Unmarshal([]byte(resultStr), &result)
	s.Require().NoError(err, "Failed to unmarshal workflow result")

	// The expected output from the final task is "Hello World"
	s.Assert().Equal("Hello World", result["final_result"], "The final workflow output did not match the expected value")
}

func (s *IntegrationTestSuite) TestPersistentStateWorkflow() {
	s.T().Log("Running TestPersistentStateWorkflow...")

	// 1. Create the workflow
	workflow := s.loadWorkflowFromFile("testdata/test-persistent-state-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create persistent state workflow")
	s.T().Logf("Created persistent state workflow with ID: %s", createdWorkflow.ID)

	// 2. First Execution: Should initialize counter to 1
	inputs1 := map[string]any{
		"dynamic_key_suffix": "A",
	}
	runID1, err := s.executeWorkflow(createdWorkflow.ID, inputs1)
	s.Require().NoError(err, "Failed to execute workflow run 1")

	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID1)
	s.Require().NoError(err)
	workflowExecution1 := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID1)
	var resultStr1 string
	err = workflowExecution1.Get(context.Background(), &resultStr1)
	s.Require().NoError(err, "Workflow execution 1 failed")
	s.T().Logf("Run 1 Result: %s", resultStr1)

	var result1 map[string]any
	s.Require().NoError(json.Unmarshal([]byte(resultStr1), &result1), "Failed to unmarshal run 1 result")
	s.Assert().Equal("1", result1["current_counter"], "Run 1 should result in counter = 1")
	s.Assert().Equal("temporary", result1["ephemeral_val"], "Run 1 should have ephemeral val")
	s.Assert().Equal("value_for_suffix_A", result1["dynamic_state_value"], "Run 1 should result in dynamic_state_value = value_for_suffix_A")

	// Verify state via API
	stateItems := s.getWorkflowState(createdWorkflow.ID)
	stateMap := make(map[string]any)
	for _, item := range stateItems {
		stateMap[item.Key] = item.Value
	}

	s.Assert().Equal("1", stateMap["counter"], "API should return correct counter state")
	s.Assert().Equal("temporary", stateMap["ephemeral_data"], "API should return correct ephemeral_data state")
	s.Assert().Equal("value_for_suffix_A", stateMap["my_dynamic_state_A"], "API should return correct dynamic state for A")

	// 3. Second Execution: Should read counter (1) and increment to 2
	inputs2 := map[string]any{
		"dynamic_key_suffix": "B",
	}
	runID2, err := s.executeWorkflow(createdWorkflow.ID, inputs2)
	s.Require().NoError(err, "Failed to execute workflow run 2")

	temporalId2, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID2)
	s.Require().NoError(err)
	workflowExecution2 := s.temporalClient.GetWorkflow(context.Background(), temporalId2, runID2)
	var resultStr2 string
	err = workflowExecution2.Get(context.Background(), &resultStr2)
	s.Require().NoError(err, "Workflow execution 2 failed")
	s.T().Logf("Run 2 Result: %s", resultStr2)

	var result2 map[string]any
	s.Require().NoError(json.Unmarshal([]byte(resultStr2), &result2), "Failed to unmarshal run 2 result")
	s.Assert().Equal("2", result2["current_counter"], "Run 2 should result in counter = 2")
	s.Assert().Equal("value_for_suffix_B", result2["dynamic_state_value"], "Run 2 should result in dynamic_state_value = value_for_suffix_B")

	// Verify state again to ensure both dynamic keys exist
	stateItems2 := s.getWorkflowState(createdWorkflow.ID)
	stateMap2 := make(map[string]any)
	for _, item := range stateItems2 {
		stateMap2[item.Key] = item.Value
	}
	s.Assert().Equal("value_for_suffix_A", stateMap2["my_dynamic_state_A"], "API should still have dynamic state for A")
	s.Assert().Equal("value_for_suffix_B", stateMap2["my_dynamic_state_B"], "API should return correct dynamic state for B")
}

func (s *IntegrationTestSuite) TestPolymorphicVars() {
	s.T().Log("Running TestPolymorphicVars...")

	workflow := s.loadWorkflowFromFile("testdata/test-polymorphic-vars.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create polymorphic vars workflow")

	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow")

	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "Workflow execution failed")

	var result map[string]any
	s.Require().NoError(json.Unmarshal([]byte(resultStr), &result), "Failed to unmarshal result")

	s.Assert().Equal("simple_value", result["out_simple"])
	s.Assert().Equal("complex_value", result["out_complex"])
}

func (s *IntegrationTestSuite) TestPolymorphicState() {
	s.T().Log("Running TestPolymorphicState...")

	// 1. Create the workflow
	workflow := s.loadWorkflowFromFile("testdata/test-polymorphic-state.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create polymorphic state workflow")
	s.T().Logf("Created polymorphic state workflow with ID: %s", createdWorkflow.ID)

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow run")

	// 3. Wait for workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	// We don't care about workflow result here, just that it completes
	var workflowResult string
	err = workflowExecution.Get(context.Background(), &workflowResult)
	s.Require().NoError(err, "Workflow execution failed")

	// 4. Verify the state via API
	stateItems := s.getWorkflowState(createdWorkflow.ID)
	s.Require().Len(stateItems, 2, "Should have 2 state items")

	stateMap := make(map[string]model.WorkflowStateItem)
	for _, item := range stateItems {
		stateMap[item.Key] = item
	}

	s.Require().Contains(stateMap, "my_polymorphic_key")
	s.Require().Contains(stateMap, "another_polymorphic_key")

	// Verify my_polymorphic_key
	myPolyKey := stateMap["my_polymorphic_key"]
	s.Assert().Equal("data_to_store", myPolyKey.Value, "my_polymorphic_key should have correct value")
	s.Assert().NotNil(myPolyKey.ExpiresAt, "my_polymorphic_key should have expires_at set")
	s.Assert().Equal(runID, myPolyKey.LastUpdatedByExecutionID, "my_polymorphic_key should have correct execution ID")
	s.Assert().Equal("set_polymorphic_state", myPolyKey.LastUpdatedByTaskID, "my_polymorphic_key should have correct task ID")

	// Verify another_polymorphic_key (static data)
	anotherPolyKey := stateMap["another_polymorphic_key"]
	s.Assert().Equal("static_data", anotherPolyKey.Value, "another_polymorphic_key should have correct value")
	s.Assert().Nil(anotherPolyKey.ExpiresAt, "another_polymorphic_key should NOT have expires_at set")
	s.Assert().Equal(runID, anotherPolyKey.LastUpdatedByExecutionID, "another_polymorphic_key should have correct execution ID")
	s.Assert().Equal("set_polymorphic_state", anotherPolyKey.LastUpdatedByTaskID, "another_polymorphic_key should have correct task ID")
}

func (s *IntegrationTestSuite) TestDynamicMatrixWorkflow() {
	s.T().Log("Running TestDynamicMatrixWorkflow...")

	// 1. Create a workflow with a matrix task using dynamic input
	workflow := s.loadWorkflowFromFile("testdata/test-matrix-input-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create dynamic matrix workflow")

	// Define inputs
	inputs := map[string]any{
		"fruits": []any{"mango", "papaya", "kiwi"},
	}

	// 2. Execute the workflow with inputs
	runID, err := s.executeWorkflow(createdWorkflow.ID, inputs)
	s.Require().NoError(err, "Failed to execute dynamic matrix workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Dynamic matrix workflow execution failed or timed out")

	// 4. Verify that the task was executed for each item in the matrix
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	executedScripts := make(map[string]bool)
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if message, ok := p["message"].(string); ok {
					executedScripts[message] = true
				}
			}
		}
	}

	expectedScripts := []string{"echo mango", "echo papaya", "echo kiwi"}
	s.Equal(len(expectedScripts), len(executedScripts), "The number of executed scripts should match the matrix size")
	for _, script := range expectedScripts {
		s.True(executedScripts[script], "Expected script '%s' to have been executed", script)
	}
}

func (s *IntegrationTestSuite) TestWorkflowExecutionWithTags() {
	s.T().Log("Running TestWorkflowExecutionWithTags...")

	// 1. Create a simple workflow
	workflow := s.loadWorkflowFromFile("testdata/test-manual-workflow.yaml")

	// 2. Add custom/dynamic tags
	// These keys would FAIL if used as direct search attributes without registration (if strict validation is on, or if we exhaust limit).
	// But mostly, if we use them as keys, Temporal requires them to be registered.
	// With the fix, they should be stored in nb_execution_tags as "key:value" strings.
	workflow.Tags = map[string]any{
		"env":         "production",
		"team":        "devops",
		"cost_center": "12345",
	}

	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow with tags")
	s.Require().NotEmpty(createdWorkflow.ID, "Created workflow ID should not be empty")

	// Cleanup
	defer s.deleteWorkflow(createdWorkflow.ID, false)

	// 3. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow")
	s.Require().NotEmpty(runID, "Workflow run ID should not be empty")

	// 4. Wait for completion
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)

	// Wait for workflow to complete
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Workflow execution failed or timed out. This might indicate dynamic tags caused an error.")

	// 5. Verify we can find the execution using the tag filter
	// This confirms the tags were correctly upserted into nb_execution_tags
	s.Require().Eventually(func() bool {
		// Filter by "env:production"
		// The listWorkflowExecutionsWithFilters helper (in workflow_api_test.go context) constructs the query
		// using model.SearchAttrExecutionTags='key:value' logic, which matches our fix.
		listResp := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{
			"env": "production",
		})
		for _, exec := range listResp.Executions {
			if exec.ID == runID {
				return true
			}
		}
		return false
	}, 10*time.Second, 1*time.Second, "Failed to find workflow execution by tag 'env:production'")

	s.Require().Eventually(func() bool {
		// Filter by "cost_center:12345"
		listResp := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{
			"cost_center": "12345",
		})
		for _, exec := range listResp.Executions {
			if exec.ID == runID {
				return true
			}
		}
		return false
	}, 10*time.Second, 1*time.Second, "Failed to find workflow execution by tag 'cost_center:12345'")
}

func (s *IntegrationTestSuite) TestMatrixOutputWorkflow() {
	s.T().Log("Running TestMatrixOutputWorkflow...")

	// 1. Create a workflow with a matrix task using output from a previous task
	workflow := s.loadWorkflowFromFile("testdata/test-matrix-output-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create matrix output workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute matrix output workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Matrix output workflow execution failed or timed out")

	// 4. Verify that the task was executed for each item in the matrix
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	executedScripts := make(map[string]bool)
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if message, ok := p["message"].(string); ok {
					if strings.HasPrefix(message, "Expanded: ") {
						executedScripts[message] = true
					}
				}
			}
		}
	}

	expectedScripts := []string{"Expanded: apple", "Expanded: banana", "Expanded: cherry"}
	s.Equal(len(expectedScripts), len(executedScripts), "The number of executed scripts should match the matrix size")
	for _, script := range expectedScripts {
		s.True(executedScripts[script], "Expected script '%s' to have been executed", script)
	}
}

func (s *IntegrationTestSuite) TestConditionalOutputWorkflow() {
	s.T().Log("Running TestConditionalOutputWorkflow...")

	// 1. Create a workflow with a conditional task using output from a previous task
	workflow := s.loadWorkflowFromFile("testdata/test-conditional-output-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create conditional output workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute conditional output workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Conditional output workflow execution failed or timed out")

	// 4. Verify that the conditional task was executed
	historyIterator := s.temporalClient.GetWorkflowHistory(context.Background(), temporalId, runID, false, 0)
	taskExecuted := false
	for historyIterator.HasNext() {
		event, err := historyIterator.Next()
		s.Require().NoError(err)
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			activityType := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
			if activityType == "core.print" {
				params := event.GetActivityTaskScheduledEventAttributes().GetInput().GetPayloads()[0].GetData()
				var p map[string]any
				s.Require().NoError(json.Unmarshal(params, &p))
				if message, ok := p["message"].(string); ok {
					if message == "Conditional task executed" {
						taskExecuted = true
					}
				}
			}
		}
	}

	s.True(taskExecuted, "Expected conditional task to have been executed")
}
