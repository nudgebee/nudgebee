package api

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/workflow"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/optimizer"

	"github.com/Cyprinus12138/otelgin"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	sloggin "github.com/samber/slog-gin"
)

type Server struct {
	router                 *gin.Engine
	workflowService        workflow.WorkflowService
	configService          configSvc.ConfigService
	optimizerService       optimizer.Service
	httpServer             *http.Server
	tracer                 *trace.Tracer
	meter                  *metric.Meter
	logger                 *slog.Logger
	securityContextBuilder SecurityContextBuilder
}

func getLogLevel() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo // default
	}
}

func NewServer(workflowService workflow.WorkflowService, configService configSvc.ConfigService) *Server {
	var logger = slog.New(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: getLogLevel(),
		}),
	)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	router.Use(otelgin.Middleware(config.SERVICE_NAME))
	s := &Server{
		router:          router,
		workflowService: workflowService,
		configService:   configService,
		httpServer: &http.Server{
			Addr:    ":8080",
			Handler: router,
		},
		logger:                 logger,
		securityContextBuilder: &DefaultSecurityContextBuilder{},
	}
	return s
}

func NewServerWithObservability(workflowService workflow.WorkflowService, configService configSvc.ConfigService, tracer *trace.Tracer, meter *metric.Meter) *Server {
	var logger = slog.New(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: getLogLevel(),
		}),
	)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	gin.SetMode(gin.ReleaseMode)
	router.Use(gin.Recovery())
	router.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	router.Use(otelgin.Middleware(config.SERVICE_NAME))
	s := &Server{
		router:          router,
		workflowService: workflowService,
		configService:   configService,
		httpServer: &http.Server{
			Addr:    ":8080",
			Handler: router,
		},
		meter:                  meter,
		tracer:                 tracer,
		logger:                 logger,
		securityContextBuilder: &DefaultSecurityContextBuilder{},
	}
	return s
}

func (s *Server) SetWorkflowService(ws workflow.WorkflowService) {
	s.workflowService = ws
	s.setupRoutes() // Re-setup routes to use the now-available workflowService
}

func (s *Server) setupRoutes() {
	workflows := s.router.Group("/workflows")
	{
		workflows.POST("", s.createWorkflow)
		workflows.GET("", s.listWorkflows)
		workflows.GET("/:id", s.getWorkflow)
		workflows.GET("/:id/state", s.getWorkflowState)
		workflows.PUT("/:id", s.updateWorkflow)
		workflows.DELETE("/:id", s.deleteWorkflow)
		workflows.POST("/:id/trigger", s.triggerWorkflow)
		workflows.GET("/:id/runs", s.listWorkflowExecutions)
		workflows.GET("/:id/runs/:execution_id", s.getWorkflowExecution)
		workflows.PUT("/:id/runs/:execution_id", s.updateWorkflowExecution)
		workflows.POST("/:id/runs/:execution_id/cancel", s.cancelWorkflowExecution)
		workflows.POST("/:id/runs/:execution_id/retrigger", s.retriggerWorkflowExecution)
		workflows.GET("/:id/executions", s.listWorkflowExecutions)
		workflows.GET("/:id/executions/:execution_id", s.getWorkflowExecution)
		workflows.PUT("/:id/executions/:execution_id", s.updateWorkflowExecution)
		workflows.POST("/:id/executions/:execution_id/cancel", s.cancelWorkflowExecution)
		workflows.POST("/:id/executions/:execution_id/retrigger", s.retriggerWorkflowExecution)
		workflows.POST("/:id/pause", s.pauseWorkflow)
		workflows.POST("/:id/resume", s.resumeWorkflow)
		workflows.POST("/validate", s.validateWorkflow)
		workflows.POST("/dry-run", s.dryRunWorkflow)
	}

	configs := s.router.Group("/configs")
	{
		configs.POST("", s.saveConfig)
		configs.GET("", s.listConfigs)
		configs.GET("/:key", s.getConfig)
		configs.DELETE("/:key", s.deleteConfig)
	}

	// Simplified webhook endpoint
	s.router.POST("/webhook/:workflowId", s.handleGenericWebhook)

	approvals := s.router.Group("/approvals")
	{
		approvals.POST("/:token", s.handleApproval)
	}

	// Hasura Action endpoint
	s.router.POST("/hasura", s.handleHasuraAction)

	// Health check endpoint
	s.router.GET("/health", s.healthCheck)

	tasks := s.router.Group("/tasks")
	{
		tasks.GET("", s.listTasks)
		tasks.POST("/:task_type/execute", s.executeTask)
	}

	templating := s.router.Group("/templating")
	{
		templating.GET("/functions", s.listTemplatingFunctions)
	}

	s.setupSwagger()
}

// HealthResponse is the body returned by the liveness endpoint.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// healthCheck godoc
// @Summary      Liveness probe
// @Description  Returns 200 OK when the runbook-server process is up. Does not verify Temporal/Postgres/RabbitMQ.
// @Tags         system
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}

func (s *Server) GetRouter() *gin.Engine {
	s.setupRoutes()
	return s.router
}

func (s *Server) Start(address string) error {
	s.httpServer.Addr = address
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) GetHandler() http.Handler {
	return s.router
}
