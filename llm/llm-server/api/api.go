package api

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/gin-gonic/gin"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	asyncOperationWorkerPool     *common.WorkerPool
	asyncUserOperationWorkerPool *common.WorkerPool
	auditWorkerPool              *common.WorkerPool
	metricsWorkerPool            *common.WorkerPool
)

func init() {
	asyncOperationWorkerPool = common.NewWorkerPool("async_api_operations", config.Config.AsyncApiWorkerCount, config.Config.AsyncApiQueueSize)
	asyncUserOperationWorkerPool = common.NewWorkerPool("async_api_user_operations", config.Config.AsyncApiWorkerCount, config.Config.AsyncApiQueueSize)
	auditWorkerPool = common.NewWorkerPool("audit_operations", config.Config.AuditApiWorkerCount, config.Config.AsyncApiQueueSize)
	metricsWorkerPool = common.NewWorkerPool("metrics_tracking", 10, 5000)
	core.SetMetricsWorkerPool(metricsWorkerPool)
}

func GetMetricsWorkerPool() *common.WorkerPool {
	return metricsWorkerPool
}

func ConfigureRoutes(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	handleHeathCheckApis(r, tracer, meter)
	handleCompletionApis(r, tracer, meter)
	handleAgentsApis(r, tracer, meter)
	handleFunctionsApis(r, tracer, meter)
	handleToolsApis(r, tracer, meter)
	handleAnalysisApis(r, tracer, meter)
	handleSyncConversationStatusApi(r)
	handleRagApis(r, tracer, meter)
	handleBudgetStatusApi(r, tracer, meter)
	handleConversationApis(r, tracer, meter)
	handleKnowledgebaseApis(r, tracer, meter)
	handleMemoryApis(r, tracer, meter)
	handleMemoryV2Apis(r, tracer, meter)
	handleGlobalContextApis(r, tracer, meter)
	handleWorkspaceApis(r, tracer, meter)
	handleLLMConfigTestApis(r, tracer, meter)
	handleSwagger(r)
}

// CleanupResources cleans up all API resources that need graceful shutdown
func CleanupResources() {
	// Clean up event analysis resources
	CleanupEventAnalysisResources()
	// Clean up all background worker pools
	common.StopAllWorkerPools()
}
