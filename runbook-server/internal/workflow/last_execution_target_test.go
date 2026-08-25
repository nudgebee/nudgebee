package workflow

import (
	"context"
	"sync"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestResolveLastExecutionTarget(t *testing.T) {
	cases := []struct {
		name       string
		wfID       string
		startAttr  string
		wantTarget string
	}{
		{"top level run owns its own row", "8ad2-uuid", "", "8ad2-uuid"},
		{"called run owns the callee's row", "inline-call_child-1234", "callee-uuid", "callee-uuid"},
		{"synthesized child owns no row", "inline-foreach_pods-1234", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantTarget, resolveLastExecutionTarget(tc.wfID, tc.startAttr))
		})
	}
}

// statusWrite records one Internal_UpdateLastExecutionStatusActivity call.
type statusWrite struct {
	WorkflowID string
	Status     model.WorkflowExecutionStatus
}

// registerCalleeStatusActivities captures every last-execution status write so a test
// can assert which workflow row it landed on.
func (s *ExecutorTestSuite) registerCalleeStatusActivities(writes *[]statusWrite, mu *sync.Mutex) {
	s.env.RegisterActivityWithOptions(func(ctx context.Context, params map[string]any) (any, error) {
		msg, _ := params["message"].(string)
		return map[string]string{"data": msg}, nil
	}, activity.RegisterOptions{Name: "core.print"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, wf *model.Workflow, status model.WorkflowExecutionStatus, message string) error {
		mu.Lock()
		defer mu.Unlock()
		*writes = append(*writes, statusWrite{WorkflowID: wf.ID, Status: status})
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, tenantID, accountID string, restrictToAccount bool) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, typeRefID string, failed bool) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateEventResolutionStatusActivity"})

	s.env.RegisterActivityWithOptions(func(ctx context.Context, workflowID string, stateUpdates map[string]model.StateUpdateDTO, executionID, taskID string) error {
		return nil
	}, activity.RegisterOptions{Name: "UpdateWorkflowStateActivity"})
}

// inlineChildWorkflow mirrors what the executor hands to a child spawned by an
// inline task: a synthetic definition snapshot whose id matches no workflow row.
func inlineChildWorkflow(inlineID string) *model.Workflow {
	return &model.Workflow{
		ID:        inlineID,
		TenantID:  "test-tenant",
		AccountID: "test-account",
		Name:      "child-of-caller",
		Definition: model.WorkflowDefinition{
			Tasks: []model.Task{
				{ID: "leaf", Type: "core.print", Params: map[string]any{"message": "ran"}},
			},
		},
	}
}

func (s *ExecutorTestSuite) newStatusExecutor() *WorkflowExecutor {
	mockStore := new(MockWorkflowStore)
	mockStore.On("GetState", mock.Anything, mock.Anything).Return([]model.WorkflowStateItem{}, nil).Maybe()
	return &WorkflowExecutor{workflowStore: mockStore}
}

// callerWorkflow spawns the snapshot as a real Temporal child with the tags
// applyCalleeRunTags sets. Typed, because the test env drops untyped keys as Unspecified.
func callerWorkflow(ctx workflow.Context, child *model.Workflow, calleeWorkflowID string) error {
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "caller-uuid-call_child-1",
	}
	if calleeWorkflowID != "" {
		cwo.TypedSearchAttributes = temporal.NewSearchAttributes(
			temporal.NewSearchAttributeKeyKeyword(model.SearchAttrWorkflowID).ValueSet(calleeWorkflowID),
			temporal.NewSearchAttributeKeyKeyword(model.SearchAttrWorkflowTrigger).ValueSet(string(model.WorkflowTriggerCalled)),
		)
	}
	var out string
	return workflow.ExecuteChildWorkflow(workflow.WithChildOptions(ctx, cwo), "ExecuteWorkflowInternal", child, nil).Get(ctx, &out)
}

// A called run belongs on the callee's row — the row the listing's "Last Execution"
// column reads. Before the fix it wrote its "inline-…" id into a uuid-keyed UPDATE.
func (s *ExecutorTestSuite) TestCalledRunRecordsLastExecutionOnCallee() {
	var mu sync.Mutex
	var writes []statusWrite
	s.registerCalleeStatusActivities(&writes, &mu)
	executor := s.newStatusExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.RegisterWorkflow(callerWorkflow)
	s.env.ExecuteWorkflow(callerWorkflow, inlineChildWorkflow("inline-call_child-1234"), "callee-uuid")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(writes, 2, "a called run records RUNNING then its terminal status")
	s.Equal(statusWrite{WorkflowID: "callee-uuid", Status: model.WorkflowExecutionStatusRunning}, writes[0])
	s.Equal(statusWrite{WorkflowID: "callee-uuid", Status: model.WorkflowExecutionStatusCompleted}, writes[1])
}

// Children that synthesize their own definition (core.group, core.foreach) own no
// workflow row, so they must write no status at all.
func (s *ExecutorTestSuite) TestSynthesizedChildRecordsNoLastExecution() {
	var mu sync.Mutex
	var writes []statusWrite
	s.registerCalleeStatusActivities(&writes, &mu)
	executor := s.newStatusExecutor()

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.RegisterWorkflow(callerWorkflow)
	s.env.ExecuteWorkflow(callerWorkflow, inlineChildWorkflow("inline-foreach_pods-1234"), "")

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Empty(writes, "a synthesized child owns no workflow row")
}

// A top-level run keeps writing to its own row.
func (s *ExecutorTestSuite) TestTopLevelRunRecordsLastExecutionOnItself() {
	var mu sync.Mutex
	var writes []statusWrite
	s.registerCalleeStatusActivities(&writes, &mu)
	executor := s.newStatusExecutor()

	wf := inlineChildWorkflow("stored-wf-uuid")
	wf.Name = "restart-service"

	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(writes, 2)
	s.Equal(statusWrite{WorkflowID: "stored-wf-uuid", Status: model.WorkflowExecutionStatusRunning}, writes[0])
	s.Equal(statusWrite{WorkflowID: "stored-wf-uuid", Status: model.WorkflowExecutionStatusCompleted}, writes[1])
}
