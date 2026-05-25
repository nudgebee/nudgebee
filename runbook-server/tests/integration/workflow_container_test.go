package integration_test

import (
	"context"
	"encoding/json"
	"fmt"

	"nudgebee/runbook/internal/model"
)

func (s *IntegrationTestSuite) TestSwitchWorkflow() {
	s.T().Log("Running TestSwitchWorkflow...")

	workflow := s.loadWorkflowFromFile("testdata/test-switch-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create switch workflow")

	// findSwitchWithChild asserts the switch is surfaced at the top level with
	// its selected branch nested under it.
	findSwitchWithChild := func(details *model.WorkflowExecutionDetails, expectedChildID, expectedSelectedCase string) {
		var switchTask *model.TaskExecutionDetails
		for i, t := range details.Tasks {
			if t.ID == "my-switch" {
				switchTask = &details.Tasks[i]
				break
			}
		}
		s.Require().NotNil(switchTask, "switch task 'my-switch' should be present in response")
		s.Assert().Equal("core.switch", switchTask.Type)
		s.Assert().Equal(model.TaskStatusCompleted, switchTask.Status)

		if outputMap, ok := switchTask.Output.(map[string]any); ok {
			s.Assert().Equal(expectedSelectedCase, outputMap["selected_case"])
		} else {
			s.T().Errorf("switch output should be a map, got %T", switchTask.Output)
		}

		var childFound bool
		for _, c := range switchTask.Children {
			if c.ID == expectedChildID {
				childFound = true
				s.Assert().Equal(model.TaskStatusCompleted, c.Status)
				break
			}
		}
		s.Assert().True(childFound, "selected branch %q should be nested under the switch", expectedChildID)
	}

	for _, tc := range []struct{ value, childID, selectedCase string }{
		{"A", "task-a", "A"},
		{"B", "task-b", "B"},
		{"C", "task-c", "default"},
	} {
		runID, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"value": tc.value})
		s.Require().NoError(err, "Failed to execute workflow for %s", tc.value)

		temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
		s.Require().NoError(err)
		wfExec := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
		var result string
		s.Require().NoError(wfExec.Get(context.Background(), &result), "Workflow execution failed for %s", tc.value)

		details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
		findSwitchWithChild(details, tc.childID, tc.selectedCase)
	}
}

