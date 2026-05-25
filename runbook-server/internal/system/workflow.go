package system

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

const (
	CleanupExpiredStateActivityName = "CleanupExpiredStateActivity"
)

func SystemCleanupWorkflow(ctx workflow.Context) (int64, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting SystemCleanupWorkflow")

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute, // Generous timeout for DB operation
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var deletedCount int64
	err := workflow.ExecuteActivity(ctx, CleanupExpiredStateActivityName).Get(ctx, &deletedCount)
	if err != nil {
		logger.Error("Failed to cleanup expired state", "error", err)
		return 0, err
	}

	logger.Info("Completed SystemCleanupWorkflow", "deletedCount", deletedCount)
	return deletedCount, nil
}
