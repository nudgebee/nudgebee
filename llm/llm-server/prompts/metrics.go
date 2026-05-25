package prompts

import (
	"context"
	"log/slog"
	"nudgebee/llm/config"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter metric.Meter

	// Prompt loading metrics
	metricsPromptLoadsTotal      metric.Int64Counter
	metricsPromptLoadLatency     metric.Float64Histogram
	metricsPromptCacheTotal      metric.Int64Counter
	metricsPromptConfigSource    metric.Int64Counter
	metricsPromptExperimentTotal metric.Int64Counter
	metricsPromptErrorsTotal     metric.Int64Counter

	initMetricsOnce sync.Once
)

// InitMetrics initializes OpenTelemetry metrics for the prompt system
func InitMetrics() {
	initMetricsOnce.Do(func() {
		meter = otel.Meter(config.SERVICE_NAME)
		var err error

		// Counter names omit _total; the Prometheus exporter appends it.
		metricsPromptLoadsTotal, err = meter.Int64Counter(
			"nb_llm_prompt_loads",
			metric.WithDescription("Total number of prompt loads"),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_loads", "error", err)
		}

		// Use seconds (base unit) instead of milliseconds per OTel convention.
		// The Prometheus exporter appends _seconds from the unit metadata.
		metricsPromptLoadLatency, err = meter.Float64Histogram(
			"nb_llm_prompt_load_latency",
			metric.WithDescription("Prompt load latency"),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_load_latency", "error", err)
		}

		metricsPromptCacheTotal, err = meter.Int64Counter(
			"nb_llm_prompt_cache",
			metric.WithDescription("Total prompt cache operations (hit/miss)"),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_cache", "error", err)
		}

		metricsPromptConfigSource, err = meter.Int64Counter(
			"nb_llm_prompt_config_source",
			metric.WithDescription("Total prompts by config source"),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_config_source", "error", err)
		}

		metricsPromptExperimentTotal, err = meter.Int64Counter(
			"nb_llm_prompt_experiment",
			metric.WithDescription("Total experiment requests"),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_experiment", "error", err)
		}

		metricsPromptErrorsTotal, err = meter.Int64Counter(
			"nb_llm_prompt_errors",
			metric.WithDescription("Total prompt loading errors"),
		)
		if err != nil {
			slog.Error("prompts metrics: failed to create nb_llm_prompt_errors", "error", err)
		}

		slog.Info("prompts metrics: initialized successfully")
	})
}

// RecordPromptLoad records a successful prompt load.
// latencySeconds is the load latency in seconds.
func RecordPromptLoad(name, category, provider, version string, latencySeconds float64, cacheHit bool, configSource string, experimentName string, accountID string) {
	InitMetrics()
	ctx := context.Background()

	attrs := []attribute.KeyValue{
		attribute.String("prompt_name", name),
		attribute.String("category", category),
		attribute.String("provider", provider),
		attribute.String("version", version),
	}

	if accountID != "" {
		attrs = append(attrs, attribute.String("account_id", accountID))
	}

	// Total loads
	if metricsPromptLoadsTotal != nil {
		metricsPromptLoadsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	// Latency (now in seconds)
	if metricsPromptLoadLatency != nil {
		metricsPromptLoadLatency.Record(ctx, latencySeconds, metric.WithAttributes(attrs...))
	}

	// Cache status
	if metricsPromptCacheTotal != nil {
		cacheStatus := "miss"
		if cacheHit {
			cacheStatus = "hit"
		}
		cacheAttrs := []attribute.KeyValue{
			attribute.String("prompt_name", name),
			attribute.String("category", category),
			attribute.String("status", cacheStatus),
		}
		metricsPromptCacheTotal.Add(ctx, 1, metric.WithAttributes(cacheAttrs...))
	}

	// Config source
	if metricsPromptConfigSource != nil {
		sourceAttrs := []attribute.KeyValue{
			attribute.String("source", configSource),
			attribute.String("prompt_name", name),
			attribute.String("category", category),
		}
		metricsPromptConfigSource.Add(ctx, 1, metric.WithAttributes(sourceAttrs...))
	}

	// Experiment tracking
	if experimentName != "" && metricsPromptExperimentTotal != nil {
		expAttrs := []attribute.KeyValue{
			attribute.String("experiment_name", experimentName),
			attribute.String("prompt_name", name),
			attribute.String("test_version", version),
		}
		if accountID != "" {
			expAttrs = append(expAttrs, attribute.String("account_id", accountID))
		}
		metricsPromptExperimentTotal.Add(ctx, 1, metric.WithAttributes(expAttrs...))
	}
}

// RecordPromptError records a prompt loading error
func RecordPromptError(name, category, errorType string) {
	InitMetrics()
	ctx := context.Background()

	if metricsPromptErrorsTotal != nil {
		attrs := []attribute.KeyValue{
			attribute.String("prompt_name", name),
			attribute.String("category", category),
			attribute.String("error_type", errorType),
		}
		metricsPromptErrorsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

// RecordCacheOperation records a cache operation (used for manual cache operations)
func RecordCacheOperation(name, category, operation string) {
	InitMetrics()
	ctx := context.Background()

	if metricsPromptCacheTotal != nil {
		attrs := []attribute.KeyValue{
			attribute.String("prompt_name", name),
			attribute.String("category", category),
			attribute.String("status", operation),
		}
		metricsPromptCacheTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}
