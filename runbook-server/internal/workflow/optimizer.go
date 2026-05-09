package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	OptimizerTaskQueue               = "optimizer-task-queue"
	OptimizerWorkflowName            = "OptimizerWorkflow"
	GenerateTasksActivityName        = "GenerateTasksActivity"
	ExecuteTaskActivityName          = "ExecuteTaskActivity"
	CompleteAutoOptimizeActivityName = "CompleteAutoOptimizeActivity"
)

type OptimizerWorkflowInput struct {
	AutoOptimizeID string
}

func OptimizerWorkflow(ctx workflow.Context, input OptimizerWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute * 15,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// 1. Generate Tasks
	var taskIDs []string
	err := workflow.ExecuteActivity(ctx, GenerateTasksActivityName, input.AutoOptimizeID).Get(ctx, &taskIDs)
	if err != nil {
		return fmt.Errorf("failed to generate tasks: %w", err)
	}

	if len(taskIDs) == 0 {
		return nil
	}

	// 2. Execute Tasks
	for _, taskID := range taskIDs {
		err := workflow.ExecuteActivity(ctx, ExecuteTaskActivityName, taskID).Get(ctx, nil)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to execute task", "TaskID", taskID, "Error", err)
		}
	}

	// 3. Mark Complete
	err = workflow.ExecuteActivity(ctx, CompleteAutoOptimizeActivityName, input.AutoOptimizeID).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to complete auto optimize: %w", err)
	}

	return nil
}
