package common

import (
	"sync"

	"nudgebee/llm/config"
)

var (
	conversationStatusUpdateWorkerPool *WorkerPool
	onceStatusUpdateWorkerPool         sync.Once
)

// GetStatusUpdateWorkerPool returns the bounded pool for quick async DB writes:
// conversation status updates and memory usage stats share it.
func GetStatusUpdateWorkerPool() *WorkerPool {
	onceStatusUpdateWorkerPool.Do(func() {
		// Dedicated pool for fast DB I/O (status updates).
		// We use a high queue size (500) as these are non-blocking, quick operations.
		conversationStatusUpdateWorkerPool = NewWorkerPool("conversation_status_updates", config.Config.AsyncPlanExecutionWorkerCount, 500)
	})
	return conversationStatusUpdateWorkerPool
}
