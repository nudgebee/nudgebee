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

// A step's own params must never choose whose rights it acts with: a run with no
// recorded owner would otherwise act as whatever user (an admin, a bee account)
// the definition names in _run_as_user_id.

func TestHookActionParamsCannotNameThePrincipal(t *testing.T) {
	for _, tc := range []struct{ principal, want string }{{"", ""}, {"owner", "owner"}} {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		var got map[string]any
		env.RegisterActivityWithOptions(func(_ context.Context, params map[string]any) (any, error) {
			got = params
			return nil, nil
		}, activity.RegisterOptions{Name: "notifications.im"})

		wf := &model.Workflow{ID: "wf-1", TenantID: "t1", AccountID: "acct-a", UpdatedBy: "last-editor", PrincipalUserID: tc.principal}
		action := model.Action{Type: "notifications.im", Params: map[string]any{"text": "done", tasks.ParamRunAsUserID: "someone-else"}}
		env.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
			return executeSimpleActions(ctx, []model.Action{action}, NewTemplateContext(nil, nil), wf)
		}, workflow.RegisterOptions{Name: "hookHarness"})

		env.ExecuteWorkflow("hookHarness")
		require.NoError(t, env.GetWorkflowError())
		principal, present := got[tasks.ParamRunAsUserID]
		if tc.want == "" {
			assert.False(t, present, "a hook's params must not set the principal, got %v", principal)
		} else {
			assert.Equal(t, tc.want, principal)
		}
	}
}

func (s *ExecutorTestSuite) TestStepParamsCannotNameThePrincipal() {
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

	// No recorded owner (e.g. it was deleted): the step must fall back to the
	// run's own identity, not to the id its params name.
	wf := &model.Workflow{
		ID: "wf-run-as", TenantID: "t1", AccountID: "acct-a", UpdatedBy: "last-editor",
		Definition: model.WorkflowDefinition{Tasks: []model.Task{
			{ID: "step", Type: "core.print", Params: map[string]any{"message": "hi", tasks.ParamRunAsUserID: "someone-else"}},
		}},
	}
	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	_, present := got[tasks.ParamRunAsUserID]
	s.False(present, "a step's params must not set the principal")
	s.Equal("last-editor", got[tasks.ParamUserID])
}
