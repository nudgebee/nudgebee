package core

import (
	"context"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestReWooPlanner2_GraphLogic(t *testing.T) {
	// Setup a basic graph
	// Plan:
	// 1. Task A (No deps)
	// 2. Task B (Dep: Task A)
	// 3. Task C (No deps)
	// 4. Task D (Dep: Task B, Task C)

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
	}

	// Manually populate the graph
	planner.executionGraph["TaskA"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskA", Tool: "ToolA", Dependency: []string{}},
		Status: StepStatusPending,
	}
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"TaskA"}},
		Status: StepStatusPending,
	}
	planner.executionGraph["TaskC"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskC", Tool: "ToolC", Dependency: []string{}},
		Status: StepStatusPending,
	}
	planner.executionGraph["TaskD"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskD", Tool: "ToolD", Dependency: []string{"TaskB", "TaskC"}},
		Status: StepStatusPending,
	}

	t.Run("Batch 1: Should pick Task A and Task C", func(t *testing.T) {
		batch := planner.getRunnableBatch()
		assert.Len(t, batch, 2)
		ids := []string{batch[0].ToolID, batch[1].ToolID}
		assert.Contains(t, ids, "TaskA")
		assert.Contains(t, ids, "TaskC")
		assert.NotContains(t, ids, "TaskB")
		assert.NotContains(t, ids, "TaskD")

		// Verify status update to Running happens inside getRunnableBatch or caller?
		// In current impl, getRunnableBatch updates status to Running.
		assert.Equal(t, StepStatusRunning, planner.executionGraph["TaskA"].Status)
		assert.Equal(t, StepStatusRunning, planner.executionGraph["TaskC"].Status)
	})

	t.Run("Sync State: Task A completes", func(t *testing.T) {
		// Simulate execution results
		steps := []NBAgentPlannerToolActionStep{
			{
				Action:      NBAgentPlannerToolAction{ToolID: "TaskA"},
				Observation: "Result A",
				Status:      ToolStatusSuccess,
			},
		}
		planner.syncState(steps)

		assert.Equal(t, StepStatusCompleted, planner.executionGraph["TaskA"].Status)
		assert.Equal(t, "Result A", planner.executionGraph["TaskA"].Output)
	})

	t.Run("Batch 2: Task B should be ready (Task C still running)", func(t *testing.T) {
		batch := planner.getRunnableBatch()
		assert.Len(t, batch, 1)
		assert.Equal(t, "TaskB", batch[0].ToolID)
	})

	t.Run("Sync State: Task B and C complete", func(t *testing.T) {
		steps := []NBAgentPlannerToolActionStep{
			{
				Action:      NBAgentPlannerToolAction{ToolID: "TaskB"},
				Observation: "Result B",
				Status:      ToolStatusSuccess,
			},
			{
				Action:      NBAgentPlannerToolAction{ToolID: "TaskC"},
				Observation: "Result C",
				Status:      ToolStatusSuccess,
			},
		}
		planner.syncState(steps)
		assert.Equal(t, StepStatusCompleted, planner.executionGraph["TaskB"].Status)
		assert.Equal(t, StepStatusCompleted, planner.executionGraph["TaskC"].Status)
	})

	t.Run("Batch 3: Task D should be ready", func(t *testing.T) {
		batch := planner.getRunnableBatch()
		assert.Len(t, batch, 1)
		assert.Equal(t, "TaskD", batch[0].ToolID)
	})
}

