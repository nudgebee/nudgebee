package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/api"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	toolscore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	sloggin "github.com/samber/slog-gin"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const CTX_IS_PUBLIC = "isPublic"

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

var logger = slog.New(
	slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: getLogLevel(),
	}),
)

func authHandlerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		if c.Request.URL.Path == "/health" || strings.HasPrefix(c.Request.URL.Path, "/debug") {
			c.Set(CTX_IS_PUBLIC, true)
			c.Next()
			return
		}

		// Workspace endpoints handle their own internal authentication (JWT or Hasura)
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/workspace/") {
			c.Set(CTX_IS_PUBLIC, true)
			c.Next()
			return
		}

		authHeader := c.Request.Header.Get(config.Config.LlmServerTokenHeader)

		if authHeader == config.Config.LlmServerToken {
			c.Set(CTX_IS_PUBLIC, false)
			c.Next()
			return
		} else {
			logger.Error("main: unauthorized request", "path", c.Request.URL.Path, "method", c.Request.Method, "authHeader", authHeader)
			c.Writer.WriteHeader(401)
			c.Abort()
			return
		}
	}
}

func traceResponseHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		prop := propagation.TraceContext{}
		prop.Inject(c.Request.Context(), propagation.HeaderCarrier(c.Writer.Header()))
		c.Next()
	}
}

func main() {
	slog.SetDefault(logger)
	tp, mp, err := initOtel()
	if err != nil {
		slog.Error(err.Error())
		return
	}
	common.InitMetrics()
	core.InitMetrics()
	prompts.InitializeGlobalLoader()
	if err := core.InitTokenizers(); err != nil {
		slog.Error("main: failed to init tokenizers", "error", err)
		return
	}
	defer func() {
		tpSdk, ok := tp.(*sdktrace.TracerProvider)
		if ok {
			if err := tpSdk.Shutdown(context.Background()); err != nil {
				slog.Error(fmt.Sprintf("main: error shutting down tracer provider: %v", err))
			}
		}
		mpSdk, ok := mp.(*sdkmetric.MeterProvider)
		if ok {
			if err := mpSdk.Shutdown(context.Background()); err != nil {
				slog.Error(fmt.Sprintf("main: error shutting down meter provider: %v", err))
			}
		}
	}()

	// Setting AWS region for bedrock model
	if config.Config.LlmProviderRegion != "" {
		err = os.Setenv("AWS_REGION", config.Config.LlmProviderRegion)
		if err != nil {
			slog.Error("main: failed to set AWS_REGION environment variable", "error", err)
			return
		}
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	pprof.Register(r)
	r.Use(gin.Recovery())
	r.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	r.Use(otelgin.Middleware(config.SERVICE_NAME))
	r.Use(traceResponseHeaderMiddleware())
	r.Use(authHandlerMiddleware())

	var tracer = otel.Tracer(config.SERVICE_NAME)
	var meter = otel.Meter(config.SERVICE_NAME)

	api.ConfigureRoutes(r, tracer, meter)

	srv := &http.Server{
		Addr:    "0.0.0.0:" + config.Config.Port,
		Handler: r,
	}

	// Start background integration KB sync
	syncCtx, syncCancel := context.WithCancel(context.Background())
	go toolscore.StartIntegrationKBSync(syncCtx)
	slog.Info("main: started integration KB sync background thread")

	// Clean up stale workspace pods on startup
	go workspace.CleanupStaleWorkspaces(syncCtx)

	// Periodically delete never-used and stale long-term memories.
	go core.StartMemoryTTLCleanup(syncCtx)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		slog.Info("main: got SIGTERM, shutting down")
		syncCancel() // Stop the KB sync goroutine
		slog.Info("main: connections closed, shutting down server")
		err := srv.Shutdown(context.Background())
		if err != nil {
			slog.Error("main: server shutdown failed:", "error", err)
		}
		// Clean up API resources
		api.CleanupResources()
		common.StopScheduler()
		common.MqClose()
		common.Close()
		os.Exit(1)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("main: server listen failed:", "error", err)
	}

}
