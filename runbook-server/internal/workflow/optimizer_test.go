package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

type optimizerRunRecorder struct {
	generated int
	executed  []string
	completed []string
}

func newOptimizerTestEnv(t *testing.T, rec *optimizerRunRecorder, planned []string) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(OptimizerWorkflow)
	env.RegisterActivityWithOptions(func(ctx context.Context, id string) ([]string, error) {
		rec.generated++
		return planned, nil
	}, activity.RegisterOptions{Name: GenerateTasksActivityName})
	env.RegisterActivityWithOptions(func(ctx context.Context, taskID string) error {
		rec.executed = append(rec.executed, taskID)
		return nil
	}, activity.RegisterOptions{Name: ExecuteTaskActivityName})
	env.RegisterActivityWithOptions(func(ctx context.Context, id string, taskIDs []string) error {
		rec.completed = taskIDs
		return nil
	}, activity.RegisterOptions{Name: CompleteAutoOptimizeActivityName})
	env.RegisterActivityWithOptions(func(ctx context.Context, id string, taskIDs []string) (CollectPRResultsResult, error) {
		return CollectPRResultsResult{}, nil
	}, activity.RegisterOptions{Name: CollectPRResultsActivityName})
	env.RegisterActivityWithOptions(func(ctx context.Context, id string, taskIDs []string) error {
		return nil
	}, activity.RegisterOptions{Name: NotifyPRsReadyActivityName})
	return env
}

// A scheduled run plans its tasks and executes what it planned.
func TestOptimizerWorkflow_PlansWhenNoTasksGiven(t *testing.T) {
	rec := &optimizerRunRecorder{}
	env := newOptimizerTestEnv(t, rec, []string{"t1", "t2"})

	env.ExecuteWorkflow(OptimizerWorkflow, OptimizerWorkflowInput{AutoOptimizeID: "ao"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, 1, rec.generated)
	assert.Equal(t, []string{"t1", "t2"}, rec.executed)
	assert.Equal(t, []string{"t1", "t2"}, rec.completed)
}

// An approved run executes exactly the approved tasks and never plans: planning
// again would park a second approval instead of applying the first.
func TestOptimizerWorkflow_ApprovedRunExecutesGivenTasks(t *testing.T) {
	rec := &optimizerRunRecorder{}
	env := newOptimizerTestEnv(t, rec, []string{"should-not-run"})

	env.ExecuteWorkflow(OptimizerWorkflow, OptimizerWorkflowInput{AutoOptimizeID: "ao", TaskIDs: []string{"a1"}})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, 0, rec.generated)
	assert.Equal(t, []string{"a1"}, rec.executed)
	assert.Equal(t, []string{"a1"}, rec.completed)
}

// A parked run plans nothing to execute and ends without completing a run.
func TestOptimizerWorkflow_ParkedRunEndsAfterPlanning(t *testing.T) {
	rec := &optimizerRunRecorder{}
	env := newOptimizerTestEnv(t, rec, nil)

	env.ExecuteWorkflow(OptimizerWorkflow, OptimizerWorkflowInput{AutoOptimizeID: "ao"})

	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, 1, rec.generated)
	assert.Empty(t, rec.executed)
	assert.Nil(t, rec.completed)
}
