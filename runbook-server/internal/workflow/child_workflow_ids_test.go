package workflow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func replaySafeChild(ctx workflow.Context) error { return nil }

// replaySafeParent records two invocations of the same task ID to guard uniqueness.
func replaySafeParent(ctx workflow.Context) error {
	var previous childWorkflowIDs
	for i := 0; i < 2; i++ {
		ids, err := newChildWorkflowIDs(ctx, "parent-definition", "same-task")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(ids.DefinitionID, "inline-same-task-") ||
			!strings.HasPrefix(ids.ExecutionID, "parent-definition-same-task-") {
			return fmt.Errorf("unexpected child workflow ID")
		}
		if ids.ExecutionID == previous.ExecutionID || ids.DefinitionID == previous.DefinitionID {
			return fmt.Errorf("child workflow IDs were reused")
		}
		previous = ids
		opts := workflow.ChildWorkflowOptions{WorkflowID: ids.ExecutionID}
		if err := workflow.ExecuteChildWorkflow(workflow.WithChildOptions(ctx, opts), replaySafeChild).Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

// This fixture was recorded from a disposable Temporal dev server using two
// sequential children. Replay must reuse the recorded IDs, not generate new ones.
func TestRecordedChildWorkflowIDsReplay(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(replaySafeParent)
	require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/child_workflow_ids_history.json"))
}

// Workers running the old code cannot safely replay histories with new ID markers.
func TestOldWorkerCannotReplayRecordedChildWorkflowIDs(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(func(ctx workflow.Context) error {
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("parent-definition-same-task-%s", uuid.NewString())
			opts := workflow.ChildWorkflowOptions{WorkflowID: id}
			if err := workflow.ExecuteChildWorkflow(workflow.WithChildOptions(ctx, opts), replaySafeChild).Get(ctx, nil); err != nil {
				return err
			}
		}
		return nil
	}, workflow.RegisterOptions{Name: "replaySafeParent"})
	require.Error(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/child_workflow_ids_history.json"))
}

// Histories that predate the version marker keep the old command sequence.
func TestLegacyChildWorkflowIDsVersion(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("record-child-workflow-ids", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion).Once()
	env.ExecuteWorkflow(func(ctx workflow.Context) (childWorkflowIDs, error) {
		return newChildWorkflowIDs(ctx, "parent", "task")
	})
	require.NoError(t, env.GetWorkflowError())
	var ids childWorkflowIDs
	require.NoError(t, env.GetWorkflowResult(&ids))
	require.True(t, strings.HasPrefix(ids.DefinitionID, "inline-task-"))
	require.True(t, strings.HasPrefix(ids.ExecutionID, "parent-task-"))
	env.AssertExpectations(t)
}