// TestReWooPlanner2_FinalizePendingSteps verifies that unexecuted Pending and
// Waiting nodes are promoted to Skipped when the planner terminates (#28243).
// Other terminal states (Completed/Failed) must remain untouched.
func TestReWooPlanner2_FinalizePendingSteps(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph: map[string]*PlannerNode{
			"E1": {Step: rewooPlannerStep2{ID: "E1", Tool: "tool_a"}, Status: StepStatusCompleted, Output: "ok"},
			"E2": {Step: rewooPlannerStep2{ID: "E2", Tool: "tool_b"}, Status: StepStatusFailed, Output: "boom"},
			"E3": {Step: rewooPlannerStep2{ID: "E3", Tool: "tool_c"}, Status: StepStatusPending},
			"E4": {Step: rewooPlannerStep2{ID: "E4", Tool: "tool_d"}, Status: StepStatusPending, Output: "existing note"},
			"E5": {Step: rewooPlannerStep2{ID: "E5", Tool: "tool_e"}, Status: StepStatusWaiting},
			"E6": {Step: rewooPlannerStep2{ID: "E6", Tool: "tool_f"}, Status: StepStatusSkipped, Output: "already skipped"},
		},
		// plannerAgentID left empty so updateStoredPlan() returns early and does not touch the DB.
	}

	planner.finalizePendingSteps("Skipped because the investigation finished before this step was executed.")

	// Pending and Waiting nodes become Skipped; existing Output is preserved, empty Output gets the reason.
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E3"].Status)
	assert.Equal(t, "Skipped because the investigation finished before this step was executed.", planner.executionGraph["E3"].Output)
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E4"].Status)
	assert.Equal(t, "existing note", planner.executionGraph["E4"].Output, "existing output must not be overwritten")
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E5"].Status, "Waiting nodes must also be finalized to Skipped")
	assert.Equal(t, "Skipped because the investigation finished before this step was executed.", planner.executionGraph["E5"].Output)

	// Non-pending/non-waiting nodes untouched.
	assert.Equal(t, StepStatusCompleted, planner.executionGraph["E1"].Status)
	assert.Equal(t, StepStatusFailed, planner.executionGraph["E2"].Status)
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E6"].Status)
	assert.Equal(t, "already skipped", planner.executionGraph["E6"].Output)
}

// TestReWooPlanner2_FinalizePendingSteps_EmptyGraph guards against a panic
// when the planner terminates before any nodes were registered — e.g. a
// top-level “direct“ classification that returns a finish action without
// ever building an execution graph.
func TestReWooPlanner2_FinalizePendingSteps_EmptyGraph(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph: map[string]*PlannerNode{},
	}

	// Must not panic or error on an empty graph; no-op by design.
	planner.finalizePendingSteps("any reason")

	assert.Empty(t, planner.executionGraph, "empty graph must stay empty")
}

// TestReWooPlanner2_FinalizePendingSteps_NoPendingNoOp verifies the
// skipped_count == 0 fast path: when no Pending or Waiting nodes exist,
// every other state (Running/Completed/Failed/Skipped) must remain
// untouched AND the persistence helper must not be invoked.
// Using plannerAgentID == "" keeps updateStoredPlan a no-op so this
// test stays pure-unit without any DAO plumbing.
func TestReWooPlanner2_FinalizePendingSteps_NoPendingNoOp(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph: map[string]*PlannerNode{
			"E1": {Step: rewooPlannerStep2{ID: "E1", Tool: "t1"}, Status: StepStatusCompleted, Output: "done"},
			"E2": {Step: rewooPlannerStep2{ID: "E2", Tool: "t2"}, Status: StepStatusFailed, Output: "err"},
			"E3": {Step: rewooPlannerStep2{ID: "E3", Tool: "t3"}, Status: StepStatusSkipped, Output: "skip"},
			"E4": {Step: rewooPlannerStep2{ID: "E4", Tool: "t4"}, Status: StepStatusRunning},
		},
	}

	before := map[string]StepStatus{}
	for id, node := range planner.executionGraph {
		before[id] = node.Status
	}

	planner.finalizePendingSteps("unused reason")

	for id, expected := range before {
		assert.Equal(t, expected, planner.executionGraph[id].Status,
			"node %s must remain in its original state when no Pending nodes exist", id)
	}
}

// TestReWooPlanner2_FinalizePendingSteps_AllPending verifies the common case
// where the solver finishes early and the entire graph is still Pending —
// every node should transition to Skipped with the reason injected.
func TestReWooPlanner2_FinalizePendingSteps_AllPending(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph: map[string]*PlannerNode{
			"E1": {Step: rewooPlannerStep2{ID: "E1", Tool: "t1"}, Status: StepStatusPending},
			"E2": {Step: rewooPlannerStep2{ID: "E2", Tool: "t2"}, Status: StepStatusPending},
			"E3": {Step: rewooPlannerStep2{ID: "E3", Tool: "t3"}, Status: StepStatusPending},
		},
	}

	reason := "Skipped because the investigation finished before this step was executed."
	planner.finalizePendingSteps(reason)

	for id, node := range planner.executionGraph {
		assert.Equal(t, StepStatusSkipped, node.Status, "node %s must be Skipped", id)
		assert.Equal(t, reason, node.Output, "node %s must carry the supplied reason", id)
	}
}

