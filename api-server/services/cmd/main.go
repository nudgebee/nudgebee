package main

import (
	"context"
	"errors"
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
	"nudgebee/services/bootstrap"
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

// TraceLogHandler stamps trace_id/span_id from the record context onto every
// slog.XxxContext line, so context-style logging correlates in Loki without
// threading a stamped logger through every call site.
var logger = slog.New(
	common.NewTraceLogHandler(
		slogformatter.NewFormatterHandler(errorFormatter)(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}),
		),
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

	// Announce the deployment role up front: on a split deployment the single
	// most useful thing in a pod's first log line is whether it is meant to be
	// draining queues. See config.appConfig.ServerRole.
	slog.Info("starting services-server", "server_role", config.Config.ServerRole,
		"background_jobs", config.RunsBackgroundJobs())

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
		// Runs after serve's drain, so it has to fit the rest of the pod's
		// grace period; an unreachable collector must not hold it up.
		ctx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		tpSdk, ok := tp.(*sdktrace.TracerProvider)
		if ok {
			if err := tpSdk.Shutdown(ctx); err != nil {
				slog.Error(fmt.Sprintf("Error shutting down tracer provider: %v", err))
			}
		}
		mpSdk, ok := mp.(*sdkmetric.MeterProvider)
		if ok {
			if err := mpSdk.Shutdown(ctx); err != nil {
				slog.Error(fmt.Sprintf("Error shutting down meter provider: %v", err))
			}
		}
	}()

	api.RunEEStartupHooks()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	pprof.Register(r)
	r.Use(gin.Recovery())
	// otelgin must run before sloggin so the server span is on the request
	// context when the access log line is emitted with trace_id/span_id.
	r.Use(otelgin.Middleware(config.SERVICE_NAME))
	slogginConfig := sloggin.DefaultConfig()
	slogginConfig.WithTraceID = true
	slogginConfig.WithSpanID = true
	// 499 is a request the caller abandoned (api.StatusClientClosedRequest). sloggin
	// would log it at Warn like any 4xx; the handler already logs it at Info.
	slogginConfig.Filters = []sloggin.Filter{sloggin.IgnorePath("/health"), sloggin.IgnoreStatus(api.StatusClientClosedRequest)}
	r.Use(sloggin.NewWithConfig(logger, slogginConfig))
	r.Use(traceResponseHeaderMiddleware())
	r.Use(authHandlerMiddleware())

	var tracer = otel.Tracer(config.SERVICE_NAME)
	var meter = otel.Meter(config.SERVICE_NAME)

	api.RunEEBootstrapHooks(logger)

	// Provision the first admin and tenant, and adopt the credential the chart
	// generated for the bundled in-cluster agent, so a fresh install is complete
	// before anyone signs in. Inert unless an admin address and LOCAL_AGENT_* are
	// configured. Logged, not fatal: a server that cannot do this must still serve.
	//
	// One bounded attempt happens here, before requests arrive, matching
	// RunEEBootstrapHooks above. On a fresh install the migration Job is still
	// running at this point, so a failure is retried in the background until the
	// schema catches up; firstRunCtx stops those retries on shutdown.
	firstRunCtx, stopFirstRun := context.WithCancel(context.Background())
	bootstrap.RunFirstRun(firstRunCtx, logger)

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
	if err := serve(srv, c, shutdownTimeout, func() {
		stopFirstRun()
		cleanup()
	}); err != nil {
		slog.Error("Server listen failed:", "error", err)
	}
}

// shutdownTimeout bounds how long in-flight requests get to finish after
// SIGTERM, and telemetryFlushTimeout the trace/metric flush after that. The
// chart's terminationGracePeriodSeconds must cover the preStop sleep, both of
// these and cleanup(), or the kubelet SIGKILLs the pod part-way through.
const (
	shutdownTimeout       = 15 * time.Second
	telemetryFlushTimeout = 5 * time.Second
)

// serve runs srv until stop fires, then stops accepting connections, gives
// in-flight requests up to timeout to finish, and only then runs cleanup.
// Closing the DB and MQ first would fail exactly those requests. It returns
// only once all of that is done, because main returning ends the process.
func serve(srv *http.Server, stop <-chan os.Signal, timeout time.Duration, cleanup func()) error {
	drained := make(chan struct{})
	// Closed when serve returns, so neither signal watcher outlives it — e.g.
	// when the listen fails and no signal ever comes.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
		case <-done:
			return
		}
		slog.Info("Got SIGTERM, draining in-flight requests", "timeout", timeout)
		// A second signal skips the drain, so a local Ctrl-C twice still exits.
		go func() {
			select {
			case <-stop:
				slog.Warn("Second signal, exiting without finishing the drain")
				os.Exit(1)
			case <-done:
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("Server shutdown did not finish draining", "error", err)
		}
		cleanup()
		close(drained)
	}()

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-drained
	return nil
}
