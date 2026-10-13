package tasks

import (
	"context"
	"testing"

	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// recordingTask is a write step that records the user it ran as.
type recordingTask struct {
	ranAs *string
}

func (recordingTask) GetName() string            { return "k8s.pod_delete" }
func (recordingTask) GetDescription() string     { return "test double" }
func (recordingTask) GetDisplayName() string     { return "test double" }
func (recordingTask) InputSchema() *types.Schema { return nil }
func (recordingTask) OutputSchema() *types.Schema {
	return nil
}
func (r recordingTask) Execute(ctx types.TaskContext, _ map[string]any) (any, error) {
	*r.ranAs = ctx.GetUserID()
	return map[string]any{"data": "ok"}, nil
}

// runStep executes one step through TaskWrapper inside a Temporal activity,
// recording which principal the gate was asked about.
func runStep(t *testing.T, params map[string]any, allow bool) (ranAs, checked string, err error) {
	t.Helper()
	authz := StepAuthorizer{
		LoadContext: func(_ context.Context, _, userID string) (*security.SecurityContext, error) {
			checked = userID
			if allow {
				return principalContext(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil), nil
			}
			// May run automations, may not change the cluster.
			return principalContext(t, nil, nil, map[string]map[string]bool{"acct-a": {"workflows:Execute": true}}), nil
		},
		UserStatus: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	tw := &TaskWrapper{Task: recordingTask{ranAs: &ranAs}, Authorizer: &authz}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(tw.Execute, activity.RegisterOptions{Name: tw.Task.GetName()})
	_, err = env.ExecuteActivity(tw.Task.GetName(), params)
	return ranAs, checked, err
}

func stepParams(extra map[string]any) map[string]any {
	p := map[string]any{
		ParamTenantID:   "t",
		ParamAccountID:  "acct-a",
		ParamWorkflowID: "wf-1",
		ParamUserID:     "last-editor",
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestTaskWrapperRunsAsTheRunPrincipal(t *testing.T) {
	ranAs, checked, err := runStep(t, stepParams(map[string]any{ParamRunAsUserID: "owner"}), true)
	require.NoError(t, err)
	assert.Equal(t, "owner", checked, "the gate checks the run's principal, not the last editor")
	assert.Equal(t, "owner", ranAs, "the task's outbound identity is the principal it was checked as")
}

func TestTaskWrapperFallsBackToTheLegacyUserForInFlightRuns(t *testing.T) {
	// Runs started before principals existed carry only _user_id.
	ranAs, checked, err := runStep(t, stepParams(nil), true)
	require.NoError(t, err)
	assert.Equal(t, "last-editor", checked)
	assert.Equal(t, "last-editor", ranAs)
}

func TestTaskWrapperRefusesBeforeTheTaskRuns(t *testing.T) {
	ranAs, _, err := runStep(t, stepParams(map[string]any{ParamRunAsUserID: "owner"}), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs k8s:Write on account acct-a")
	assert.Empty(t, ranAs, "a refused step must not execute")
}