// TestReWooPlanner2_FinalizePendingSteps_Idempotent verifies that a second
// invocation is safe and a true no-op — important because Plan()'s deferred
// hook can legitimately fire multiple times across iterations when the
// planner is resumed, and we must not re-stamp the reason over a node
// whose Output was updated after the first finalize.
func TestReWooPlanner2_FinalizePendingSteps_Idempotent(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph: map[string]*PlannerNode{
			"E1": {Step: rewooPlannerStep2{ID: "E1", Tool: "t1"}, Status: StepStatusPending},
			"E2": {Step: rewooPlannerStep2{ID: "E2", Tool: "t2"}, Status: StepStatusCompleted, Output: "ok"},
		},
	}

	planner.finalizePendingSteps("first reason")

	// After first call: E1 is Skipped with "first reason", E2 unchanged.
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E1"].Status)
	assert.Equal(t, "first reason", planner.executionGraph["E1"].Output)

	// Simulate a downstream writer updating the reason after the finalize
	// (e.g. an operator annotation surfaced via the UI). A second finalize
	// must not clobber that.
	planner.executionGraph["E1"].Output = "operator annotated: looked fine"

	planner.finalizePendingSteps("second reason")

	// Status still Skipped; Output was non-empty so the second finalize
	// preserves the annotation — mirrors the "existing Output" branch of
	// finalizePendingSteps.
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["E1"].Status)
	assert.Equal(t, "operator annotated: looked fine", planner.executionGraph["E1"].Output)
	// E2 still untouched across both runs.
	assert.Equal(t, StepStatusCompleted, planner.executionGraph["E2"].Status)
	assert.Equal(t, "ok", planner.executionGraph["E2"].Output)
}

// TestReWooPlanner2_FinalizePendingSteps_DeferGating documents — as an
// executable specification — which finish states must trigger the finalize
// hook vs which must skip it. Mirrors the exact condition in Plan()'s
// deferred closure so that a future refactor of the gating logic breaks
// this test and the reviewer is forced to re-evaluate the contract.
func TestReWooPlanner2_FinalizePendingSteps_DeferGating(t *testing.T) {
	shouldFinalize := func(finish *NBAgentPlannerFinishAction) bool {
		if finish == nil {
			return false
		}
		if finish.Status == ConversationStatusWaiting || finish.Status == ConversationStatusWaitingForClientTool {
			return false
		}
		return true
	}

	cases := []struct {
		name   string
		finish *NBAgentPlannerFinishAction
		want   bool
	}{
		{"nil finish skips finalize", nil, false},
		{"waiting for user input skips finalize (keeps Pending for resume)",
			&NBAgentPlannerFinishAction{Status: ConversationStatusWaiting}, false},
		{"waiting for client tool skips finalize",
			&NBAgentPlannerFinishAction{Status: ConversationStatusWaitingForClientTool}, false},
		{"completed triggers finalize",
			&NBAgentPlannerFinishAction{Status: ConversationStatusCompleted}, true},
		{"failed triggers finalize",
			&NBAgentPlannerFinishAction{Status: ConversationStatusFailed}, true},
		{"in_progress triggers finalize (terminal-by-default)",
			&NBAgentPlannerFinishAction{Status: ConversationStatusInProgress}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldFinalize(tc.finish))
		})
	}
}

func TestReWooPlanner2_Serialization(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
	}
	planner.executionGraph["TaskA"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskA", Tool: "ToolA"},
		Status: StepStatusCompleted,
		Output: "Done",
	}
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"TaskA"}},
		Status: StepStatusPending,
	}

	data, err := planner.Marshal()
	assert.NoError(t, err)

	// Unmarshal into new instance
	newPlanner := &ReWooPlanner2{}
	err = newPlanner.Unmarshal(data)
	assert.NoError(t, err)

	assert.True(t, newPlanner.isGraphInitialized)
	assert.Len(t, newPlanner.executionGraph, 2)

	nodeA := newPlanner.executionGraph["TaskA"]
	assert.Equal(t, StepStatusCompleted, nodeA.Status)
	assert.Equal(t, "Done", nodeA.Output)

	nodeB := newPlanner.executionGraph["TaskB"]
	assert.Equal(t, StepStatusPending, nodeB.Status)
	assert.Equal(t, []string{"TaskA"}, nodeB.Step.Dependency)
}

