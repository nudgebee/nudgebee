package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestListWorkflows() {
	s.T().Log("Running TestListWorkflows...")

	// Create a dummy workflow to ensure there's something to list
	workflow := s.loadWorkflowFromFile("testdata/test-list-workflow.yaml")
	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Assert().Nil(err, "unable to create workflow")

	listResponse := s.listWorkflows()

	found := false
	for _, wf := range listResponse.Workflows {
		if wf.ID == createdWorkflow.ID {
			found = true
			break
		}
	}
	s.Assert().True(found, "Created workflow should be present in the list")
}

func (s *IntegrationTestSuite) TestListWorkflowsByName() {
	s.T().Log("Running TestListWorkflowsByName...")

	// Create a workflow with a specific name
	workflow := s.loadWorkflowFromFile("testdata/test-list-workflow.yaml")
	workflow.Name = "name-search-test-workflow"
	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Assert().Nil(err, "unable to create workflow")

	// Search by exact name
	nameList := s.listWorkflows(map[string]string{"name": "filter-test"})

	found := false
	for _, wf := range nameList.Workflows {
		if wf.ID == createdWorkflow.ID {
			found = true
			s.Assert().Equal("name-search-test-workflow", wf.Name, "Workflow name should match")
			break
		}
	}
	s.Assert().True(found, "Workflow with specific name should be found")
}

func (s *IntegrationTestSuite) TestListWorkflowsWithFilters() {
	s.T().Log("Running TestListWorkflowsWithFilters...")

	// 1. Create a standard manual workflow (Active by default)
	manualWf := s.loadWorkflowFromFile("testdata/test-list-workflow.yaml")
	manualWf.Name = "filter-test-manual"
	createdManualWf, _, err := s.createAndActivateWorkflow(manualWf)
	s.Assert().Nil(err, "unable to create manual workflow")

	// 2. Create a scheduled workflow (Active by default, has schedule trigger)
	scheduledWf := s.loadWorkflowFromFile("testdata/test-scheduled-workflow.yaml")
	scheduledWf.Name = "filter-test-scheduled"
	createdScheduledWf, _, err := s.createAndActivateWorkflow(scheduledWf)
	s.Assert().Nil(err, "unable to create scheduled workflow")

	// 3. Pause the scheduled workflow to change its status
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/pause", apiBaseURL, createdScheduledWf.ID), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.T().Logf("Error closing response body: %v", cerr)
		}
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	// Test Filter: Status = ACTIVE
	s.T().Log("Filtering by Status=ACTIVE")
	activeList := s.listWorkflows(map[string]string{"status": string(model.WorkflowStatusActive)})
	foundManual := false
	foundScheduled := false
	for _, wf := range activeList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
		if wf.ID == createdScheduledWf.ID {
			foundScheduled = true
		}
	}
	s.Assert().True(foundManual, "Active manual workflow should be present when filtering by ACTIVE")
	s.Assert().False(foundScheduled, "Paused scheduled workflow should NOT be present when filtering by ACTIVE")

	// Test Filter: Status = PAUSED
	s.T().Log("Filtering by Status=PAUSED")
	pausedList := s.listWorkflows(map[string]string{"status": string(model.WorkflowStatusPaused)})
	foundManual = false
	foundScheduled = false
	for _, wf := range pausedList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
		if wf.ID == createdScheduledWf.ID {
			foundScheduled = true
		}
	}
	s.Assert().False(foundManual, "Active manual workflow should NOT be present when filtering by PAUSED")
	s.Assert().True(foundScheduled, "Paused scheduled workflow should be present when filtering by PAUSED")

	// Test Filter: Trigger Type = schedule
	s.T().Log("Filtering by Trigger Type=schedule")
	scheduleList := s.listWorkflows(map[string]string{"type": "schedule"})
	foundManual = false
	foundScheduled = false
	for _, wf := range scheduleList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
		if wf.ID == createdScheduledWf.ID {
			foundScheduled = true
		}
	}
	s.Assert().False(foundManual, "Manual workflow should NOT be present when filtering by 'schedule' type")
	s.Assert().True(foundScheduled, "Scheduled workflow should be present when filtering by 'schedule' type")

	// Test Filter: Name (Case Insensitive Partial Match)
	s.T().Log("Filtering by Name (Partial Case-Insensitive)")
	// manualWf.Name is "filter-test-manual". Let's search for "TEST-MANUAL".
	nameList := s.listWorkflows(map[string]string{"name": "TEST-MANUAL"})
	foundManual = false
	for _, wf := range nameList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
	}
	s.Assert().True(foundManual, "Manual workflow should be present when filtering by partial case-insensitive name 'TEST-MANUAL'")

	// Test Filter: Tags (key:value format)
	s.T().Log("Filtering by Tags key:value (env:production)")
	tagList := s.listWorkflows(map[string]string{"tags": "env:production"})
	foundManual = false
	for _, wf := range tagList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
	}
	s.Assert().True(foundManual, "Manual workflow with tag env:production should be present when filtering by that tag")

	// Test Filter: Tags (simple string, no colon)
	s.T().Log("Filtering by simple string tag (backend)")
	simpleTagList := s.listWorkflows(map[string]string{"tags": "backend"})
	foundManual = false
	for _, wf := range simpleTagList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
	}
	s.Assert().True(foundManual, "Manual workflow with tag team:backend should be found when filtering by simple string 'backend'")

	// Test Filter: Tags (non-matching tag should exclude)
	s.T().Log("Filtering by non-matching tag")
	noMatchTagList := s.listWorkflows(map[string]string{"tags": "env:staging"})
	foundManual = false
	for _, wf := range noMatchTagList.Workflows {
		if wf.ID == createdManualWf.ID {
			foundManual = true
		}
	}
	s.Assert().False(foundManual, "Manual workflow should NOT be present when filtering by non-matching tag env:staging")

	// Test Filter: Limit (Testing our improved DAO totalCount logic)
	s.T().Log("Testing Limit filter and totalCount behavior")

	// First get all workflows without limit to know the total count
	allWorkflowsList := s.listWorkflows()
	totalWorkflows := len(allWorkflowsList.Workflows)
	s.T().Logf("Total workflows in system: %d", totalWorkflows)

	// Test with limit = 1 (should return 1 workflow but totalCount should be unchanged)
	limitedList := s.listWorkflows(map[string]string{"limit": "1"})
	s.Assert().LessOrEqual(len(limitedList.Workflows), 1, "Should return at most 1 workflow when limit=1")

	// IMPORTANT: Test our improved DAO logic - totalCount should be same as total workflows
	// even when limit is applied (this tests our fix to the List function)
	if totalWorkflows > 1 {
		s.Assert().Equal(totalWorkflows, limitedList.TotalCount,
			"TotalCount should equal total workflows even with limit applied")
		s.Assert().Greater(limitedList.TotalCount, len(limitedList.Workflows),
			"TotalCount should be greater than returned workflows when limit < total")
	}

	// Test that our created workflows are in the system
	if totalWorkflows > 0 {
		s.Assert().Greater(len(allWorkflowsList.Workflows), len(limitedList.Workflows),
			"Limited list should be smaller than full list when limit < total")
	}

	// Test with limit larger than total workflows
	largeLimit := totalWorkflows + 10
	largeLimitList := s.listWorkflows(map[string]string{"limit": fmt.Sprintf("%d", largeLimit)})
	s.Assert().Equal(totalWorkflows, len(largeLimitList.Workflows),
		"Should return all workflows when limit is larger than total")
	s.Assert().Equal(totalWorkflows, largeLimitList.TotalCount,
		"TotalCount should equal total workflows when limit >= total")
}

