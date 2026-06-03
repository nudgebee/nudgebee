package workflow

import (
	"context"
	"fmt"
	"testing"
	//"time" // Not directly used in this test file

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"nudgebee/runbook/internal/model"
	// "nudgebee/runbook/internal/tasks/core" // Not directly imported here, but types used.
	// "nudgebee/runbook/internal/tasks/types" // Not directly imported here, but types used.
)

type ExecutorTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ExecutorTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *ExecutorTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestExecutorTestSuite(t *testing.T) {
	suite.Run(t, new(ExecutorTestSuite))
}

// switchFanInWorkflow builds a workflow with the shape that previously stalled:
// a `core.switch` fans out to three leaf tasks, all of which are listed in the
// downstream `converge` task's DependsOn. Caller picks the case value to force
// a specific branch.
func switchFanInWorkflow(caseValue string) *model.Workflow {
	return &model.Workflow{
		ID:        "test-switch-fanin-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "router",
					Type: "core.switch",
					Params: map[string]any{
						"expression":   caseValue,
						"default_next": "leaf_default",
						"cases": []any{
							map[string]any{"value": "a", "next": "leaf_a"},
							map[string]any{"value": "b", "next": "leaf_b"},
							map[string]any{"value": "c", "next": "leaf_c"},
						},
					},
				},
				{ID: "leaf_a", Type: "core.print", Params: map[string]any{"message": "ran-leaf_a"}},
				{ID: "leaf_b", Type: "core.print", Params: map[string]any{"message": "ran-leaf_b"}},
				{ID: "leaf_c", Type: "core.print", Params: map[string]any{"message": "ran-leaf_c"}},
				{ID: "leaf_default", Type: "core.print", Params: map[string]any{"message": "ran-default"}},
				{
					ID:        "converge",
					Type:      "data.transform",
					DependsOn: []string{"leaf_a", "leaf_b", "leaf_c", "leaf_default"},
					Params: map[string]any{
						"expression": "joined:" +
							"{{ Tasks['leaf_a'].status }}/{{ Tasks['leaf_a'].output.data | default('-') }}|" +
							"{{ Tasks['leaf_b'].status }}/{{ Tasks['leaf_b'].output.data | default('-') }}|" +
							"{{ Tasks['leaf_c'].status }}/{{ Tasks['leaf_c'].output.data | default('-') }}|" +
							"{{ Tasks['leaf_default'].status }}/{{ Tasks['leaf_default'].output.data | default('-') }}",
					},
				},
			},
			Output: map[string]any{
				"converge": "{{ Tasks['converge'].output.data }}",
			},
		},
	}
}

func (s *ExecutorTestSuite) registerFanInActivities() {
	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		msg, _ := params["message"].(string)
		return map[string]string{"data": msg}, nil
	}, activity.RegisterOptions{Name: "core.print"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		return map[string]any{"data": expr}, nil
	}, activity.RegisterOptions{Name: "data.transform"})

	// core.switch is dispatched as an activity; mirror SwitchTask.Execute's
	// shape (selected_case + routed_to) so the executor can build the inline
	// branch definition from the result.
	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		out := map[string]any{"routed_to": []string{}}
		if cases, ok := params["cases"].([]any); ok {
			for _, c := range cases {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				val := fmt.Sprintf("%v", cm["value"])
				if val != expr {
					continue
				}
				out["selected_case"] = val
				if next, _ := cm["next"].(string); next != "" {
					out["routed_to"] = []string{next}
				}
				return out, nil
			}
		}
		if defaultNext, _ := params["default_next"].(string); defaultNext != "" {
			out["selected_case"] = "default"
			out["routed_to"] = []string{defaultNext}
		}
		return out, nil
	}, activity.RegisterOptions{Name: "core.switch"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})
}

func (s *ExecutorTestSuite) newFanInExecutor() *WorkflowExecutor {
	mockStore := new(MockWorkflowStore)
	mockStore.On("GetState", mock.Anything, mock.Anything).Return([]model.WorkflowStateItem{}, nil).Maybe()
	mockStore.On("SetLastExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return &WorkflowExecutor{workflowStore: mockStore}
}

// TestSwitchFanOutFanInConvergence reproduces the original bug: a switch fans
// out to three branches that converge into one downstream task. Before the fix,
// the parent loop never marked branch IDs complete (selected or otherwise), so
// the join's dependency check stalled forever. Asserts the selected branch
// produced output the join can read, and the unselected branches are SKIPPED.
func (s *ExecutorTestSuite) TestSwitchFanOutFanInConvergence() {
	s.registerFanInActivities()
	executor := s.newFanInExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, switchFanInWorkflow("b"), nil)

	s.True(s.env.IsWorkflowCompleted(), "workflow should complete (no stall)")
	s.NoError(s.env.GetWorkflowError())

	var resultStr string
	s.NoError(s.env.GetWorkflowResult(&resultStr))
	// Selected branch (b) carries its output; the other three are SKIPPED with no output.
	s.JSONEq(
		`{"converge":"joined:SKIPPED/-|COMPLETED/ran-leaf_b|SKIPPED/-|SKIPPED/-"}`,
		resultStr,
	)
}