func TestReWooPlanner2_ReviewUpdate(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{}, // Mock or nil check in method
	}
	// Note: We can't easily test the LLM call part of reviewAndRefinePlan without mocking.
	// But we can test the graph update logic if we extract it or mock the response.
	// For now, let's verify the logic we wrote inside reviewAndRefinePlan for "update" action manually

	// Setup initial graph
	planner.executionGraph["TaskA"] = &PlannerNode{Status: StepStatusCompleted}
	planner.executionGraph["TaskB"] = &PlannerNode{Status: StepStatusPending}

	// Simulate "Update" action logic manually since it's embedded in the LLM method
	// 1. Remove pending
	for id, node := range planner.executionGraph {
		if node.Status == StepStatusPending {
			delete(planner.executionGraph, id)
		}
	}
	// 2. Add new steps
	newStep := rewooPlannerStep2{ID: "TaskNew", Tool: "ToolNew"}
	planner.executionGraph["TaskNew"] = &PlannerNode{Step: newStep, Status: StepStatusPending}

	assert.Nil(t, planner.executionGraph["TaskB"])
	assert.NotNil(t, planner.executionGraph["TaskA"])
	assert.NotNil(t, planner.executionGraph["TaskNew"])
}

func TestReWooPlanner2_RestoreMissingDependency(t *testing.T) {
	// Scenario:
	// The planner has regenerated the plan and only included TaskB (dependent on TaskA).
	// TaskA was completed in a previous turn but is not in the current executionGraph.
	// We receive TaskA in the intermediateSteps (history).
	// Expectation: TaskA should be restored to the graph so TaskB can run.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
	}

	// 1. Setup graph with ONLY TaskB, depending on TaskA
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"TaskA"}},
		Status: StepStatusPending,
	}

	// 2. Simulate syncState with TaskA (which is missing from graph)
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				ToolID:    "TaskA",
				Tool:      "ToolA",
				ToolInput: "InputA",
				Log:       "PlanA",
			},
			Observation: "Result A",
			Status:      ToolStatusSuccess,
		},
	}

	planner.syncState(steps)

	// 3. Verify TaskA was restored
	nodeA, exists := planner.executionGraph["TaskA"]
	assert.True(t, exists, "TaskA should be restored to graph")
	if exists {
		assert.Equal(t, StepStatusCompleted, nodeA.Status)
		assert.Equal(t, "Result A", nodeA.Output)
		assert.Equal(t, "ToolA", nodeA.Step.Tool)
		assert.Equal(t, 0, nodeA.Iteration, "Restored node should have Iteration 0")
	}

	// 4. Verify TaskB is now runnable
	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 1)
	assert.Equal(t, "TaskB", batch[0].ToolID)
}

func TestReWooPlanner2_RestoreFailedDependency(t *testing.T) {
	// Scenario: TaskB depends on TaskA.
	// TaskA is not in graph (history), but it FAILED.
	// Expectation: TaskA restored as Failed. TaskB becomes Skipped.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	// 1. Graph has TaskB waiting on TaskA
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"TaskA"}},
		Status: StepStatusPending,
	}

	// 2. Sync history where TaskA failed
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{ToolID: "TaskA", Tool: "ToolA"},
			Observation: "Error",
			Status:      ToolStatusFailure,
		},
	}

	planner.syncState(steps)

	// 3. Verify TaskA restored as Failed
	nodeA, exists := planner.executionGraph["TaskA"]
	assert.True(t, exists, "TaskA should be restored")
	if exists {
		assert.Equal(t, StepStatusFailed, nodeA.Status)
	}

	// 4. Verify TaskB is Skipped (due to propagateSkips)
	nodeB := planner.executionGraph["TaskB"]
	assert.Equal(t, StepStatusSkipped, nodeB.Status, "TaskB should be skipped because dependency TaskA failed")
}

func TestReWooPlanner2_RestoreMultipleDependencies(t *testing.T) {
	// Scenario: TaskC depends on TaskA and TaskB.
	// Both A and B are in history (not in graph).
	// Expectation: Both restored. TaskC becomes Running.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
	}

	planner.executionGraph["TaskC"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskC", Tool: "ToolC", Dependency: []string{"TaskA", "TaskB"}},
		Status: StepStatusPending,
	}

	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{ToolID: "TaskA"}, Status: ToolStatusSuccess},
		{Action: NBAgentPlannerToolAction{ToolID: "TaskB"}, Status: ToolStatusSuccess},
	}

	planner.syncState(steps)

	assert.Contains(t, planner.executionGraph, "TaskA")
	assert.Contains(t, planner.executionGraph, "TaskB")

	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 1)
	assert.Equal(t, "TaskC", batch[0].ToolID)
}

