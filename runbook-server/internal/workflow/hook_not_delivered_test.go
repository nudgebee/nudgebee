package workflow

import (
	"context"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func runHookReturning(t *testing.T, hookErr error) error {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, _ map[string]any) (any, error) {
		return nil, hookErr
	}, activity.RegisterOptions{Name: "notifications.im"})
	env.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
		return executeSimpleActions(ctx, []model.Action{{Type: "notifications.im", Params: map[string]any{"message": "done"}}}, NewTemplateContext(nil, nil), &model.Workflow{ID: "wf-1", TenantID: "t1"})
	}, workflow.RegisterOptions{Name: "hookHarness"})

	env.ExecuteWorkflow("hookHarness")
	require.True(t, env.IsWorkflowCompleted())
	return env.GetWorkflowError()
}

func TestAHookMessageThatWasNotDeliveredDoesNotFailTheTask(t *testing.T) {
	err := runHookReturning(t, temporal.NewNonRetryableApplicationError("the message was not delivered (not_in_channel)", types.ErrTypeNotificationNotDelivered, nil))

	assert.NoError(t, err)
}

func TestAnyOtherHookErrorStillFailsTheTask(t *testing.T) {
	err := runHookReturning(t, temporal.NewNonRetryableApplicationError("template could not be rendered", "SomethingElse", nil))

	assert.Error(t, err)
}