// TestSwitchDefaultBranchFanIn covers the default branch path when no case
// matches. Mirrors the above shape but routes through `default_next`.
func (s *ExecutorTestSuite) TestSwitchDefaultBranchFanIn() {
	s.registerFanInActivities()
	executor := s.newFanInExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, switchFanInWorkflow("nope"), nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var resultStr string
	s.NoError(s.env.GetWorkflowResult(&resultStr))
	s.JSONEq(
		`{"converge":"joined:SKIPPED/-|SKIPPED/-|SKIPPED/-|COMPLETED/ran-default"}`,
		resultStr,
	)
}

// TestSwitchFanInAllSkippedSkipsDownstream locks in the all-skipped propagation
// rule. If every dependency is SKIPPED — e.g. because the converge task's
// `If` is empty and every parent was a non-selected switch branch (no case
// matched and no default) — the converge task should also be SKIPPED. Here we
// test the simpler form: every parent is a disabled task.
func (s *ExecutorTestSuite) TestSwitchFanInAllSkippedSkipsDownstream() {
	s.registerFanInActivities()
	executor := s.newFanInExecutor()

	wf := &model.Workflow{
		ID:        "test-all-skipped-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{ID: "p1", Type: "core.print", Disabled: true, Params: map[string]any{"message": "x"}},
				{ID: "p2", Type: "core.print", Disabled: true, Params: map[string]any{"message": "y"}},
				{
					ID:        "joined",
					Type:      "data.transform",
					DependsOn: []string{"p1", "p2"},
					Params:    map[string]any{"expression": "should-not-run"},
				},
			},
			Output: map[string]any{
				"joined_status": "{{ Tasks['joined'].status }}",
			},
		},
	}

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var resultStr string
	s.NoError(s.env.GetWorkflowResult(&resultStr))
	s.JSONEq(`{"joined_status":"SKIPPED"}`, resultStr)
}

// TestSwitchUnselectedBranchSkipsDependent covers the regression report from
// the SwitchFanInFanOut workflow: a downstream task whose ONLY dependency is
// an unselected switch branch must be SKIPPED. Before the fix, the dep-check
// unconditionally exempted switch branches from skip-propagation, so the
// downstream task ran with empty input from the SKIPPED branch.
//
// Shape mirrors the user's workflow: switch routes to leaf_selected (case "one"),
// leaf_unselected is the case "two" target, and tail depends only on
// leaf_unselected.
func (s *ExecutorTestSuite) TestSwitchUnselectedBranchSkipsDependent() {
	s.registerFanInActivities()
	executor := s.newFanInExecutor()

	wf := &model.Workflow{
		ID:        "test-unselected-dep-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "router",
					Type: "core.switch",
					Params: map[string]any{
						"expression":   "one",
						"default_next": "",
						"cases": []any{
							map[string]any{"value": "one", "next": "leaf_selected"},
							map[string]any{"value": "two", "next": "leaf_unselected"},
						},
					},
				},
				{ID: "leaf_selected", Type: "core.print", Params: map[string]any{"message": "ran-selected"}},
				{ID: "leaf_unselected", Type: "core.print", Params: map[string]any{"message": "ran-unselected"}},
				{
					ID:        "tail",
					Type:      "core.print",
					DependsOn: []string{"leaf_unselected"},
					Params:    map[string]any{"message": "tail-should-not-run"},
				},
			},
			Output: map[string]any{
				"selected_status":   "{{ Tasks['leaf_selected'].status }}",
				"unselected_status": "{{ Tasks['leaf_unselected'].status }}",
				"tail_status":       "{{ Tasks['tail'].status }}",
			},
		},
	}

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted(), "workflow should complete")
	s.NoError(s.env.GetWorkflowError())

	var resultStr string
	s.NoError(s.env.GetWorkflowResult(&resultStr))
	s.JSONEq(
		`{"selected_status":"COMPLETED","unselected_status":"SKIPPED","tail_status":"SKIPPED"}`,
		resultStr,
	)
}

// registerSystemActivities registers no-op stubs for the internal Temporal
// activities every workflow runs (status updates, state fetches, config
// fetches). Used by the shape-based switch dispatch tests.
func (s *ExecutorTestSuite) registerSystemActivities() {
	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})
	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string, stateUpdates map[string]model.StateUpdateDTO, executionID string, taskID string) error {
		return nil
	}, activity.RegisterOptions{Name: "UpdateWorkflowStateActivity"})
}

