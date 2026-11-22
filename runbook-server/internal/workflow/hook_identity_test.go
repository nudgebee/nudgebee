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

func TestHookActionsCarryTheRunsIdentity(t *testing.T) {
	// Hooks are activities like any step and pass the same per-step check, so
	// they need the run's tenant, account, workflow and principal. Before this,
	// they carried none of them.
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	var got map[string]any
	env.RegisterActivityWithOptions(func(_ context.Context, params map[string]any) (any, error) {
		got = params
		return nil, nil
	}, activity.RegisterOptions{Name: "notifications.im"})

	wf := &model.Workflow{ID: "wf-1", TenantID: "t1", AccountID: "acct-a", Name: "nightly", UpdatedBy: "last-editor", PrincipalUserID: "owner"}
	env.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
		return executeSimpleActions(ctx, []model.Action{{Type: "notifications.im", Params: map[string]any{"text": "done"}}}, NewTemplateContext(nil, nil), wf)
	}, workflow.RegisterOptions{Name: "hookHarness"})

	env.ExecuteWorkflow("hookHarness")
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	assert.Equal(t, "t1", got[tasks.ParamTenantID])
	assert.Equal(t, "acct-a", got[tasks.ParamAccountID])
	assert.Equal(t, "wf-1", got[tasks.ParamWorkflowID])
	assert.Equal(t, "owner", got[tasks.ParamRunAsUserID])
	assert.Equal(t, "done", got["text"])
}
