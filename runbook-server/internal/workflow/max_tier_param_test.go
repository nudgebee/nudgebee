package workflow

import (
	"context"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// The tier ceiling comes from the definition only: a step's or hook's own params
// may neither set nor lift it.

func TestHookActionsCarryTheDefinitionsTierCeiling(t *testing.T) {
	for _, tc := range []struct {
		maxTier int
		want    any
	}{{0, nil}, {tasks.TierRead, tasks.TierRead}} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		var got map[string]any
		env.RegisterActivityWithOptions(func(_ context.Context, params map[string]any) (any, error) {
			got = params
			return nil, nil
		}, activity.RegisterOptions{Name: "notifications.im"})

		wf := &model.Workflow{ID: "wf-1", TenantID: "t1", AccountID: "acct-a", Definition: model.WorkflowDefinition{MaxTier: tc.maxTier}}
		action := model.Action{Type: "notifications.im", Params: map[string]any{"text": "done", tasks.ParamMaxTier: tasks.TierUnrestricted}}
		env.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
			return executeSimpleActions(ctx, []model.Action{action}, NewTemplateContext(nil, nil), wf)
		}, workflow.RegisterOptions{Name: "hookHarness"})

		env.ExecuteWorkflow("hookHarness")
		require.NoError(t, env.GetWorkflowError())
		ceiling, present := got[tasks.ParamMaxTier]
		if tc.want == nil {
			assert.False(t, present, "a hook's params must not set a ceiling, got %v", ceiling)
		} else {
			assert.EqualValues(t, tc.want, ceiling)
		}
	}
}

func (s *ExecutorTestSuite) TestStepsCarryTheDefinitionsTierCeiling() {
	var got map[string]any
	s.env.RegisterActivityWithOptions(func(_ context.Context, params map[string]any) (any, error) {
		got = params
		return map[string]string{"data": "ok"}, nil
	}, activity.RegisterOptions{Name: "core.print"})
	s.env.RegisterActivityWithOptions(func(context.Context, *model.Workflow, model.WorkflowExecutionStatus, string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})
	s.env.RegisterActivityWithOptions(func(context.Context, string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})
	s.env.RegisterActivityWithOptions(func(context.Context, string, string, bool) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})
	executor := s.newFanInExecutor()

	wf := &model.Workflow{
		ID: "wf-tier", TenantID: "t1", AccountID: "acct-a", UpdatedBy: "last-editor",
		Definition: model.WorkflowDefinition{MaxTier: tasks.TierRead, Tasks: []model.Task{
			{ID: "step", Type: "core.print", Params: map[string]any{"message": "hi", tasks.ParamMaxTier: tasks.TierUnrestricted}},
		}},
	}
	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.EqualValues(tasks.TierRead, got[tasks.ParamMaxTier], "the definition's ceiling, not the step's own")
}

// An inline step (here one that calls another automation) never reaches the
// task wrapper, so the executor checks it against the ceiling itself.
func (s *ExecutorTestSuite) TestInlineStepsAboveTheTierCeilingAreRefused() {
	s.env.RegisterActivityWithOptions(func(context.Context, *model.Workflow, model.WorkflowExecutionStatus, string) error {
		return nil
	}, activity.RegisterOptions{Name: "Internal_UpdateLastExecutionStatusActivity"})
	s.env.RegisterActivityWithOptions(func(context.Context, string) (map[string]any, error) {
		return map[string]any{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowStateActivity"})
	s.env.RegisterActivityWithOptions(func(context.Context, string, string, bool) (FetchConfigsResponse, error) {
		return FetchConfigsResponse{}, nil
	}, activity.RegisterOptions{Name: "FetchWorkflowConfigsActivity"})
	executor := s.newFanInExecutor()

	wf := &model.Workflow{
		ID: "wf-inline-tier", TenantID: "t1", AccountID: "acct-a", UpdatedBy: "last-editor",
		Definition: model.WorkflowDefinition{MaxTier: tasks.TierChange, Tasks: []model.Task{
			{ID: "call", Type: "core.call-workflow", Params: map[string]any{"workflow_name": "anything"}},
		}},
	}
	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "step core.call-workflow is tier 3, above this automation's tier 2")
}