func (s *IntegrationTestSuite) TestListWorkflowExecutionsWithFilters() {
	s.T().Log("Running TestListWorkflowExecutionsWithFilters...")

	// 1. Create a workflow
	workflow := s.loadWorkflowFromFile("testdata/test-long-running-workflow.yaml")
	workflow.Name = "exec-filter-test"
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Assert().Nil(err, "unable to create workflow")

	// 2. Execute the workflow manually (Run 1 - Fast)
	runID1, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"mode": "print"})
	s.Assert().Nil(err, "unable to execute workflow 1")

	// 3. Execute the workflow manually (Run 2 - Wait/Cancel)
	runID2, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"mode": "sleep"})
	s.Assert().Nil(err, "unable to execute workflow 2")

	// 4. Cancel runID2 immediately
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/runs/%s/cancel", apiBaseURL, createdWorkflow.ID, runID2), nil)
	s.Require().NoError(err)
	s.addRequestHeaders(req)
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.T().Logf("Error closing response body: %v", cerr)
		}
	}()
	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	// 5. Wait for Run 2 to be CANCELED
	s.Require().Eventually(func() bool {
		details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID2)
		return details.Status == model.WorkflowExecutionStatusCanceled
	}, 10*time.Second, 1*time.Second, "Execution 2 should be canceled")

	// 6. Wait for Run 1 to be COMPLETED
	s.Require().Eventually(func() bool {
		details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID1)
		return details.Status == model.WorkflowExecutionStatusCompleted
	}, 10*time.Second, 1*time.Second, "Execution 1 should complete")

	// 7. Test Filter: Status = COMPLETED
	s.T().Log("Filtering Executions by Status=COMPLETED")
	completedList := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{"status": string(model.WorkflowExecutionStatusCompleted)})
	foundCompleted := false
	for _, exec := range completedList.Executions {
		if exec.ID == runID1 {
			foundCompleted = true
		}
		s.Assert().NotEqual(runID2, exec.ID, "Canceled execution should NOT be in COMPLETED list")
	}
	s.Assert().True(foundCompleted, "Completed execution 1 should be found")

	// 8. Test Filter: Status = CANCELED
	s.T().Log("Filtering Executions by Status=CANCELED")
	canceledList := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{"status": string(model.WorkflowExecutionStatusCanceled)})
	foundCanceled := false
	for _, exec := range canceledList.Executions {
		if exec.ID == runID2 {
			foundCanceled = true
		}
		s.Assert().NotEqual(runID1, exec.ID, "Completed execution should NOT be in CANCELED list")
	}
	s.Assert().True(foundCanceled, "Canceled execution 2 should be found")

	// 9. Test Filter: Trigger Type (Manual)
	s.T().Log("Filtering Executions by Trigger Type=manual")
	manualList := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{"type": "manual"})
	found1 := false
	found2 := false
	for _, exec := range manualList.Executions {
		if exec.ID == runID1 {
			found1 = true
		}
		if exec.ID == runID2 {
			found2 = true
		}
	}
	s.Assert().True(found1, "Execution 1 (manual) should be found")
	s.Assert().True(found2, "Execution 2 (manual) should be found")

	// 10. Test Sorting (OrderBy StartTime DESC - default)
	// Note: Standard Visibility (used in tests) does NOT support ORDER BY.
	// s.T().Log("Testing Default Sorting (StartTime DESC)")
	// // We expect runID2 (started later) to be before runID1
	// defaultList := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, nil)
	// if len(defaultList.Executions) >= 2 {
	// 	// Find indices of our runs
	// 	idx1 := -1
	// 	idx2 := -1
	// 	for i, exec := range defaultList.Executions {
	// 		if exec.ID == runID1 {
	// 			idx1 = i
	// 		}
	// 		if exec.ID == runID2 {
	// 			idx2 = i
	// 		}
	// 	}
	// 	if idx1 != -1 && idx2 != -1 {
	// 		s.Assert().Less(idx2, idx1, "Newer execution (runID2) should appear before older execution (runID1) in DESC sort")
	// 	}
	// }

	// 11. Test Sorting (OrderBy StartTime ASC)
	// s.T().Log("Testing Sorting (StartTime ASC)")
	// ascList := s.listWorkflowExecutionsWithFilters(createdWorkflow.ID, map[string]string{"order_by": "StartTime", "order_dir": "ASC"})
	// if len(ascList.Executions) >= 2 {
	// 	idx1 := -1
	// 	idx2 := -1
	// 	for i, exec := range ascList.Executions {
	// 		if exec.ID == runID1 {
	// 			idx1 = i
	// 		}
	// 		if exec.ID == runID2 {
	// 			idx2 = i
	// 		}
	// 	}
	// 	if idx1 != -1 && idx2 != -1 {
	// 		s.Assert().Less(idx1, idx2, "Older execution (runID1) should appear before newer execution (runID2) in ASC sort")
	// 	}
	// }
}

