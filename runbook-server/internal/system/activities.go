package system

import (
	"context"
	"nudgebee/runbook/internal/storage"
)

const CleanupBatchSize = 5000 // Batch size for deleting expired state

type SystemActivities struct {
	Store *storage.WorkflowDao
}

func NewSystemActivities(store *storage.WorkflowDao) *SystemActivities {
	return &SystemActivities{Store: store}
}

func (a *SystemActivities) CleanupExpiredStateActivity(ctx context.Context) (int64, error) {
	// Delete up to CleanupBatchSize rows at a time to keep transactions short
	return a.Store.DeleteExpiredState(ctx, CleanupBatchSize)
}
