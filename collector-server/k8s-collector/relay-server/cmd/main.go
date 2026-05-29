package main

// added a test change which does absolutely nothing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/relay-server/pkg/config"
	otel_exporter "nudgebee/relay-server/pkg/otel"
	"nudgebee/relay-server/pkg/server"
	"nudgebee/relay-server/pkg/server/metrics"

	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	slogformatter "github.com/samber/slog-formatter"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var errorFormatter = slogformatter.ErrorFormatter("error")
var logger = slog.New(
	slogformatter.NewFormatterHandler(errorFormatter)(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	),
)

func main() {

	if err := godotenv.Load(); err != nil {
		logger.Warn("Error loading .env file")
	}
	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Error loading config", "error", err)
		return
	}

	tp, mp, err := otel_exporter.InitOtel(*logger, *cfg)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	defer func() {
		tpSdk, ok := tp.(*sdktrace.TracerProvider)
		if ok {
			if err := tpSdk.Shutdown(context.Background()); err != nil {
				logger.Error(fmt.Sprintf("Error shutting down tracer provider: %v", err))
			}
		}
		mpSdk, ok := mp.(*sdkmetric.MeterProvider)
		if ok {
			if err := mpSdk.Shutdown(context.Background()); err != nil {
				logger.Error(fmt.Sprintf("Error shutting down meter provider: %v", err))
			}
		}
	}()

	var tracer = otel.Tracer(config.SERVICE_NAME)
	var meter = otel.Meter(config.SERVICE_NAME)
	// initialize all metrics exactly once
	if err := metrics.Init(meter); err != nil {
		logger.Error("metrics init failed", "error", err)
		return
	}

	// Initialize async metrics with reasonable defaults
	// Buffer: 10000 events, Batch: 100 events, Flush: every 1 second
	metrics.InitAsync(10000, 100, 1*time.Second)
	defer metrics.ShutdownAsync()
	// 5. Wire up Gin router
	router, err := server.SetupRouter(cfg, &tracer, &meter, logger)
	if err != nil {
		logger.Error("Server startup failed:", "error", err)
		os.Exit(1)
	}

	// 6. Start server with proper timeouts
	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout + 5*time.Second, // slight buffer over handler timeout
	}

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("Starting server", "address", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	// 7. Graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-srvErr:
		logger.Error("Server error:", "error", err)
	case sig := <-quit:
		logger.Info("Received signal, shutting down", "signal", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("Server shutdown error", "error", err)
		} else {
			logger.Info("Server shutdown complete")
		}
	}
}