func (s *IntegrationTestSuite) TestGetWorkflow() {
	s.T().Log("Running TestGetWorkflow...")

	// Create a dummy workflow to retrieve
	workflow := s.loadWorkflowFromFile("testdata/test-get-workflow.yaml")
	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow for getting")

	retrievedWorkflow := s.getWorkflow(createdWorkflow.ID)

	s.Assert().Equal(createdWorkflow.ID, retrievedWorkflow.ID, "Retrieved workflow ID should match created ID")
	s.Assert().Equal(createdWorkflow.Name, retrievedWorkflow.Name, "Retrieved workflow name should match created name")
}

func (s *IntegrationTestSuite) TestGetWorkflowExecutionDetails() {
	s.T().Log("Running TestGetWorkflowExecutionDetails...")

	// 1. Create a simple workflow
	workflow := s.loadWorkflowFromFile("testdata/test-get-execution-details-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow")

	// 2. Execute the workflow
	executionId, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute workflow")

	// 3. Wait for workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, executionId)
	s.NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, executionId)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Workflow execution failed or timed out")

	// 4. Get workflow execution details via API
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, executionId)

	s.Assert().Equal(createdWorkflow.ID, executionDetails.WorkflowId, "Retrieved workflow ID should match created ID")
	s.Assert().Equal(executionId, executionDetails.Id, "Retrieved run ID should match executed run ID")
	s.Assert().Equal(model.WorkflowExecutionStatusCompleted, executionDetails.Status, "Workflow status should be COMPLETED")
	s.Assert().NotNil(executionDetails.StartTime, "Start time should not be nil")
	s.Assert().NotNil(executionDetails.CloseTime, "Close time should not be nil")
	// Assert that only the user-defined task is present
	s.Require().Len(executionDetails.Tasks, 1, "Should have exactly one user-defined task execution detail")
	task1 := executionDetails.Tasks[0]
	s.Assert().Equal("task1", task1.ID, "The task ID should be 'task1'")
	s.Assert().Equal(model.TaskStatusCompleted, task1.Status, "Task status should be COMPLETED")
}

