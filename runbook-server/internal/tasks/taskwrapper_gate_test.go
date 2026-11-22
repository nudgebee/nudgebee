package tasks

import (
	"context"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// approvalDouble stands in for core.approval: it records that it ran.
type approvalDouble struct{ ran *bool }

func (approvalDouble) GetName() string             { return "core.approval" }
func (approvalDouble) GetDescription() string      { return "test double" }
func (approvalDouble) GetDisplayName() string      { return "test double" }
func (approvalDouble) InputSchema() *types.Schema  { return nil }
func (approvalDouble) OutputSchema() *types.Schema { return nil }
func (a approvalDouble) Execute(types.TaskContext, map[string]any) (any, error) {
	*a.ran = true
	return map[string]any{"status": "approve"}, nil
}

// runApprovalStep runs the approval through TaskWrapper as activity
// activityID, with a principal who holds no right at all.
func runApprovalStep(t *testing.T, activityID string, params map[string]any) (ran, checked bool, err error) {
	t.Helper()
	authz := StepAuthorizer{
		LoadContext: func(context.Context, string, string) (*security.SecurityContext, error) {
			checked = true
			return principalContext(t, nil, nil, nil), nil
		},
		UserStatus: func(context.Context, string, string) (UserState, error) { return UserState{Active: true}, nil },
	}
	tw := &TaskWrapper{Task: approvalDouble{ran: &ran}, Authorizer: &authz}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(tw.Execute, activity.RegisterOptions{Name: "core.approval"})
	env.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{ActivityID: activityID, StartToCloseTimeout: time.Minute})
		return workflow.ExecuteActivity(ctx, "core.approval", params).Get(ctx, nil)
	}, workflow.RegisterOptions{Name: "gate-test"})
	env.ExecuteWorkflow("gate-test")
	return ran, checked, env.GetWorkflowError()
}

// The run's own approval gate waits for an answer without any right of the
// principal's; any other approval step is checked as before.
func TestTaskWrapperExemptsOnlyTheRunsApprovalGate(t *testing.T) {
	gate := stepParams(map[string]any{ParamRunAsUserID: "bee", ParamApprovalGate: "run-1/entry-1"})
	ran, checked, err := runApprovalStep(t, model.ApprovalGateTaskID, gate)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.False(t, checked, "the gate needs no right of the principal's")

	for name, tc := range map[string]struct {
		activityID string
		params     map[string]any
	}{
		"an approval step of the workflow's own": {"ask", gate},
		"the gate's id without the run's mark":   {model.ApprovalGateTaskID, stepParams(map[string]any{ParamRunAsUserID: "bee"})},
	} {
		t.Run(name, func(t *testing.T) {
			ran, checked, err := runApprovalStep(t, tc.activityID, tc.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), ErrTypeStepPermissionDenied, "checked like any step, and refused")
			assert.True(t, checked)
			assert.False(t, ran)
		})
	}
}