// TestSwitchFanInConvergence exercises the bug fixed by the parent commit:
// a `core.switch` fans out to N branches, and a downstream task lists every
// branch in DependsOn (the convergence / fan-in pattern). Before the fix the
// runtime stalled with "workflow stalled: no tasks to run, but not all tasks
// are complete" because parent state was never populated for the branch IDs.
//
// Observable trace surface (driven by Temporal history events, not the
// in-process `executionTrace` slice):
//   - Only tasks scheduled as Temporal activities show up in `details.Tasks`.
//   - The selected branch runs as activity `<switchID>-<branchID>` (e.g.
//     `router-leaf-a`) and surfaces as a top-level COMPLETED entry.
//   - The inline switch task itself, and unselected branches that were never
//     scheduled, do NOT surface — they only exist in templateContext / logs.
//   - The convergence task surfaces and its output proves the selected
//     branch's state propagated into the parent under its ORIGINAL ID
//     (`{{ Tasks['leaf-a'].output.data }}` resolved correctly). This is the
//     load-bearing check: without the fix it would either stall, or the
//     template would render as an empty string.
func (s *IntegrationTestSuite) TestSwitchFanInConvergence() {
	s.T().Log("Running TestSwitchFanInConvergence...")

	workflow := s.loadWorkflowFromFile("testdata/test-switch-fanin-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create switch fan-in workflow")

	type matrixCase struct {
		input          string
		selectedBranch string
		expectedJoin   string
	}

	cases := []matrixCase{
		{input: "A", selectedBranch: "leaf-a",
			expectedJoin: "joined:COMPLETED/ran-leaf-a|SKIPPED/-|SKIPPED/-|SKIPPED/-"},
		{input: "B", selectedBranch: "leaf-b",
			expectedJoin: "joined:SKIPPED/-|COMPLETED/ran-leaf-b|SKIPPED/-|SKIPPED/-"},
		{input: "C", selectedBranch: "leaf-c",
			expectedJoin: "joined:SKIPPED/-|SKIPPED/-|COMPLETED/ran-leaf-c|SKIPPED/-"},
		{input: "Z", selectedBranch: "leaf-default",
			expectedJoin: "joined:SKIPPED/-|SKIPPED/-|SKIPPED/-|COMPLETED/ran-leaf-default"},
	}

	for _, tc := range cases {
		s.Run(fmt.Sprintf("input=%s", tc.input), func() {
			runID, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"value": tc.input})
			s.Require().NoError(err, "Failed to execute workflow for input %s", tc.input)

			temporalID, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
			s.Require().NoError(err)
			exec := s.temporalClient.GetWorkflow(context.Background(), temporalID, runID)
			var resultStr string
			err = exec.Get(context.Background(), &resultStr)
			s.Require().NoError(err, "Workflow with input %s did not complete (the bug would stall it here)", tc.input)

			details := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
			s.Require().Equal(model.WorkflowExecutionStatusCompleted, details.Status,
				"Workflow with input %s did not reach COMPLETED status (got %s)", tc.input, details.Status)

			byID := make(map[string]model.TaskExecutionDetails, len(details.Tasks))
			for _, t := range details.Tasks {
				byID[t.ID] = t
			}

			// 1. Selected branch ran as an activity under its renamed ID.
			renamedSelected := fmt.Sprintf("router-%s", tc.selectedBranch)
			selectedTask, ok := byID[renamedSelected]
			s.Require().True(ok, "Selected branch activity %q missing from trace for input %s; saw %v",
				renamedSelected, tc.input, taskIDsOf(details.Tasks))
			s.Assert().Equal(model.TaskStatusCompleted, selectedTask.Status,
				"Selected branch %s should be COMPLETED for input %s", renamedSelected, tc.input)

			// 2. Unselected branch activities are NOT scheduled in Temporal — confirm absence.
			for _, branch := range []string{"leaf-a", "leaf-b", "leaf-c", "leaf-default"} {
				if branch == tc.selectedBranch {
					continue
				}
				renamedUnselected := fmt.Sprintf("router-%s", branch)
				_, scheduled := byID[renamedUnselected]
				s.Assert().False(scheduled,
					"Unselected branch %q must not be scheduled as an activity for input %s", renamedUnselected, tc.input)
			}

			// 3. Converge ran and templated the selected branch's output. This is the load-bearing
			// check: it proves branch state propagated into the parent context under each branch's
			// ORIGINAL ID, so `{{ Tasks['leaf-a'].output.data }}` and `{{ Tasks['leaf-b'].status }}`
			// (etc.) all resolve correctly. Pre-fix, the workflow would stall before reaching
			// converge; even a partial fix that only marked the selected branch would render empty
			// strings for the skipped siblings.
			converge, ok := byID["converge"]
			s.Require().True(ok, "Converge task missing from trace for input %s; saw %v",
				tc.input, taskIDsOf(details.Tasks))
			s.Assert().Equal(model.TaskStatusCompleted, converge.Status,
				"Converge task must run after fan-in for input %s (was %s)", tc.input, converge.Status)
			outMap, ok := converge.Output.(map[string]any)
			s.Require().True(ok, "Converge task output was not a map[string]any: %T", converge.Output)
			s.Assert().Equal(tc.expectedJoin, outMap["data"],
				"Converge task should template selected branch output for input %s", tc.input)
		})
	}

	s.deleteWorkflow(createdWorkflow.ID, false)
}

// taskIDsOf is a small helper used by fan-in assertion error messages to make
// "missing task" failures actionable — it dumps the actual top-level IDs.
func taskIDsOf(tasks []model.TaskExecutionDetails) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, fmt.Sprintf("%s(%s)", t.ID, t.Status))
	}
	return ids
}