func (s *IntegrationTestSuite) TestDeleteWorkflow() {
	s.T().Log("Running TestDeleteWorkflow...")

	// Create a dummy workflow to delete (without a schedule trigger)
	workflow := s.loadWorkflowFromFile("testdata/test-delete-workflow.yaml")
	createdWorkflow, _, err := s.createWorkflow(workflow)
	s.Require().NoError(err, "Failed to create workflow for deletion")

	// Delete the workflow
	s.deleteWorkflow(createdWorkflow.ID, false)
}

func (s *IntegrationTestSuite) TestCancelWorkflowExecution() {
	s.T().Log("Running TestCancelWorkflowExecution...")

	// 1. Create a long-running workflow
	workflow := s.loadWorkflowFromFile("testdata/test-long-running-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create long-running workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute long-running workflow")

	// 3. Cancel the workflow execution
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/runs/%s/cancel", apiBaseURL, createdWorkflow.ID, runID), nil)
	s.Require().NoError(err, "Failed to create request for canceling workflow")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for canceling workflow")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for cancel workflow")

	// 4. Verify that the workflow execution is canceled
	s.Require().Eventually(func() bool {
		executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
		s.T().Log("executionDetails >> ", executionDetails.Status, executionDetails.Id, runID)
		return executionDetails.Status == model.WorkflowExecutionStatusCanceled
	}, 10*time.Second, 1*time.Second, "Workflow execution should have been canceled")
}

func (s *IntegrationTestSuite) TestPauseAndResumeWorkflowSchedule() {
	s.T().Log("Running TestPauseAndResumeWorkflowSchedule...")

	// 1. Create a scheduled workflow
	workflow := s.loadWorkflowFromFile("testdata/test-scheduled-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create scheduled workflow")

	// 2. Pause the workflow schedule
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/pause", apiBaseURL, createdWorkflow.ID), nil)
	s.Require().NoError(err, "Failed to create request for pausing workflow")
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for pausing workflow")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for pause workflow")

	// 3a. Verify that the workflow's administrative status is Paused
	pausedWorkflow := s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusPaused, pausedWorkflow.Status, "Workflow administrative status should be PAUSED")

	// 3b. Verify that the schedule is paused
	scheduleID := "workflow-schedule-" + createdWorkflow.ID
	handle := s.temporalClient.ScheduleClient().GetHandle(context.Background(), scheduleID)
	s.Require().Eventually(func() bool {
		desc, err := handle.Describe(context.Background())
		s.Require().NoError(err)
		return desc.Schedule.State.Paused
	}, 10*time.Second, 1*time.Second, "Workflow schedule should have been paused")

	// 4. Resume the workflow schedule
	req, err = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/workflows/%s/resume", apiBaseURL, createdWorkflow.ID), nil)
	s.Require().NoError(err, "Failed to create request for resuming workflow")
	s.addRequestHeaders(req)

	resp, err = http.DefaultClient.Do(req)
	s.Require().NoError(err, "Failed to send request for resuming workflow")
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode, "Expected 200 OK for resume workflow")

	// 5a. Verify that the workflow's administrative status is Active
	resumedWorkflow := s.getWorkflow(createdWorkflow.ID)
	s.Assert().Equal(model.WorkflowStatusActive, resumedWorkflow.Status, "Workflow administrative status should be ACTIVE")

	// 5b. Verify that the schedule is running again
	s.Require().Eventually(func() bool {
		desc, err := handle.Describe(context.Background())
		s.Require().NoError(err)
		return !desc.Schedule.State.Paused
	}, 10*time.Second, 1*time.Second, "Workflow schedule should have been resumed")
}
