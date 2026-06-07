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
	"time"

	"github.com/gin-gonic/gin"

	"nudgebee/services/api"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	sloggin "github.com/samber/slog-gin"

	slogformatter "github.com/samber/slog-formatter"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	_ "nudgebee/services/integrations"
)

const CTX_IS_PUBLIC = "isPublic"

var errorFormatter = slogformatter.ErrorFormatter("error")
var logger = slog.New(
	slogformatter.NewFormatterHandler(errorFormatter)(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	),
)

func authHandlerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		if strings.HasPrefix(c.Request.URL.Path, "/health") || strings.HasPrefix(c.Request.URL.Path, "/api/webhooks") || strings.HasPrefix(c.Request.URL.Path, "/swagger") || c.Request.URL.Path == "/openapi.json" {
			c.Set(CTX_IS_PUBLIC, true)
			c.Next()
			return
		}

		authHeader := c.Request.Header.Get(config.Config.ServiceApiServerTokenHeader)

		if authHeader == config.Config.ServiceApiServerToken {
			c.Set(CTX_IS_PUBLIC, false)
			c.Next()
			return
		} else {
			maskedAuth := "***"
			if len(authHeader) > 4 {
				maskedAuth = authHeader[:4] + "***"
			}
			logger.Error("Unauthorized request", "path", c.Request.URL.Path, "method", c.Request.Method, "authHeader", maskedAuth)
			c.AbortWithStatus(401)
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

func cleanup() {
	slog.Info("Closing DB and MQ connections")
	common.MqClose()
	database.Close()
}

// @title           Nudgebee Services API
// @version         1.0
// @description     Backend API for the Nudgebee platform (RPC actions, webhooks, integrations, observability).
// @BasePath        /
// @securityDefinitions.apikey ActionToken
// @in              header
// @name            X-ACTION-TOKEN
// @description     Internal service-to-service auth token. Default header name (configurable via `action_api_server_token_header`); value is the `action_api_server_token` config value.
func main() {
	slog.SetDefault(logger)

	// Heads-up when the action token isn't configured. The auth middleware
	// gates on `headerValue == ServiceApiServerToken`, so an empty config
	// matches any request without the header — every endpoint is effectively
	// unauthenticated. Not blocked here for backward-compat; operators
	// should set the token in any non-throwaway deployment. Generate with:
	//   openssl rand -hex 32
	// Set the same value as ACTION_API_SERVER_TOKEN in app/.env and the
	// chart's nudgebee_secret.
	if config.Config.ServiceApiServerToken == "" {
		slog.Warn("ACTION_API_SERVER_TOKEN is empty — auth middleware will accept any request. " +
			"Set ACTION_API_SERVER_TOKEN (and the matching value in app/.env / chart nudgebee_secret) for any non-throwaway deployment. " +
			"Generate with: openssl rand -hex 32.")
	}

	tp, mp, err := initOtel()
	if err != nil {
		slog.Error(err.Error())
		return
	}
	common.InitMetrics()
	common.RegisterDependencyMetrics(func(ctx context.Context) common.DependencyStats {
		var s common.DependencyStats

		if db, ok := database.GetDatabaseManagerIfInitialized(database.Metastore); ok {
			stats := db.Db.Stats()
			s.Postgres = common.DBStats{
				Open:      stats.OpenConnections,
				InUse:     stats.InUse,
				Idle:      stats.Idle,
				MaxOpen:   stats.MaxOpenConnections,
				WaitCount: stats.WaitCount,
				Status:    1,
			}
		}

		if config.Config.ClickhouseEnabled {
			if db, ok := database.GetDatabaseManagerIfInitialized(database.Warehouse); ok {
				stats := db.Db.Stats()
				s.Clickhouse = common.DBStats{
					Open:      stats.OpenConnections,
					InUse:     stats.InUse,
					Idle:      stats.Idle,
					MaxOpen:   stats.MaxOpenConnections,
					WaitCount: stats.WaitCount,
					Status:    1,
				}
			}
		}

		mqCtx, mqCancel := context.WithTimeout(ctx, 5*time.Second)
		defer mqCancel()
		mqInfo := common.MqHealthCheck(mqCtx)
		s.MqConsumers = mqInfo.Consumers
		s.MqPublishers = mqInfo.Publishers
		if mqInfo.Err == nil {
			s.MqStatus = 1
		}

		cacheInfo := common.CacheHealthCheck(ctx)
		s.CacheProvider = cacheInfo.Provider
		if cacheInfo.Err == nil {
			s.CacheStatus = 1
		}

		return s
	})

	defer func() {
		tpSdk, ok := tp.(*sdktrace.TracerProvider)
		if ok {
			if err := tpSdk.Shutdown(context.Background()); err != nil {
				slog.Error(fmt.Sprintf("Error shutting down tracer provider: %v", err))
			}
		}
		mpSdk, ok := mp.(*sdkmetric.MeterProvider)
		if ok {
			if err := mpSdk.Shutdown(context.Background()); err != nil {
				slog.Error(fmt.Sprintf("Error shutting down meter provider: %v", err))
			}
		}
	}()

	api.RunEEStartupHooks()

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

	api.RunEEBootstrapHooks(logger)
	api.ConfigureRoutes(r, &tracer, &meter, logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		slog.Info("Got SIGTERM, shutting down")
		cleanup()
		slog.Info("Connections closed, shutting down server")
		err := srv.Shutdown(context.Background())
		if err != nil {
			slog.Error("Server shutdown failed:", "error", err)
		}
		os.Exit(1)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Server listen failed:", "error", err)
	}

}
