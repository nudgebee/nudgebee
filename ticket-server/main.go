package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/tickets-server/common"
	"nudgebee/tickets-server/routes"
	"os"
	"os/signal"
	"syscall"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"

	slogformatter "github.com/samber/slog-formatter"
	sloggin "github.com/samber/slog-gin"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var errorFormatter = slogformatter.ErrorFormatter("error")
var logger = slog.New(
	traceContextHandler{
		slogformatter.NewFormatterHandler(errorFormatter)(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}),
		),
	},
)

// traceContextHandler enriches every log record whose context carries a valid
// OpenTelemetry span with trace_id / span_id, matching the field names used by
// the other services (and Loki `trace_id` queries). The slog-gin access logger
// logs with the request context, so per-request lines get trace_id for free;
// any slog.*Context(ctx, …) call does too. Records logged with a background
// context (plain slog.Info) simply carry no trace_id.
type traceContextHandler struct {
	slog.Handler
}

func (h traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := oteltrace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceContextHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceContextHandler) WithGroup(name string) slog.Handler {
	return traceContextHandler{h.Handler.WithGroup(name)}
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
	router := gin.New()
	pprof.Register(router)
	router.Use(gin.Recovery())
	router.Use(sloggin.NewWithFilters(logger, sloggin.IgnorePath("/health")))
	// Extract the inbound W3C traceparent into the request context (server span)
	// and echo it back on the response, so ticket-server continues the caller's
	// distributed trace instead of dropping it.
	router.Use(otelgin.Middleware(common.SERVICE_NAME))
	router.Use(traceResponseHeaderMiddleware())

	routes.InitializeRoutes(router)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", common.Config.Port),
		Handler: router,
	}

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

// traceResponseHeaderMiddleware echoes the W3C trace context back on the
// response headers so callers can correlate their request with this service's
// span. Mirrors the same middleware in api-server / llm-server / cloud-collector.
func traceResponseHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		prop := propagation.TraceContext{}
		prop.Inject(c.Request.Context(), propagation.HeaderCarrier(c.Writer.Header()))
		c.Next()
	}
}
