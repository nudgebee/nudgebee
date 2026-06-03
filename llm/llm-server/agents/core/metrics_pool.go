package core

import "nudgebee/llm/common"

var metricsWorkerPool *common.WorkerPool

// SetMetricsWorkerPool allows other packages (like api) to register the metrics pool
func SetMetricsWorkerPool(p *common.WorkerPool) {
	metricsWorkerPool = p
}

// GetMetricsWorkerPool returns the registered metrics pool, falling back to a dummy/sync if not set
func GetMetricsWorkerPool() *common.WorkerPool {
	return metricsWorkerPool
}