func (s *IntegrationTestSuite) TestSwitchNestedWorkflow() {
	s.T().Log("Running TestSwitchNestedWorkflow...")

	workflow := s.loadWorkflowFromFile("testdata/test-switch-nested-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create nested switch workflow")

	// 1. Nested Switch Path: root-switch matches "nested" → nested-switch,
	//    nested-switch matches "A" → task-nested-a. Both switches surface at
	//    their level with the matched branch nested under each.
	s.T().Log("Testing Nested Switch Path...")
	runID_Nested, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"path": "nested", "sub_path": "A"})
	s.Require().NoError(err)
	s.waitForWorkflowCompletion(createdWorkflow.ID, runID_Nested)
	details_Nested := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID_Nested)

	var rootSwitch *model.TaskExecutionDetails
	for i, t := range details_Nested.Tasks {
		if t.ID == "root-switch" {
			rootSwitch = &details_Nested.Tasks[i]
			break
		}
	}
	s.Require().NotNil(rootSwitch, "root-switch should be surfaced at the top level")
	s.Assert().Equal(model.TaskStatusCompleted, rootSwitch.Status)
	if outMap, ok := rootSwitch.Output.(map[string]any); ok {
		s.Assert().Equal("nested", outMap["selected_case"])
	}

	// The matched branch (nested-switch) and its leaf (task-nested-a) appear
	// nested under root-switch with the "root-switch-" prefix stripped.
	var innerSwitchFound, nestedLeafFound bool
	for _, c := range rootSwitch.Children {
		if c.ID == "nested-switch" {
			innerSwitchFound = true
			s.Assert().Equal("core.switch", c.Type)
			s.Assert().Equal(model.TaskStatusCompleted, c.Status)
			if outMap, ok := c.Output.(map[string]any); ok {
				s.Assert().Equal("A", outMap["selected_case"])
			}
		}
		if c.ID == "nested-switch-task-nested-a" {
			nestedLeafFound = true
			s.Assert().Equal(model.TaskStatusCompleted, c.Status)
		}
	}
	s.Assert().True(innerSwitchFound, "nested-switch should be nested under root-switch")
	s.Assert().True(nestedLeafFound, "task-nested-a should be present under root-switch")

	// 2. Dependency Chain Path: root-switch matches "dep-chain" → task-chain-1
	//    and task-chain-2 run in chain scope under root-switch.
	s.T().Log("Testing Dependency Chain Path...")
	runID_Chain, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"path": "dep-chain"})
	s.Require().NoError(err)
	s.waitForWorkflowCompletion(createdWorkflow.ID, runID_Chain)
	details_Chain := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID_Chain)

	var chainRoot *model.TaskExecutionDetails
	for i, t := range details_Chain.Tasks {
		if t.ID == "root-switch" {
			chainRoot = &details_Chain.Tasks[i]
			break
		}
	}
	s.Require().NotNil(chainRoot)
	var chain1Found, chain2Found bool
	for _, c := range chainRoot.Children {
		if c.ID == "task-chain-1" {
			chain1Found = true
			s.Assert().Equal(model.TaskStatusCompleted, c.Status)
		}
		if c.ID == "task-chain-2" {
			chain2Found = true
			s.Assert().Equal(model.TaskStatusCompleted, c.Status)
		}
	}
	s.Assert().True(chain1Found, "task-chain-1 should be nested under root-switch")
	s.Assert().True(chain2Found, "task-chain-2 should be nested under root-switch")

	// 3. Empty Path: switch falls through with no default → switch COMPLETED
	//    with empty children; workflow still completes successfully.
	s.T().Log("Testing Empty Path...")
	runID_Empty, err := s.executeWorkflow(createdWorkflow.ID, map[string]any{"path": "empty"})
	s.Require().NoError(err)
	s.waitForWorkflowCompletion(createdWorkflow.ID, runID_Empty)
	details_Empty := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID_Empty)
	s.Assert().Equal(model.WorkflowExecutionStatusCompleted, details_Empty.Status)
	for _, t := range details_Empty.Tasks {
		if t.ID == "root-switch" {
			s.Assert().Equal(model.TaskStatusCompleted, t.Status)
		}
	}
}

func (s *IntegrationTestSuite) TestGroupTaskWorkflow() {
	s.T().Log("Running TestGroupTaskWorkflow...")

	// 1. Create a workflow with a group task
	workflow := s.loadWorkflowFromFile("testdata/test-group-task-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create group task workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute group task workflow")

	// 3. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var result string
	err = workflowExecution.Get(context.Background(), &result)
	s.Require().NoError(err, "Group task workflow execution failed or timed out")

	// 4. Verify that the nested tasks were executed by checking the execution details
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	s.Require().Len(executionDetails.Tasks, 1, "Should have one top-level task (the group)")

	groupTask := executionDetails.Tasks[0]
	s.Assert().Equal("my-group", groupTask.ID, "The top-level task should be the group")
	s.Assert().Equal(model.TaskStatusCompleted, groupTask.Status, "Group task should be completed")
	s.Require().Len(groupTask.Children, 2, "Group task should have two children")

	task1Found := false
	task2Found := false
	for _, task := range groupTask.Children {
		if task.ID == "task1-in-group" {
			task1Found = true
			s.Assert().Equal(model.TaskStatusCompleted, task.Status, "Task 'task1-in-group' should be completed")
		}
		if task.ID == "task2-in-group" {
			task2Found = true
			s.Assert().Equal(model.TaskStatusCompleted, task.Status, "Task 'task2-in-group' should be completed")
		}
	}

	s.Assert().True(task1Found, "The first task in the group should have been executed")
	s.Assert().True(task2Found, "The second task in the group should have been executed")
}

