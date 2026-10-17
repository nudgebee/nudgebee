package tasks

import (
	"context"
	"encoding/json"
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
		UserStatus: func(context.Context, string, string) (UserState, error) { return UserState{Active: true}, nil },
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
	assert.Contains(t, err.Error(), "needs k8s:Execute on account acct-a")
	assert.Empty(t, ranAs, "a refused step must not execute")
}

func TestTaskWrapperRefusesAStepAboveTheRunsTier(t *testing.T) {
	// recordingTask is k8s.pod_delete, a change-tier step.
	for _, tc := range []struct {
		name    string
		ceiling any
		refused bool
	}{
		{"no ceiling", nil, false},
		{"a read-only run", float64(TierRead), true}, // numbers arrive as float64 after the JSON round trip
		{"a run allowed changes", TierChange, false},
		{"an unrestricted run", float64(TierUnrestricted), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{ParamRunAsUserID: "owner"}
			if tc.ceiling != nil {
				extra[ParamMaxTier] = tc.ceiling
			}
			ranAs, checked, err := runStep(t, stepParams(extra), true)
			if !tc.refused {
				require.NoError(t, err)
				assert.Equal(t, "owner", ranAs)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "step k8s.pod_delete is tier 2, above this automation's tier 1")
			assert.Empty(t, ranAs, "a refused step must not execute")
			assert.Empty(t, checked, "refused before any permission lookup")
		})
	}
}

func TestTaskWrapperRefusesAnUnreadableTierCeiling(t *testing.T) {
	for _, ceiling := range []any{"1", -1, float64(7), 1.5, true, map[string]any{}} {
		ranAs, checked, err := runStep(t, stepParams(map[string]any{ParamRunAsUserID: "owner", ParamMaxTier: ceiling}), true)
		require.Error(t, err, "%v", ceiling)
		assert.Contains(t, err.Error(), "unreadable tier ceiling")
		assert.Empty(t, ranAs)
		assert.Empty(t, checked)
	}
}

func TestTierParam(t *testing.T) {
	for v, want := range map[any]int{nil: 0, 0: 0, 2: 2, int32(1): 1, int64(3): 3, float64(1): 1, json.Number("2"): 2} {
		got, ok := tierParam(v)
		assert.True(t, ok, "%v", v)
		assert.Equal(t, want, got, "%v", v)
	}
	_, ok := tierParam(json.Number("x"))
	assert.False(t, ok)
}
