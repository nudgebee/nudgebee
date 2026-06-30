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
	CollectPRResultsActivityName     = "CollectPRResultsActivity"
	NotifyPRsReadyActivityName       = "NotifyPRsReadyActivity"
)

// PR-ready follow-up cadence. GitOps PRs are created asynchronously by the
// api-server code agent after the run completes, so we poll the run's
// resolutions a few times before sending the "PRs ready" summary. Kept well
// within the optimizer workflow's 1h execution timeout.
const (
	prFollowupInitialDelay = time.Minute
	prFollowupPollInterval = time.Minute
	prFollowupMaxAttempts  = 8
)

type OptimizerWorkflowInput struct {
	AutoOptimizeID string
}

// CollectPRResultsResult reports whether the run produced any GitOps PR tasks
// and whether all of them have settled (PR URL populated, or creation failed).
type CollectPRResultsResult struct {
	HasPRTasks bool `json:"has_pr_tasks"`
	AllSettled bool `json:"all_settled"`
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

	// 3. Mark Complete — also sends the change-gated completion summary.
	err = workflow.ExecuteActivity(ctx, CompleteAutoOptimizeActivityName, input.AutoOptimizeID, taskIDs).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to complete auto optimize: %w", err)
	}

	// 4. PR-ready follow-up — only if the run raised GitOps PRs (created
	// asynchronously after completion). Best-effort: never fails the run.
	notifyPRsReadyFollowup(ctx, input.AutoOptimizeID, taskIDs)

	return nil
}

// notifyPRsReadyFollowup waits for the run's asynchronously-created GitOps PRs
// to settle, then sends a single aggregated "PRs ready" message with the real
// PR URLs. It short-circuits immediately if the run produced no PR tasks.
func notifyPRsReadyFollowup(ctx workflow.Context, autoOptimizeID string, taskIDs []string) {
	logger := workflow.GetLogger(ctx)

	if err := workflow.Sleep(ctx, prFollowupInitialDelay); err != nil {
		return
	}

	for attempt := 0; attempt < prFollowupMaxAttempts; attempt++ {
		var res CollectPRResultsResult
		if err := workflow.ExecuteActivity(ctx, CollectPRResultsActivityName, autoOptimizeID, taskIDs).Get(ctx, &res); err != nil {
			logger.Error("Failed to collect PR results", "error", err)
			return
		}

		if !res.HasPRTasks {
			return // nothing to follow up on
		}

		if res.AllSettled || attempt == prFollowupMaxAttempts-1 {
			if err := workflow.ExecuteActivity(ctx, NotifyPRsReadyActivityName, autoOptimizeID, taskIDs).Get(ctx, nil); err != nil {
				logger.Error("Failed to send PR-ready notification", "error", err)
			}
			return
		}

		if err := workflow.Sleep(ctx, prFollowupPollInterval); err != nil {
			return
		}
	}
}
