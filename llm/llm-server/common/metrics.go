package common

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
	meter                         metric.Meter
	metricsApiRequestsFailedTotal metric.Int64Counter
	metricsApiRequestsTotal       metric.Int64Counter

	metricsAgentOperationsTotal     metric.Int64Counter
	metricsAgentLatencySeconds      metric.Float64Histogram
	metricsToolOperationsTotal      metric.Int64Counter
	metricsToolLatencySeconds       metric.Float64Histogram
	metricsLLMRequestsTotal         metric.Int64Counter
	metricsLLMTokensTotal           metric.Int64Counter
	metricsLLMLatencySeconds        metric.Float64Histogram
	metricsLLMCacheTotal            metric.Int64Counter
	metricsLLMCachedTokensTotal     metric.Int64Counter
	metricsLLMCircuitBreakerTripped metric.Int64Counter
	metricsLLMRateLimitHitsTotal    metric.Int64Counter

	// Event analyzer metrics
	metricsEventAnalysisOperationsTotal metric.Int64Counter
	metricsEventAnalysisLatencySeconds  metric.Float64Histogram

	// Observable gauges for connection pool and worker pool stats
	metricsDBConnectionsInUse   metric.Int64ObservableGauge
	metricsDBConnectionsIdle    metric.Int64ObservableGauge
	metricsDBConnectionsWait    metric.Int64ObservableGauge
	metricsDBConnectionsMaxOpen metric.Int64ObservableGauge
	metricsWorkerPoolPending    metric.Int64ObservableGauge
	metricsWorkerPoolSize       metric.Int64ObservableGauge

	initMetricsOnce sync.Once
)

// Histogram bucket boundaries tuned for LLM and agent operations.
// LLM calls typically range from 0.5s to 120s+.
var llmLatencyBuckets = metric.WithExplicitBucketBoundaries(
	0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300,
)

// Tool and agent operations can be faster (sub-second) or slow (multi-minute).
var operationLatencyBuckets = metric.WithExplicitBucketBoundaries(
	0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600,
)

