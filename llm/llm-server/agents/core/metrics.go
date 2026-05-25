package core

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
	coreMeter metric.Meter

	metricsLLMConcurrencyInUse metric.Int64ObservableGauge
	metricsLLMConcurrencyMax   metric.Int64ObservableGauge

	// Scratchpad observation summarization metrics.
	metricsScratchpadSummarization         metric.Int64Counter
	metricsScratchpadSummarizationLatency  metric.Float64Histogram
	metricsScratchpadSummarizationFallback metric.Int64Counter

	initCoreMetricsOnce sync.Once
)

// scratchpadSummarizationLatencyBuckets covers the expected range for a lite-model call.
var scratchpadSummarizationLatencyBuckets = metric.WithExplicitBucketBoundaries(
	0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5,
)

// InitMetrics initializes OpenTelemetry metrics for the core agent package.
// It must be called after the global OTel provider is configured.
func InitMetrics() {
	initCoreMetricsOnce.Do(func() {
		coreMeter = otel.Meter(config.SERVICE_NAME)
		var err error

		metricsLLMConcurrencyInUse, err = coreMeter.Int64ObservableGauge(
			"nb_llm_concurrency_in_use",
			metric.WithDescription("Current number of in-flight LLM calls, labeled by account_id, provider, model"),
			metric.WithUnit("1"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_concurrency_in_use metric", "error", err)
		}

		metricsLLMConcurrencyMax, err = coreMeter.Int64ObservableGauge(
			"nb_llm_concurrency_max",
			metric.WithDescription("Maximum allowed concurrent LLM calls, labeled by account_id, provider, model"),
			metric.WithUnit("1"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_concurrency_max metric", "error", err)
		}

		if metricsLLMConcurrencyInUse != nil && metricsLLMConcurrencyMax != nil {
			_, err = coreMeter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				for key, s := range GetLlmConcurrencyStats() {
					attr := metric.WithAttributes(attribute.String("key", key))
					o.ObserveInt64(metricsLLMConcurrencyInUse, int64(s.CurrentInUse), attr)
					o.ObserveInt64(metricsLLMConcurrencyMax, int64(s.MaxConcurrent), attr)
				}
				return nil
			}, metricsLLMConcurrencyInUse, metricsLLMConcurrencyMax)
			if err != nil {
				slog.Error("metrics: failed to register llm concurrency callback", "error", err)
			}
		}

		metricsScratchpadSummarization, err = coreMeter.Int64Counter(
			"nb_llm_scratchpad_summarization",
			metric.WithDescription("Number of scratchpad observation summarizations performed"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_scratchpad_summarization metric", "error", err)
		}

		metricsScratchpadSummarizationLatency, err = coreMeter.Float64Histogram(
			"nb_llm_scratchpad_summarization_latency",
			metric.WithDescription("Latency of scratchpad observation summarization calls"),
			metric.WithUnit("s"),
			scratchpadSummarizationLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_scratchpad_summarization_latency metric", "error", err)
		}

		metricsScratchpadSummarizationFallback, err = coreMeter.Int64Counter(
			"nb_llm_scratchpad_summarization_fallback",
			metric.WithDescription("Number of scratchpad summarizations that fell back to byte truncation"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_scratchpad_summarization_fallback metric", "error", err)
		}
	})
}

// MetricsScratchpadSummarization records a summarization attempt to OTel.
func MetricsScratchpadSummarization(toolName, status string, latencySeconds float64) {
	InitMetrics()
	attrs := metric.WithAttributes(
		attribute.String("tool", toolName),
		attribute.String("status", status),
	)
	if metricsScratchpadSummarization != nil {
		metricsScratchpadSummarization.Add(context.Background(), 1, attrs)
	}
	if metricsScratchpadSummarizationLatency != nil {
		metricsScratchpadSummarizationLatency.Record(context.Background(), latencySeconds, attrs)
	}
}

// MetricsScratchpadSummarizationFallback records a fallback from LLM summarization to byte truncation.
func MetricsScratchpadSummarizationFallback(toolName, reason string) {
	InitMetrics()
	if metricsScratchpadSummarizationFallback != nil {
		metricsScratchpadSummarizationFallback.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("tool", toolName),
			attribute.String("reason", reason),
		))
	}
}