func (s *IntegrationTestSuite) TestCallWorkflowTask() {

	s.T().Log("Running TestCallWorkflowTask...")

	// 1. Create the child workflow
	childWorkflow := s.loadWorkflowFromFile("testdata/test-child-workflow.yaml")
	createdChildWorkflow, _, err := s.createAndActivateWorkflow(childWorkflow)
	s.Require().NoError(err, "Failed to create child workflow")
	s.Require().NotEmpty(createdChildWorkflow.ID, "Created child workflow ID should not be empty")
	s.T().Logf("Created child workflow with ID: %s", createdChildWorkflow.ID)

	// 2. Create the parent workflow
	parentWorkflow := s.loadWorkflowFromFile("testdata/test-parent-workflow.yaml")
	createdParentWorkflow, _, err := s.createAndActivateWorkflow(parentWorkflow)
	s.Require().NoError(err, "Failed to create parent workflow")
	s.Require().NotEmpty(createdParentWorkflow.ID, "Created parent workflow ID should not be empty")
	s.T().Logf("Created parent workflow with ID: %s", createdParentWorkflow.ID)

	// 3. Execute the parent workflow
	runID, err := s.executeWorkflow(createdParentWorkflow.ID, map[string]any{"greeting": "Hello"})
	s.Require().NoError(err, "Failed to execute parent workflow")
	s.Require().NotEmpty(runID, "Workflow run ID should not be empty")
	s.T().Logf("Executed parent workflow %s with Run ID: %s", createdParentWorkflow.ID, runID)

	// 4. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdParentWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "Parent workflow execution failed or timed out")
	s.T().Logf("Parent workflow %s (Run ID: %s) completed with result: %s", createdParentWorkflow.ID, runID, resultStr)

	// 5. Verify the final output
	result := map[string]any{}
	err = json.Unmarshal([]byte(resultStr), &result)
	s.Require().NoError(err, "Failed to unmarshal workflow result")
	s.T().Logf("Test client received resultStr: %s", resultStr)
	s.T().Logf("Test client unmarshaled result: %+v", result)
	s.Assert().Equal("Hello World!", result["final_output"], "The final workflow output did not match the expected value")

	// 6. Verify Execution Details
	s.T().Log("Verifying execution details...")
	executionDetails := s.getWorkflowExecutionDetails(createdParentWorkflow.ID, runID)
	s.Require().NotNil(executionDetails, "Execution details should not be nil")

	// Since 'call_child' is now executed as a Child Workflow, it should appear as a task in the parent workflow,
	// and its internal tasks should be listed in its 'Children' field.

	var finalEchoTaskFound, callChildTaskFound, childEchoTaskFound bool

	for _, t := range executionDetails.Tasks {
		if t.ID == "final_echo" {
			finalEchoTaskFound = true
			s.Assert().Equal(model.TaskStatusCompleted, t.Status, "'final_echo' task should be completed")
			s.Assert().Contains(t.Output, "data", "'final_echo' task output should contain 'data'")
			s.Assert().Equal("Hello World!", t.Output.(map[string]any)["data"], "'final_echo' task output data did not match")
		}
		if t.ID == "call_child" {
			callChildTaskFound = true
			s.Assert().Equal(model.TaskStatusCompleted, t.Status, "'call_child' task should be completed")
			s.Assert().NotEmpty(t.Children, "'call_child' should have children (the tasks of the child workflow)")

			// Check for 'echo_task' inside children
			for _, childT := range t.Children {
				if childT.ID == "echo_task" {
					childEchoTaskFound = true
					s.Assert().Equal(model.TaskStatusCompleted, childT.Status, "Child workflow's 'echo_task' should be completed")
					s.Assert().Contains(childT.Output, "data", "Child task output should contain 'data'")
					s.Assert().Equal("Hello World!", childT.Output.(map[string]any)["data"], "Child task output data did not match")
				}
			}
		}
	}

	s.Assert().True(finalEchoTaskFound, "'final_echo' task should have been executed")
	s.Assert().True(callChildTaskFound, "'call_child' task should have been executed")
	s.Assert().True(childEchoTaskFound, "'echo_task' should be found within the children of 'call_child'")

	s.deleteWorkflow(createdChildWorkflow.ID, false)
	s.deleteWorkflow(createdParentWorkflow.ID, false)
}

