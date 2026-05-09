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

	"nudgebee/collector/cloud/account"
	"nudgebee/collector/cloud/api"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	sloggin "github.com/samber/slog-gin"

	slogformatter "github.com/samber/slog-formatter"

	// Reads the cgroup memory limit at startup and sets GOMEMLIMIT to a fraction of it
	// so the Go runtime scavenges aggressively before the kernel OOM-kills the container.
	// Ratio is configurable via the GOMEMLIMIT_RATIO env var (default 0.9).
	_ "github.com/KimMachineGun/automemlimit"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	awsProvider "nudgebee/collector/cloud/providers/aws"
	azureProvider "nudgebee/collector/cloud/providers/azure"
	gcpProvider "nudgebee/collector/cloud/providers/gcloud"
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

		if c.Request.URL.Path == "/health" || strings.HasPrefix(c.Request.URL.Path, "/debug") {
			c.Set(CTX_IS_PUBLIC, true)
			c.Next()
			return
		}

		authHeader := c.Request.Header.Get(config.Config.CloudCollectorServerTokenHeader)

		if authHeader == config.Config.CloudCollectorServerToken {
			c.Set(CTX_IS_PUBLIC, false)
			c.Next()
			return
		} else {
			logger.Error("Unauthorized request", "path", c.Request.URL.Path, "method", c.Request.Method, "authHeader", authHeader)
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

	gin.SetMode(gin.ReleaseMode)
	// Start the permission audit collector for tracking cloud API permission errors
	providers.StartPermissionAuditCollector()
	defer providers.StopPermissionAuditCollector()

	r := gin.New()
	pprof.Register(r)
	r.Use(gin.Recovery())
	r.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	r.Use(otelgin.Middleware(config.SERVICE_NAME))
	r.Use(traceResponseHeaderMiddleware())
	r.Use(authHandlerMiddleware())

	var tracer = otel.Tracer(config.SERVICE_NAME)
	var meter = otel.Meter(config.SERVICE_NAME)

	api.ConfigureRoutes(r, &tracer, &meter, logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	requestTimeout := time.Duration(config.Config.CloudCollectorRequestTimeoutSeconds) * time.Second

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  requestTimeout,
		WriteTimeout: requestTimeout,
		IdleTimeout:  60 * time.Second,
	}

	// Create the CloudProviderContext
	cloudCtx := providers.NewCloudProviderContext(context.Background())

	// Create an instance of your event handler
	sqsEventHandler := account.NewAsyncEventHandler()

	// Start the AWS EventBridge SQS consumer in a goroutine, passing the handler
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("AWS EventBridge SQS consumer panicked", "error", r)
			}
		}()
		awsProvider.StartEventBridgeSQSConsumer(cloudCtx, sqsEventHandler)
	}()

	// Start the Azure Event Grid Service Bus consumer in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Azure Event Grid Service Bus consumer panicked", "error", r)
			}
		}()
		azureProvider.StartAzureServiceBusConsumer(cloudCtx, sqsEventHandler)
	}()

	// Start the GCP Pub/Sub consumer for Cloud Monitoring alerts and events
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("GCP Pub/Sub consumer panicked", "error", r)
			}
		}()
		// Load GCP event rules from embedded YAML or custom file
		ruleSet, err := gcpProvider.LoadGCPEventRules(config.Config.CloudCollectorGcpEventRulesPath)
		if err != nil {
			slog.Error("Failed to load GCP event rules", "error", err)
			return
		}
		slog.Info("Loaded GCP event rules", "count", len(ruleSet.Rules))

		// Create GCP provider instance for API calls (metrics, logs, resources)
		// The provider is accessed through exported functions, pass nil for now
		// as the processor will use the account-specific provider when needed
		processor := gcpProvider.NewTemplatedPubSubProcessor(ruleSet, gcpProvider.GetProviderForPubSub())

		// Start consuming Pub/Sub messages
		gcpProvider.StartPubSubConsumer(
			cloudCtx,
			config.Config.CloudCollectorGcpPubSubProjectID,
			config.Config.CloudCollectorGcpPubSubSubscriptionID,
			processor,
			sqsEventHandler,
		)
	}()

	// Start the AWS Org Registration SQS consumer in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("AWS Org Registration SQS consumer panicked", "error", r)
			}
		}()
		awsProvider.StartOrgRegistrationSQSConsumer(cloudCtx)
	}()

	// Start the RabbitMQ consumer for cloud account cost reports
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Cloud account cost report consumer panicked", "error", r)
			}
		}()
		ctx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), logger, &tracer, &meter)
		err := account.ConsumeCloudAccountCostReportJobs(ctx, config.Config.CloudCollectorServerCostProcessingWorkersMax)
		if err != nil {
			slog.Error("Failed to start cloud account cost report consumer", "error", err)
		}
	}()

	// Start the RabbitMQ consumer for cloud account metrics
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Cloud account metrics consumer panicked", "error", r)
			}
		}()
		ctx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), logger, &tracer, &meter)
		err := account.ConsumeCloudAccountMetricsJobs(ctx, config.Config.CloudCollectorServerMetricsWorkersMax)
		if err != nil {
			slog.Error("Failed to start cloud account metrics consumer", "error", err)
		}
	}()

	// Start the RabbitMQ consumer for cloud account events
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Cloud account events consumer panicked", "error", r)
			}
		}()
		ctx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), logger, &tracer, &meter)
		err := account.ConsumeCloudAccountEventsJobs(ctx, config.Config.CloudCollectorServerEventsWorkersMax)
		if err != nil {
			slog.Error("Failed to start cloud account events consumer", "error", err)
		}
	}()

	// Start the RabbitMQ consumer for post-report processing (resource discovery, recommendations, metrics)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Cloud account post-report consumer panicked", "error", r)
			}
		}()
		ctx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), logger, &tracer, &meter)
		err := account.ConsumeCloudAccountPostReportJobs(ctx, 1)
		if err != nil {
			slog.Error("Failed to start cloud account post-report consumer", "error", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		slog.Info("Got SIGTERM, shutting down")
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
