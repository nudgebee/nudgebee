package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"nudgebee/runbook/internal/model"
)

type endedRun struct {
	executionID string
	status      model.WorkflowExecutionStatus
	message     string
}

func TestUpdateLastExecutionStatusTellsTheRunFinishedHook(t *testing.T) {
	var got []endedRun
	prev := runFinished
	runFinished = func(_ context.Context, _ model.Workflow, executionID string, status model.WorkflowExecutionStatus, message string) error {
		got = append(got, endedRun{executionID, status, message})
		return nil
	}
	t.Cleanup(func() { runFinished = prev })

	bound := model.Workflow{ID: "wf", Binding: &model.WorkflowBinding{Role: model.BindingRoleAutomation}}
	cases := map[string]struct {
		wf     model.Workflow
		status model.WorkflowExecutionStatus
		told   bool
	}{
		"a bound automation that failed":    {bound, model.WorkflowExecutionStatusFailed, true},
		"a bound automation that completed": {bound, model.WorkflowExecutionStatusCompleted, true},
		"a bound automation still running":  {bound, model.WorkflowExecutionStatusRunning, false},
		"an unbound workflow that failed":   {model.Workflow{ID: "wf"}, model.WorkflowExecutionStatusFailed, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got = nil
			store := new(MockWorkflowStore)
			store.On("SetLastExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			a := &WorkflowActivities{Store: store}

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivityWithOptions(a.Internal_UpdateLastExecutionStatus, activity.RegisterOptions{Name: Internal_UpdateLastExecutionStatusActivity})
			_, err := env.ExecuteActivity(Internal_UpdateLastExecutionStatusActivity, tc.wf, tc.status, "boom")
			require.NoError(t, err)
			if !tc.told {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Equal(t, tc.status, got[0].status)
			assert.Equal(t, "boom", got[0].message)
			assert.NotEmpty(t, got[0].executionID, "the run id of the execution the activity belongs to")
		})
	}
}