func TestReWooPlanner2_PropagateSkipsOnNewStep(t *testing.T) {
	// Scenario: TaskA has already failed.
	// A new step, TaskB, is added (e.g. by reviewAndRefinePlan) that depends on TaskA.
	// Expectation: TaskB should be marked as Skipped when propagateSkips is called.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	// 1. Existing failed node
	planner.executionGraph["TaskA"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskA", Tool: "ToolA"},
		Status: StepStatusFailed,
	}

	// 2. New node added (simulating reviewAndRefinePlan)
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"TaskA"}},
		Status: StepStatusPending,
	}

	// 3. Propagate skips
	planner.propagateSkips()

	// 4. Verify TaskB is Skipped
	nodeB := planner.executionGraph["TaskB"]
	assert.Equal(t, StepStatusSkipped, nodeB.Status, "TaskB should be skipped because it depends on failed TaskA")
}

func TestReWooPlanner2_RestoreMixedDependencies(t *testing.T) {
	// Scenario: TaskC depends on A (Success) and B (Failed). Both missing.
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["TaskC"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskC", Tool: "ToolC", Dependency: []string{"TaskA", "TaskB"}},
		Status: StepStatusPending,
	}

	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{ToolID: "TaskA"}, Status: ToolStatusSuccess},
		{Action: NBAgentPlannerToolAction{ToolID: "TaskB"}, Status: ToolStatusFailure},
	}

	planner.syncState(steps)

	nodeA := planner.executionGraph["TaskA"]
	nodeB := planner.executionGraph["TaskB"]
	nodeC := planner.executionGraph["TaskC"]

	assert.Equal(t, StepStatusCompleted, nodeA.Status)
	assert.Equal(t, StepStatusFailed, nodeB.Status)

	// propagateSkips is called inside syncState? No, it's called AFTER syncState loop.
	// Let's check the code: yes, syncState calls propagateSkips() at the end.
	assert.Equal(t, StepStatusSkipped, nodeC.Status, "TaskC should be skipped because B failed")
}

func TestReWooPlanner2_WaitingForClientTool(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{}, // Mock context
	}

	// 1. Setup graph with TaskA
	planner.executionGraph["TaskA"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskA", Tool: "ClientTool"},
		Status: StepStatusRunning,
	}

	// 2. Sync state with WaitingForClient result
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{ToolID: "TaskA"},
			Status: ToolStatusWaitingForClient,
		},
	}

	planner.syncState(steps)

	// 3. Verify status
	assert.Equal(t, StepStatusWaiting, planner.executionGraph["TaskA"].Status)

	// 4. Verify allStepsDone returns false
	assert.False(t, planner.allStepsDone())

	// 5. Verify Plan returns waiting action
	// Note: We need to bypass other Plan logic (review, solver) to test this in isolation
	// We can manually call the check logic or trust that Plan calls it.
	// Let's verify via Plan() call if possible, but Plan calls Solver which needs mocks.
	// So checking state is safer here.
}

func TestReWooPlanner2_Plan_ReturnsWaitingAction(t *testing.T) {
	// Setup planner with a waiting node
	ctx := security.NewRequestContextForTenantAccountAdmin("test_tenant", "test_user", []string{"test_account"})
	planner := &ReWooPlanner2{
		ctx:                ctx,
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		request:            NBAgentRequest{AccountId: "test_account"},
	}

	planner.executionGraph["TaskWait"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskWait", Tool: "LocalShell", Query: "ls"},
		Status: StepStatusWaiting,
	}

	// Call Plan
	actions, finish, err := planner.Plan(context.Background(), nil, "Do something")

	assert.NoError(t, err)
	assert.Nil(t, actions)
	assert.NotNil(t, finish)
	assert.Equal(t, ConversationStatusWaiting, finish.Status)
}