func (s *IntegrationTestSuite) TestCallWorkflowTask_Failure() {
	s.T().Log("Running TestCallWorkflowTask_Failure...")

	// 1. Create the failing child workflow
	childWorkflow := s.loadWorkflowFromFile("testdata/test-child-workflow-fail.yaml")
	createdChildWorkflow, _, err := s.createAndActivateWorkflow(childWorkflow)
	s.Require().NoError(err, "Failed to create failing child workflow")

	// 2. Create the parent workflow that calls it
	parentWorkflow := s.loadWorkflowFromFile("testdata/test-parent-workflow-fail.yaml")
	createdParentWorkflow, _, err := s.createAndActivateWorkflow(parentWorkflow)
	s.Require().NoError(err, "Failed to create parent workflow")

	// 3. Execute the parent workflow
	runID, err := s.executeWorkflow(createdParentWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute parent workflow")

	// 4. Wait for the workflow to complete
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdParentWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)

	// Expect error
	s.Require().Error(err, "Parent workflow should have failed")
	s.T().Logf("Parent workflow failed as expected with error: %v", err)

	// 5. Verify Execution Details show failure
	executionDetails := s.getWorkflowExecutionDetails(createdParentWorkflow.ID, runID)
	s.Assert().Equal(model.WorkflowExecutionStatusFailed, executionDetails.Status)

	var callChildTaskFound bool
	for _, t := range executionDetails.Tasks {
		if t.ID == "call_fail_child" {
			callChildTaskFound = true
			s.Assert().Equal(model.TaskStatusFailed, t.Status)
			s.Assert().NotEmpty(t.Children)
			// Check child task failure
			childFailed := false
			for _, child := range t.Children {
				if child.ID == "failing_task" && child.Status == model.TaskStatusFailed {
					childFailed = true
					break
				}
			}
			s.Assert().True(childFailed, "Child task 'failing_task' should have failed")
		}
	}
	s.Assert().True(callChildTaskFound, "'call_fail_child' task should have been executed")
	s.deleteWorkflow(createdChildWorkflow.ID, false)
	s.deleteWorkflow(createdParentWorkflow.ID, false)
}

func (s *IntegrationTestSuite) TestForEachWorkflow() {
	s.T().Log("Running TestForEachWorkflow...")

	// 1. Create the workflow
	workflow := s.loadWorkflowFromFile("testdata/test-foreach-workflow.yaml")
	createdWorkflow, _, err := s.createAndActivateWorkflow(workflow)
	s.Require().NoError(err, "Failed to create foreach workflow")

	// 2. Execute the workflow
	runID, err := s.executeWorkflow(createdWorkflow.ID, nil)
	s.Require().NoError(err, "Failed to execute foreach workflow")

	// 3. Wait for completion
	temporalId, err := s.resolveTemporalWorkflowID(context.Background(), createdWorkflow.ID, runID)
	s.Require().NoError(err)
	workflowExecution := s.temporalClient.GetWorkflow(context.Background(), temporalId, runID)
	var resultStr string
	err = workflowExecution.Get(context.Background(), &resultStr)
	s.Require().NoError(err, "Foreach workflow execution failed")

	// 4. Verify Execution Details
	executionDetails := s.getWorkflowExecutionDetails(createdWorkflow.ID, runID)
	var foreachTask *model.TaskExecutionDetails
	for _, t := range executionDetails.Tasks {
		if t.ID == "foreach-task" {
			foreachTask = &t
			s.Assert().Equal(model.TaskStatusCompleted, t.Status)
			break
		}
	}
	s.Require().NotNil(foreachTask, "Foreach task should have been executed and found in details")

	// Verify iteration tasks are children of foreach-task
	// Expected structure: Children are "Iteration 0", "Iteration 1"...
	// Inside them: "print-fruit" (trimmed from 0-print-fruit)

	expectedIterations := map[string]bool{
		"Iteration 0": false,
		"Iteration 1": false,
		"Iteration 2": false,
	}

	for _, iterTask := range foreachTask.Children {
		if _, ok := expectedIterations[iterTask.ID]; ok {
			expectedIterations[iterTask.ID] = true
			s.Assert().Equal(model.TaskStatusCompleted, iterTask.Status, "Iteration task %s should be completed", iterTask.ID)

			// Verify inside iteration
			foundPrint := false
			for _, child := range iterTask.Children {
				if child.ID == "print-fruit" {
					foundPrint = true
					s.Assert().Equal(model.TaskStatusCompleted, child.Status, "Child task print-fruit in %s should be completed", iterTask.ID)
				}
			}
			s.Assert().True(foundPrint, "print-fruit should be found in %s", iterTask.ID)
		}
	}

	for id, found := range expectedIterations {
		s.Assert().True(found, "Iteration task %s should have been executed", id)
	}
}
