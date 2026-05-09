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

	"nudgebee/collector/otel/api"
	"nudgebee/collector/otel/config"

	"github.com/Cyprinus12138/otelgin"
	"github.com/gin-contrib/pprof"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	sloggin "github.com/samber/slog-gin"

	slogformatter "github.com/samber/slog-formatter"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
		c.Next()
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

	srv := &http.Server{
		Addr:    ":8000",
		Handler: r,
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