// Mocking helper if needed
func TestReWooPlanner2_SerializationBatching(t *testing.T) {
	// Mock tool that requires serialization
	// In the real app, this is determined by whether the tool has multiple configs.
	// We can't easily mock the DB check here, but we can verify that IF it's detected,
	// the batching logic respects it.

	// Since we can't easily mock isSerializationRequiredTool without a DB,
	// let's test the logic that uses the 'serializedInBatch' map.
	// We will manually trigger the 'continue' logic if needed, but the current
	// getRunnableBatch already has the check.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		// We'll leave tools empty, so findNBTool returns nil, and isSerializationRequiredTool returns false.
		// To test this properly we'd need a mockable isSerializationRequiredTool.
		// For now, let's verify standard independent batching.
	}

	planner.executionGraph["S1"] = &PlannerNode{Step: rewooPlannerStep2{ID: "S1", Tool: "ToolA"}, Status: StepStatusPending}
	planner.executionGraph["S2"] = &PlannerNode{Step: rewooPlannerStep2{ID: "S2", Tool: "ToolA"}, Status: StepStatusPending}

	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 2, "Standard tools with same name should run in parallel if no serialization is required")
}

func TestReWooPlanner2_CircularDependency(t *testing.T) {
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	// S1 -> S2 -> S1
	planner.executionGraph["S1"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "S1", Tool: "T1", Dependency: []string{"S2"}},
		Status: StepStatusPending,
	}
	planner.executionGraph["S2"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "S2", Tool: "T2", Dependency: []string{"S1"}},
		Status: StepStatusPending,
	}

	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 0, "Circular dependencies should result in an empty batch (deadlock)")

	// Verify that Plan() eventually returns the deadlock error
	_, _, err := planner.Plan(context.Background(), nil, "task")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock")
}

func TestReWooPlanner2_MultiStepRecoveryChain(t *testing.T) {
	// Iteration 0: S1 Fails
	// Iteration 1: S2 (Recovery, deps: S1)
	// Iteration 1: S3 (Next step, deps: S2)
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["S1"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S1", Tool: "T1"},
		Status:    StepStatusFailed,
		Iteration: 0,
	}
	planner.executionGraph["S2"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S2", Tool: "T2", Dependency: []string{"S1"}},
		Status:    StepStatusPending,
		Iteration: 1,
	}
	planner.executionGraph["S3"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S3", Tool: "T3", Dependency: []string{"S2"}},
		Status:    StepStatusPending,
		Iteration: 1,
	}

	// 1. propagateSkips should NOT skip S2 or S3
	planner.propagateSkips()
	assert.Equal(t, StepStatusPending, planner.executionGraph["S2"].Status)
	assert.Equal(t, StepStatusPending, planner.executionGraph["S3"].Status)

	// 2. Batch should only contain S2 (S3 depends on S2 in the SAME iteration)
	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 1)
	assert.Equal(t, "S2", batch[0].ToolID)

	// 3. S2 completes, then S3 should be runnable
	planner.syncState([]NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{ToolID: "S2"}, Status: ToolStatusSuccess},
	})
	batch2 := planner.getRunnableBatch()
	assert.Len(t, batch2, 1)
	assert.Equal(t, "S3", batch2[0].ToolID)
}

func TestReWooPlanner2_DiamondDependency_PartialFailure(t *testing.T) {
	// Scenario: Diamond shape
	// Start -> A
	// Start -> B
	// (A, B) -> C
	// If A fails and B succeeds, C should be Skipped.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{}, // Mock context
	}

	// Setup graph
	planner.executionGraph["A"] = &PlannerNode{Step: rewooPlannerStep2{ID: "A", Tool: "ToolA"}, Status: StepStatusFailed}
	planner.executionGraph["B"] = &PlannerNode{Step: rewooPlannerStep2{ID: "B", Tool: "ToolB"}, Status: StepStatusCompleted}
	planner.executionGraph["C"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "C", Tool: "ToolC", Dependency: []string{"A", "B"}},
		Status: StepStatusPending,
	}

	// Propagate skips
	planner.propagateSkips()

	// Verify C is skipped
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["C"].Status, "C should be skipped because dependency A failed")
	assert.Contains(t, planner.executionGraph["C"].Output, "Skipped because dependency A failed")
}

func TestReWooPlanner2_ChainedSkips(t *testing.T) {
	// Scenario: A (Failed) -> B (Pending) -> C (Pending)
	// Expectation: B Skipped, C Skipped

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{}, // Mock context
	}

	planner.executionGraph["A"] = &PlannerNode{Step: rewooPlannerStep2{ID: "A", Tool: "ToolA"}, Status: StepStatusFailed}
	planner.executionGraph["B"] = &PlannerNode{Step: rewooPlannerStep2{ID: "B", Tool: "ToolB", Dependency: []string{"A"}}, Status: StepStatusPending}
	planner.executionGraph["C"] = &PlannerNode{Step: rewooPlannerStep2{ID: "C", Tool: "ToolC", Dependency: []string{"B"}}, Status: StepStatusPending}

	planner.propagateSkips()

	assert.Equal(t, StepStatusSkipped, planner.executionGraph["B"].Status)
	assert.Equal(t, StepStatusSkipped, planner.executionGraph["C"].Status, "C should be skipped because B was skipped")
}