// runSwitchWorkflow registers a real-shape core.switch activity (returning the
// plain map[string]any the executor reads via buildRoutingDef) plus a core.print
// branch activity, runs the workflow, and reports which branches fired their
// print. The map shape (selected_case / routed_to) is the contract — keep
// stable.
func (s *ExecutorTestSuite) runSwitchWorkflow(wf *model.Workflow) ([]string, error) {
	var firedBranches []string

	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		if msg, ok := params["message"].(string); ok {
			firedBranches = append(firedBranches, msg)
		}
		return "printed", nil
	}, activity.RegisterOptions{Name: "core.print"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		out := map[string]any{"routed_to": []string{}}
		if cases, ok := params["cases"].([]any); ok {
			for _, c := range cases {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				val, _ := cm["value"].(string)
				if val != expr {
					continue
				}
				out["selected_case"] = val
				if next, _ := cm["next"].(string); next != "" {
					out["routed_to"] = []string{next}
				}
				return out, nil
			}
		}
		if defaultNext, _ := params["default_next"].(string); defaultNext != "" {
			out["selected_case"] = "default"
			out["routed_to"] = []string{defaultNext}
		}
		return out, nil
	}, activity.RegisterOptions{Name: "core.switch"})

	s.registerSystemActivities()

	mockStore := new(MockWorkflowStore)
	mockStore.On("GetState", mock.Anything, mock.Anything).Return([]model.WorkflowStateItem{}, nil).Maybe()
	mockStore.On("SetLastExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	executor := &WorkflowExecutor{workflowStore: mockStore}
	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	if !s.env.IsWorkflowCompleted() {
		return firedBranches, nil
	}
	return firedBranches, s.env.GetWorkflowError()
}

func switchTestWorkflow(switchValue, defaultNext string) *model.Workflow {
	return &model.Workflow{
		ID:        "switch-wf",
		TenantID:  "tenant",
		AccountID: "account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "my-switch",
					Type: "core.switch",
					Params: map[string]any{
						"expression": switchValue,
						"cases": []any{
							map[string]any{"value": "A", "next": "task-a"},
							map[string]any{"value": "B", "next": "task-b"},
						},
						"default_next": defaultNext,
					},
				},
				{ID: "task-a", Type: "core.print", Params: map[string]any{"message": "branch-A"}},
				{ID: "task-b", Type: "core.print", Params: map[string]any{"message": "branch-B"}},
				{ID: "task-c", Type: "core.print", Params: map[string]any{"message": "branch-default"}},
			},
		},
	}
}

// Switch dispatched as an activity routes to the matched case's branch;
// task-b/task-c must NOT run (they're filtered by the case-routing shape).
func (s *ExecutorTestSuite) TestSwitchActivityDispatch_MatchedCase() {
	fired, err := s.runSwitchWorkflow(switchTestWorkflow("A", "task-c"))
	s.NoError(err)
	s.True(s.env.IsWorkflowCompleted())
	s.Equal([]string{"branch-A"}, fired, "only the matched branch should fire")
}

// When no case matches, the switch routes to default_next.
func (s *ExecutorTestSuite) TestSwitchActivityDispatch_DefaultBranch() {
	fired, err := s.runSwitchWorkflow(switchTestWorkflow("Z", "task-c"))
	s.NoError(err)
	s.True(s.env.IsWorkflowCompleted())
	s.Equal([]string{"branch-default"}, fired, "the default branch should fire")
}

// No case matches and default_next is empty → no branch runs but the workflow
// still completes successfully because the switch activity returned cleanly.
// The wfDef must only contain branch tasks reachable via the switch's cases or
// default_next; otherwise a stray top-level task with no depends_on would run
// on its own (expected workflow behaviour, not part of routing).
func (s *ExecutorTestSuite) TestSwitchActivityDispatch_NoMatchEmptyDefault() {
	wf := &model.Workflow{
		ID:        "switch-wf",
		TenantID:  "tenant",
		AccountID: "account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{
					ID:   "my-switch",
					Type: "core.switch",
					Params: map[string]any{
						"expression": "Z",
						"cases": []any{
							map[string]any{"value": "A", "next": "task-a"},
							map[string]any{"value": "B", "next": "task-b"},
						},
						"default_next": "",
					},
				},
				{ID: "task-a", Type: "core.print", Params: map[string]any{"message": "branch-A"}},
				{ID: "task-b", Type: "core.print", Params: map[string]any{"message": "branch-B"}},
			},
		},
	}
	fired, err := s.runSwitchWorkflow(wf)
	s.NoError(err)
	s.True(s.env.IsWorkflowCompleted())
	s.Empty(fired, "no branch should fire when nothing matches and default is empty")
}

func (s *ExecutorTestSuite) TestExecuteWorkflow_NilInputs() {
	// Register mock activities
	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string, stateUpdates map[string]model.StateUpdateDTO, executionID string, taskID string) error {
		return nil
	}, activity.RegisterOptions{Name: "UpdateWorkflowStateActivity"})

	mockStore := new(MockWorkflowStore)
	mockStore.On("GetState", mock.Anything, mock.Anything).Return([]model.WorkflowStateItem{}, nil).Maybe()
	mockStore.On("SetLastExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	executor := &WorkflowExecutor{
		workflowStore: mockStore,
	}

	wf := &model.Workflow{
		ID:        "test-nil-inputs-wf",
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{}, // Empty tasks
		},
	}

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)

	// Execute with nil inputs
	// Note: We need to pass nil explicitly as the second argument
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}
