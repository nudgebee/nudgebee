package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nudgebee/runbook/api"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/events"
	"nudgebee/runbook/internal/storage"
	"nudgebee/runbook/internal/system"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/workflow"
	configSvc "nudgebee/runbook/services/config"
	"nudgebee/runbook/services/optimizer"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
)

// @title           Nudgebee Runbook API
// @version         1.0
// @description     Runbook server — manages workflows, executions, configs, approvals, and RPC action entry points for Nudgebee. Docs are auto-merged: handlers with godoc annotations get rich entries; everything else is stubbed from the gin route table.
// @BasePath        /
// @securityDefinitions.apikey ActionToken
// @in              header
// @name            X-ACTION-TOKEN
// @description     Internal service-to-service auth token. Default header name (configurable via `action_api_server_token_header`); value is the `action_api_server_token` config value.
func main() {
	logger := newLogger()
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

	if config.Config.RunbookServerDBUrl == "" {
		slog.Info("DATABASE_URL environment variable not set, using default")
	}

	dbStore, err := storage.NewWorkflowDao()
	if err != nil {
		slog.Error("unable to create DB store", "error", err)
		os.Exit(1)
	}

	// Initialize TaskRegistry after dbStore is available
	taskRegistry := tasks.NewInitializedTaskRegistry()

	configService, err := configSvc.NewService()
	if err != nil {
		slog.Error("unable to create config service", "error", err)
		os.Exit(1)
	}

	// Initialize Event Registry
	eventRegistry := events.NewEventRegistry(dbStore, logger)

	// First, create the temporal client and data converter
	temporalGRPCAddress := config.Config.TemporalGRPCAddress
	if temporalGRPCAddress == "" {
		temporalGRPCAddress = "localhost:7233" // Default Temporal gRpc address
	}
	dc := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		workflow.NewCompressionCodec(1024), // Compress payloads larger than 1KB
	)
	temporalClient, err := client.Dial(client.Options{
		HostPort:      temporalGRPCAddress,
		DataConverter: dc,
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64 * 1024 * 1024)),
			},
		},
	})
	if err != nil {
		slog.Error("unable to create Temporal client", "error", err)
		os.Exit(1)
	}
	defer temporalClient.Close()

	// Initialize Optimizer Service
	optimizerDao, err := storage.NewOptimizerDao()
	if err != nil {
		slog.Error("unable to create optimizer dao", "error", err)
		os.Exit(1)
	}
	optimizerService := optimizer.NewService(optimizerDao, temporalClient)

	// Ensure Temporal Schedules exist for all active optimizations (Migration/Sync)
	if config.Config.OptimizationEnabled {
		go func() {
			if err := optimizerService.SyncSchedules(context.Background()); err != nil {
				slog.Error("failed to sync optimizer schedules", "error", err)
			}
		}()
	}

	executor, err := workflow.NewWorkflowExecutor(dbStore, configService, temporalClient, dc)
	if err != nil {
		slog.Error("unable to create workflow executor", "error", err)
		os.Exit(1)
	}

	// Initialize template store
	templateStore, err := storage.NewWorkflowTemplateDao()
	if err != nil {
		slog.Error("unable to create template store", "error", err)
		os.Exit(1)
	}

	// Create workflow service now
	workflowService := workflow.NewService(temporalClient, dbStore, dc, taskRegistry, executor, configService, templateStore)

	// Initialize and start Event Consumer
	eventConsumer := events.NewConsumer(eventRegistry, workflowService, logger)
	if err := eventConsumer.Start(config.Config.RabbitMqRunbookEventExchange, config.Config.RabbitMqRunbookEventRoutingKey, config.Config.RabbitMqRunbookEventQueue); err != nil {
		slog.Error("failed to start event consumer, continuing without it", "error", err)
	}

	// Resume llm.event_investigate activities when llm-server signals completion.
	// Failure to start is non-fatal — workflows fall back to activity timeout.
	investigationCompletionConsumer := events.NewInvestigationCompletionConsumer(temporalClient, logger)
	if err := investigationCompletionConsumer.Start(); err != nil {
		slog.Error("failed to start investigation completion consumer, continuing without it", "error", err)
	}

	// Register tasks with the worker
	taskWorker := executor.GetWorker()
	for _, task := range taskRegistry.ListTasks() { // Use the new ListTasks with taskRegistry
		wrapper := &tasks.TaskWrapper{Task: task, TemporalClient: executor.GetClient(), Store: dbStore, Converter: dc}
		taskWorker.RegisterActivityWithOptions(wrapper.Execute, activity.RegisterOptions{
			Name: task.GetName(),
		})
	}

	go func() {
		if err := executor.Start(); err != nil {
			slog.Error("unable to start workflow executor", "error", err)
			os.Exit(1)
		}
	}()
	defer executor.Stop()

	// --- System Worker Setup ---
	systemWorker := worker.New(temporalClient, system.SystemTaskQueue, worker.Options{})
	systemActivities := system.NewSystemActivities(dbStore)
	systemWorker.RegisterActivityWithOptions(systemActivities.CleanupExpiredStateActivity, activity.RegisterOptions{
		Name: system.CleanupExpiredStateActivityName,
	})
	systemWorker.RegisterActivityWithOptions(systemActivities.CronWebhookActivity, activity.RegisterOptions{
		Name: system.CronWebhookActivityName,
	})
	systemWorker.RegisterWorkflow(system.SystemCleanupWorkflow)
	systemWorker.RegisterWorkflow(system.CronWebhookWorkflow)

	go func() {
		if err := systemWorker.Run(worker.InterruptCh()); err != nil {
			slog.Error("unable to start system worker", "error", err)
			os.Exit(1)
		}
	}()
	defer systemWorker.Stop()

	// --- Optimizer Worker Setup ---
	if config.Config.OptimizationEnabled {
		optimizerActivities := optimizer.NewActivities(optimizerService, optimizerDao)
		optimizerWorker := worker.New(temporalClient, workflow.OptimizerTaskQueue, worker.Options{})
		optimizerWorker.RegisterWorkflow(workflow.OptimizerWorkflow)
		optimizerWorker.RegisterActivityWithOptions(optimizerActivities.GenerateTasksActivity, activity.RegisterOptions{Name: workflow.GenerateTasksActivityName})
		optimizerWorker.RegisterActivityWithOptions(optimizerActivities.ExecuteTaskActivity, activity.RegisterOptions{Name: workflow.ExecuteTaskActivityName})
		optimizerWorker.RegisterActivityWithOptions(optimizerActivities.CompleteAutoOptimizeActivity, activity.RegisterOptions{Name: workflow.CompleteAutoOptimizeActivityName})

		go func() {
			if err := optimizerWorker.Run(worker.InterruptCh()); err != nil {
				slog.Error("unable to start optimizer worker", "error", err)
				os.Exit(1)
			}
		}()
		defer optimizerWorker.Stop()
	}

	// --- System Schedule Setup ---
	// Load cron triggers from embedded YAML config (similar to RPC cron_triggers.yaml)
	cronTriggers, err := system.LoadCronTriggers()
	if err != nil {
		slog.Warn("Failed to load cron triggers, continuing without cron schedules", "error", err)
		cronTriggers = nil
	}

	sysManager := system.NewSystemJobManager(temporalClient, slog.Default())
	if err := sysManager.EnsureSchedules(context.Background(), cronTriggers); err != nil {
		slog.Error("failed to ensure system schedules, terminating", "error", err)
		os.Exit(1)
	}

	// Ensure Search Attributes
	// This might fail if the user (temporal client) doesn't have permissions (e.g. generic worker vs admin).
	// We log warn but don't exit, as they might already exist.
	if err := system.EnsureSearchAttributes(context.Background(), temporalClient, slog.Default(), "default"); err != nil {
		slog.Warn("Failed to ensure search attributes (this is expected if not admin)", "error", err)
	}
	// ---------------------------

	var tracer = otel.Tracer(config.SERVICE_NAME)
	var meter = otel.Meter(config.SERVICE_NAME)
	// Start the web server
	s := api.NewServerWithObservability(nil, configService, &tracer, &meter)

	s.SetWorkflowService(workflowService)
	s.SetOptimizerService(optimizerService)

	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Event Registry Sync
	go eventRegistry.StartSync(ctx, time.Duration(config.Config.RunbookServerEventSyncIntervalSeconds)*time.Second)

	// Start Recommendation Poller for optimization triggers
	pollInterval := time.Duration(config.Config.OptimizationRecommendationPollIntervalSeconds) * time.Second
	recommendationPoller := events.NewRecommendationPoller(dbStore, eventRegistry, workflowService, logger, pollInterval)
	go recommendationPoller.Start(ctx)

	// Listen for OS signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("Received signal, shutting down...", "signal", sig)
		cancel()
	}()

	apiPort := config.Config.APIPort
	apiAddress := ":" + apiPort

	// Start the server in a goroutine
	go func() {
		slog.Info("Attempting to start web server...", "address", apiAddress)
		if err := s.Start(apiAddress); err != nil {
			slog.Info("web server stopped with error", "error", err)
		} else {
			slog.Info("web server started successfully (should not happen if ListenAndServe blocks)")
		}
	}()

	// Wait for the context to be cancelled
	<-ctx.Done()

	// Give the server some time to gracefully shut down
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := s.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("server gracefully stopped")
}