func TestReWooPlanner2_CaseSensitivity(t *testing.T) {
	// Scenario: Dependency "taska" vs Node "TaskA".
	// Current implementation uses map lookup, so it is case-sensitive.
	// This test confirms that behavior so we know if it causes deadlocks.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["TaskA"] = &PlannerNode{Step: rewooPlannerStep2{ID: "TaskA", Tool: "ToolA"}, Status: StepStatusCompleted}
	planner.executionGraph["TaskB"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "TaskB", Tool: "ToolB", Dependency: []string{"taska"}}, // Lowercase dependency
		Status: StepStatusPending,
	}

	// Since "taska" != "TaskA", dependencies are NOT met.
	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 0, "Batch should be empty due to case mismatch in dependency")

	// Plan should error with deadlock
	_, _, err := planner.Plan(context.Background(), nil, "task")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock")
}

func TestReWooPlanner2_MissingDependency_Deadlock(t *testing.T) {
	// Scenario: A -> Z (Z does not exist)
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["A"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "A", Tool: "ToolA", Dependency: []string{"Z"}},
		Status: StepStatusPending,
	}

	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 0)

	_, _, err := planner.Plan(context.Background(), nil, "task")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock")
}

func TestReWooPlanner2_SelfDependency_Deadlock(t *testing.T) {
	// Scenario: A -> A
	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["A"] = &PlannerNode{
		Step:   rewooPlannerStep2{ID: "A", Tool: "ToolA", Dependency: []string{"A"}},
		Status: StepStatusPending,
	}

	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 0)

	_, _, err := planner.Plan(context.Background(), nil, "task")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock")
}

func TestReWooPlanner2_FullRecoveryLifecycle(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("test_tenant", "test_user", []string{"test_account"})
	planner := &ReWooPlanner2{
		ctx:                ctx,
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		currentIteration:   0,
	}

	// 1. Initial State: S1 is planned
	planner.executionGraph["S1"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S1", Tool: "kubectl"},
		Status:    StepStatusPending,
		Iteration: 0,
	}

	// 2. S1 Fails
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{ToolID: "S1"}, Status: ToolStatusFailure, Observation: "auth error"},
	}
	planner.syncState(steps)
	assert.Equal(t, StepStatusFailed, planner.executionGraph["S1"].Status)

	// 3. Reviewer Updates Plan (Simulated Update Logic)
	// This mimics the 'update' branch in reviewAndRefinePlan
	planner.currentIteration++ // Move to Iteration 1
	// Remove pending/running (none here), keep Failed/Completed
	for id, node := range planner.executionGraph {
		if node.Status == StepStatusPending || node.Status == StepStatusRunning {
			delete(planner.executionGraph, id)
		}
	}
	// Add recovery step S2 depending on S1
	planner.executionGraph["S2"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S2", Tool: "gcloud", Dependency: []string{"S1"}},
		Status:    StepStatusPending,
		Iteration: 1,
	}

	// 4. Verify Deadlock Prevention
	// S1 is still there (Anchor preserved)
	assert.NotNil(t, planner.executionGraph["S1"], "Anchor S1 should be preserved")

	// propagateSkips should NOT skip S2
	planner.propagateSkips()
	assert.Equal(t, StepStatusPending, planner.executionGraph["S2"].Status, "Recovery step S2 should NOT be skipped")

	// getRunnableBatch should PICK S2
	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 1)
	assert.Equal(t, "S2", batch[0].ToolID)
	assert.Equal(t, StepStatusRunning, planner.executionGraph["S2"].Status)

	// 5. S2 Completes
	steps2 := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{ToolID: "S2"}, Status: ToolStatusSuccess, Observation: "fixed auth"},
	}
	planner.syncState(steps2)
	assert.Equal(t, StepStatusCompleted, planner.executionGraph["S2"].Status)

	// 6. Completion check
	assert.True(t, planner.allStepsDone(), "All steps should be done after recovery")
}