func InitMetrics() {
	initMetricsOnce.Do(func() {
		meter = otel.Meter(config.SERVICE_NAME)
		var err error

		// Counter names omit the _total suffix; the OTel-to-Prometheus
		// exporter appends it automatically. Including it in the OTel name
		// risks a double suffix (nb_llm_…_total_total) on receivers that
		// do not deduplicate.

		metricsApiRequestsFailedTotal, err = meter.Int64Counter(
			"nb_llm_api_requests_failed",
			metric.WithDescription("Total number of API requests that failed"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_api_requests_failed metric", "error", err)
		}

		metricsApiRequestsTotal, err = meter.Int64Counter(
			"nb_llm_api_requests",
			metric.WithDescription("Total number of API requests processed"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_api_requests metric", "error", err)
		}

		// Agent operations
		metricsAgentOperationsTotal, err = meter.Int64Counter(
			"nb_llm_agent_operations",
			metric.WithDescription("Total number of agent operations"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_agent_operations metric", "error", err)
		}

		// Histogram names omit the unit suffix; the OTel SDK propagates
		// the unit via metadata and the Prometheus exporter appends _seconds.
		metricsAgentLatencySeconds, err = meter.Float64Histogram(
			"nb_llm_agent_latency",
			metric.WithDescription("Agent latency"),
			metric.WithUnit("s"),
			operationLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_agent_latency metric", "error", err)
		}

		// Tool operations
		metricsToolOperationsTotal, err = meter.Int64Counter(
			"nb_llm_tool_operations",
			metric.WithDescription("Total number of tool operations"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_operations metric", "error", err)
		}

		metricsToolLatencySeconds, err = meter.Float64Histogram(
			"nb_llm_tool_latency",
			metric.WithDescription("Tool latency"),
			metric.WithUnit("s"),
			operationLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_latency metric", "error", err)
		}

		// LLM requests
		metricsLLMRequestsTotal, err = meter.Int64Counter(
			"nb_llm_llm_requests",
			metric.WithDescription("Total number of LLM requests"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_llm_requests metric", "error", err)
		}

		// LLM tokens (input/output)
		metricsLLMTokensTotal, err = meter.Int64Counter(
			"nb_llm_llm_tokens",
			metric.WithDescription("Total LLM tokens"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_llm_tokens metric", "error", err)
		}

		// LLM latency
		metricsLLMLatencySeconds, err = meter.Float64Histogram(
			"nb_llm_llm_latency",
			metric.WithDescription("LLM call latency"),
			metric.WithUnit("s"),
			llmLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_llm_latency metric", "error", err)
		}

		// LLM cache hits/misses
		metricsLLMCacheTotal, err = meter.Int64Counter(
			"nb_llm_cache",
			metric.WithDescription("Total LLM cache operations (hit/miss/error)"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_cache metric", "error", err)
		}

		// LLM cached tokens saved
		metricsLLMCachedTokensTotal, err = meter.Int64Counter(
			"nb_llm_cached_tokens",
			metric.WithDescription("Total LLM tokens saved by caching"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_cached_tokens metric", "error", err)
		}

		// LLM circuit breaker trips
		metricsLLMCircuitBreakerTripped, err = meter.Int64Counter(
			"nb_llm_circuit_breaker_tripped",
			metric.WithDescription("Total number of LLM circuit breaker trips"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_circuit_breaker_tripped metric", "error", err)
		}

		// LLM rate limit hits
		metricsLLMRateLimitHitsTotal, err = meter.Int64Counter(
			"nb_llm_rate_limit_hits",
			metric.WithDescription("Total number of LLM rate limit errors encountered"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_rate_limit_hits metric", "error", err)
		}

		// Event analyzer operations
		metricsEventAnalysisOperationsTotal, err = meter.Int64Counter(
			"nb_llm_event_analysis_operations",
			metric.WithDescription("Total number of event analysis operations"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_event_analysis_operations metric", "error", err)
		}

		// Event analyzer latency
		metricsEventAnalysisLatencySeconds, err = meter.Float64Histogram(
			"nb_llm_event_analysis_latency",
			metric.WithDescription("Event analysis latency"),
			metric.WithUnit("s"),
			operationLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_event_analysis_latency metric", "error", err)
		}

		// DB connection pool gauges
		metricsDBConnectionsInUse, err = meter.Int64ObservableGauge(
			"nb_llm_db_connections_in_use",
			metric.WithDescription("Number of database connections currently in use"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_db_connections_in_use metric", "error", err)
		}
		metricsDBConnectionsIdle, err = meter.Int64ObservableGauge(
			"nb_llm_db_connections_idle",
			metric.WithDescription("Number of idle database connections"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_db_connections_idle metric", "error", err)
		}
		// WaitCount is monotonically increasing since pool creation.
		// Use rate() in Prometheus to derive wait rate from this gauge snapshot.
		metricsDBConnectionsWait, err = meter.Int64ObservableGauge(
			"nb_llm_db_connections_wait_count",
			metric.WithDescription("Cumulative number of connections waited for since pool creation"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_db_connections_wait_count metric", "error", err)
		}
		metricsDBConnectionsMaxOpen, err = meter.Int64ObservableGauge(
			"nb_llm_db_connections_max_open",
			metric.WithDescription("Maximum number of open database connections"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_db_connections_max_open metric", "error", err)
		}
		if metricsDBConnectionsInUse != nil && metricsDBConnectionsIdle != nil &&
			metricsDBConnectionsWait != nil && metricsDBConnectionsMaxOpen != nil {
			_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				for name, s := range GetAllDatabaseStats() {
					attr := metric.WithAttributes(attribute.String("db", name))
					o.ObserveInt64(metricsDBConnectionsInUse, int64(s.InUse), attr)
					o.ObserveInt64(metricsDBConnectionsIdle, int64(s.Idle), attr)
					o.ObserveInt64(metricsDBConnectionsWait, s.WaitCount, attr)
					o.ObserveInt64(metricsDBConnectionsMaxOpen, int64(s.MaxOpen), attr)
				}
				return nil
			}, metricsDBConnectionsInUse, metricsDBConnectionsIdle, metricsDBConnectionsWait, metricsDBConnectionsMaxOpen)
			if err != nil {
				slog.Error("metrics: failed to register db connection pool callback", "error", err)
			}
		}

		// Worker pool gauges
		metricsWorkerPoolPending, err = meter.Int64ObservableGauge(
			"nb_llm_worker_pool_pending_tasks",
			metric.WithDescription("Number of pending tasks in worker pool"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_worker_pool_pending_tasks metric", "error", err)
		}
		metricsWorkerPoolSize, err = meter.Int64ObservableGauge(
			"nb_llm_worker_pool_workers",
			metric.WithDescription("Number of workers in worker pool"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_worker_pool_workers metric", "error", err)
		}
		if metricsWorkerPoolPending != nil && metricsWorkerPoolSize != nil {
			_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				for _, s := range GetAllWorkerPoolStats() {
					attr := metric.WithAttributes(attribute.String("pool", s.Name))
					o.ObserveInt64(metricsWorkerPoolPending, int64(s.Pending), attr)
					o.ObserveInt64(metricsWorkerPoolSize, int64(s.NumWorkers), attr)
				}
				return nil
			}, metricsWorkerPoolPending, metricsWorkerPoolSize)
			if err != nil {
				slog.Error("metrics: failed to register worker pool callback", "error", err)
			}
		}
	})
}

// MetricsAgentLatencySeconds records the agent latency histogram.
func MetricsAgentLatencySeconds(agent, accountID string, latencySeconds float64) {
	InitMetrics()
	if metricsAgentLatencySeconds == nil {
		slog.Warn("metrics: metricsAgentLatencySeconds is not initialized")
		return
	}
	metricsAgentLatencySeconds.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("agent", agent),
		attribute.String("account_id", accountID),
	))
}

// MetricsAgentOperationsTotal increments the agent operations counter.
func MetricsAgentOperationsTotal(agent, status, accountID string) {
	InitMetrics()
	if metricsAgentOperationsTotal == nil {
		slog.Warn("metrics: metricsAgentOperationsTotal is not initialized")
		return
	}
	metricsAgentOperationsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("agent", agent),
		attribute.String("status", status),
		attribute.String("account_id", accountID),
	))
}

// MetricsToolOperationsTotal increments the tool operations counter.
func MetricsToolOperationsTotal(tool, status, accountID string) {
	InitMetrics()
	if metricsToolOperationsTotal == nil {
		slog.Warn("metrics: metricsToolOperationsTotal is not initialized")
		return
	}
	metricsToolOperationsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("tool", tool),
		attribute.String("status", status),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMRequestsTotal increments the LLM requests counter.
func MetricsLLMRequestsTotal(provider, model, status, accountID string) {
	InitMetrics()
	if metricsLLMRequestsTotal == nil {
		slog.Warn("metrics: metricsLLMRequestsTotal is not initialized")
		return
	}
	metricsLLMRequestsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("status", status),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMTokensTotal increments the LLM tokens counter.
func MetricsLLMTokensTotal(provider, model, direction, accountID string, count int64) {
	InitMetrics()
	if metricsLLMTokensTotal == nil {
		slog.Warn("metrics: metricsLLMTokensTotal is not initialized")
		return
	}
	metricsLLMTokensTotal.Add(context.Background(), count, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("direction", direction),
		attribute.String("account_id", accountID),
	))
}

func MetricsApiRequestsFailedTotal(apiModule string, reason string) {
	InitMetrics()
	if metricsApiRequestsFailedTotal == nil {
		slog.Warn("metrics: metricsApiRequestsFailedTotal is not initialized")
		return
	}
	metricsApiRequestsFailedTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason), attribute.String("module", apiModule)))
}

func MetricsApiRequestsTotal(apiModule string) {
	InitMetrics()
	if metricsApiRequestsTotal == nil {
		slog.Warn("metrics: metricsApiRequestsTotal is not initialized")
		return
	}
	metricsApiRequestsTotal.Add(context.Background(), 1, metric.WithAttributes(attribute.String("module", apiModule)))
}

// MetricsToolLatencySeconds records the tool latency histogram.
func MetricsToolLatencySeconds(tool, accountID string, latencySeconds float64) {
	InitMetrics()
	if metricsToolLatencySeconds == nil {
		slog.Warn("metrics: metricsToolLatencySeconds is not initialized")
		return
	}
	metricsToolLatencySeconds.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("tool", tool),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMLatencySeconds records the LLM latency histogram.
func MetricsLLMLatencySeconds(provider, model, accountID string, latencySeconds float64) {
	InitMetrics()
	if metricsLLMLatencySeconds == nil {
		slog.Warn("metrics: metricsLLMLatencySeconds is not initialized")
		return
	}
	metricsLLMLatencySeconds.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMCacheTotal increments the LLM cache counter.
// status should be one of: "hit", "miss", "error"
func MetricsLLMCacheTotal(provider, model, status, accountID string) {
	InitMetrics()
	if metricsLLMCacheTotal == nil {
		slog.Warn("metrics: metricsLLMCacheTotal is not initialized")
		return
	}
	metricsLLMCacheTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("status", status),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMCircuitBreakerTripped increments the circuit breaker trip counter.
func MetricsLLMCircuitBreakerTripped(provider, model string) {
	InitMetrics()
	if metricsLLMCircuitBreakerTripped == nil {
		slog.Warn("metrics: metricsLLMCircuitBreakerTripped is not initialized")
		return
	}
	metricsLLMCircuitBreakerTripped.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
	))
}

// MetricsLLMRateLimitHitsTotal increments the rate limit hits counter.
func MetricsLLMRateLimitHitsTotal(provider, model, accountID string) {
	InitMetrics()
	if metricsLLMRateLimitHitsTotal == nil {
		slog.Warn("metrics: metricsLLMRateLimitHitsTotal is not initialized")
		return
	}
	metricsLLMRateLimitHitsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("account_id", accountID),
	))
}

// MetricsLLMCachedTokensTotal increments the cached tokens counter.
func MetricsLLMCachedTokensTotal(provider, model, accountID string, count int64) {
	InitMetrics()
	if metricsLLMCachedTokensTotal == nil {
		slog.Warn("metrics: metricsLLMCachedTokensTotal is not initialized")
		return
	}
	metricsLLMCachedTokensTotal.Add(context.Background(), count, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("account_id", accountID),
	))
}

// MetricsEventAnalysisOperationsTotal increments the event analysis operations counter.
func MetricsEventAnalysisOperationsTotal(analysisType, status, accountID string) {
	InitMetrics()
	if metricsEventAnalysisOperationsTotal == nil {
		slog.Warn("metrics: metricsEventAnalysisOperationsTotal is not initialized")
		return
	}
	metricsEventAnalysisOperationsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("analysis_type", analysisType),
		attribute.String("status", status),
		attribute.String("account_id", accountID),
	))
}

// MetricsEventAnalysisLatencySeconds records the event analysis latency histogram.
func MetricsEventAnalysisLatencySeconds(analysisType, accountID string, latencySeconds float64) {
	InitMetrics()
	if metricsEventAnalysisLatencySeconds == nil {
		slog.Warn("metrics: metricsEventAnalysisLatencySeconds is not initialized")
		return
	}
	metricsEventAnalysisLatencySeconds.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("analysis_type", analysisType),
		attribute.String("account_id", accountID),
	))
}