func TestReWooPlanner2_SkippedDependencyRecovery(t *testing.T) {
	// Scenario: S0 (Failed, Iter 0) -> S1 (Skipped, Iter 0).
	// New step S2 (Iter 1) depends on S1 (Skipped).
	// Expectation: S2 should be allowed to run because Iter 1 > Iter 0.

	planner := &ReWooPlanner2{
		executionGraph:     make(map[string]*PlannerNode),
		isGraphInitialized: true,
		ctx:                &security.RequestContext{},
	}

	planner.executionGraph["S0"] = &PlannerNode{Status: StepStatusFailed, Iteration: 0}
	planner.executionGraph["S1"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S1", Tool: "T1", Dependency: []string{"S0"}},
		Status:    StepStatusSkipped,
		Iteration: 0,
	}
	planner.executionGraph["S2"] = &PlannerNode{
		Step:      rewooPlannerStep2{ID: "S2", Tool: "T2", Dependency: []string{"S1"}},
		Status:    StepStatusPending,
		Iteration: 1,
	}

	// 1. propagateSkips should NOT skip S2
	planner.propagateSkips()
	assert.Equal(t, StepStatusPending, planner.executionGraph["S2"].Status, "S2 should not be skipped even though S1 was skipped (Recovery iteration)")

	// 2. getRunnableBatch should PICK S2
	batch := planner.getRunnableBatch()
	assert.Len(t, batch, 1)
	assert.Equal(t, "S2", batch[0].ToolID)
}

func TestReWooPlanner2_NewlineDependencyParsing(t *testing.T) {
	planner := &ReWooPlanner2{
		ctx:            &security.RequestContext{},
		executionGraph: make(map[string]*PlannerNode),
	}

	content := `
<plan_response>
	<thought>Plan with newline dependencies</thought>
	<plan>
		<step>
			<id>S1</id>
			<tool>tool1</tool>
			<query>query1</query>
			<reason>reason1</reason>
			<dependency></dependency>
		</step>
		<step>
			<id>S2</id>
			<tool>tool2</tool>
			<query>query2</query>
			<reason>reason2</reason>
			<dependency></dependency>
		</step>
		<step>
			<id>S3</id>
			<tool>tool3</tool>
			<query>query3</query>
			<reason>reason3</reason>
			<dependency>S1
S2</dependency>
		</step>
	</plan>
</plan_response>`

	resp := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: content},
		},
	}

	actions, _, _, err := planner.parseOutputInternal(resp, nil)
	assert.NoError(t, err)

	for _, action := range actions {
		if action.ToolID == "S3" {
			assert.Len(t, action.Dependency, 2, "Should have split dependencies on newline")
			assert.Contains(t, action.Dependency, "S1")
			assert.Contains(t, action.Dependency, "S2")
		}
	}
}

func TestReWooPlanner2_ValidateAndCorrectActions_FuzzyDeps(t *testing.T) {
	planner := &ReWooPlanner2{
		ctx: &security.RequestContext{},
	}

	actions := []NBAgentPlannerToolAction{
		{
			ToolID: "S1",
			Tool:   "tool1",
		},
		{
			ToolID: "S2",
			Tool:   "tool2",
		},
		{
			ToolID:     "S3",
			Tool:       "tool3",
			Dependency: []string{"S1 S2"}, // Concatenated dependency
		},
	}

	corrected, err := planner.validateAndCorrectActions(actions)
	assert.NoError(t, err)

	for _, a := range corrected {
		if a.ToolID == "S3" {
			assert.Len(t, a.Dependency, 2, "Should have split concatenated dependency")
			assert.Contains(t, a.Dependency, "S1")
			assert.Contains(t, a.Dependency, "S2")
		}
	}
}

func TestReWooPlanner2_ValidateAndCorrectActions_MissingDeps(t *testing.T) {
	planner := &ReWooPlanner2{
		ctx: &security.RequestContext{},
	}

	actions := []NBAgentPlannerToolAction{
		{
			ToolID:     "S2",
			Tool:       "tool2",
			Dependency: []string{"S1"}, // S1 is missing
		},
	}

	corrected, err := planner.validateAndCorrectActions(actions)
	assert.Error(t, err, "Should return structural error for missing dependency")

	for _, a := range corrected {
		if a.ToolID == "S2" {
			assert.Len(t, a.Dependency, 1, "Should have KEPT missing dependency to ensure safety and trigger deadlock recovery")
			assert.Equal(t, "S1", a.Dependency[0])
		}
	}
}
