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

	metricsAgentOperationsTotal       metric.Int64Counter
	metricsAgentLatencySeconds        metric.Float64Histogram
	metricsToolOperationsTotal        metric.Int64Counter
	metricsToolLatencySeconds         metric.Float64Histogram
	metricsLLMRequestsTotal           metric.Int64Counter
	metricsLLMTokensTotal             metric.Int64Counter
	metricsLLMLatencySeconds          metric.Float64Histogram
	metricsLLMCacheTotal              metric.Int64Counter
	metricsLLMCachedTokensTotal       metric.Int64Counter
	metricsLLMCacheInvalidationsTotal metric.Int64Counter
	metricsLLMCircuitBreakerTripped   metric.Int64Counter
	metricsLLMRateLimitHitsTotal      metric.Int64Counter

	// Event analyzer metrics
	metricsEventAnalysisOperationsTotal metric.Int64Counter
	metricsEventAnalysisLatencySeconds  metric.Float64Histogram

	// Tool-integration metrics. Per-call invocations and latency are not
	// duplicated here — the planner records every tool.Call into the
	// generic nb_llm_tool_operations / nb_llm_tool_latency metrics at the
	// central choke point, and a circuit-breaker fast-fail emits
	// status=circuit_open on the same generic counter. Discovery, cache,
	// healthy, and per-(account, integration) tool-count gauges are
	// generic by name but carry a type label (currently "mcp") so future
	// non-MCP tool types can slot in without another rename.
	metricsToolDiscovery        metric.Int64Counter
	metricsToolDiscoveryLatency metric.Float64Histogram
	metricsToolCacheLookup      metric.Int64Counter
	metricsToolHealthy          metric.Int64ObservableGauge
	metricsToolsAvailable       metric.Int64ObservableGauge

	// Observable gauges for connection pool and worker pool stats
	metricsDBConnectionsInUse   metric.Int64ObservableGauge
	metricsDBConnectionsIdle    metric.Int64ObservableGauge
	metricsDBConnectionsWait    metric.Int64ObservableGauge
	metricsDBConnectionsMaxOpen metric.Int64ObservableGauge
	metricsWorkerPoolPending    metric.Int64ObservableGauge
	metricsWorkerPoolSize       metric.Int64ObservableGauge

	// Snapshot callbacks supplied by tools/core for the observable
	// tool-integration gauges. Stored as a slice (not a single function
	// pointer) so more than one tool-type package can register its own
	// snapshot without a later registration silently clobbering an
	// earlier one; the callback concatenates all of their results.
	toolHealthSnapshotMux sync.RWMutex
	toolHealthSnapshotFns []func() []ToolHealthEntry
	toolsAvailableMux     sync.RWMutex
	toolsAvailableFns     []func() []ToolsAvailableEntry

	initMetricsOnce sync.Once
)

// ToolHealthEntry is a per-(type, account, integration) snapshot reported
// by a tool-level circuit breaker. Healthy=true means calls would
// proceed; Healthy=false means the breaker is currently open. Type
// identifies the tool implementation class (e.g. "mcp"); IntegrationId
// is whatever identifier the implementation chose to key by.
type ToolHealthEntry struct {
	Type          string
	AccountId     string
	IntegrationId string
	Healthy       bool
}

// ToolsAvailableEntry is a per-(type, account, integration) tool count
// snapshot. Used to expose how many tools a given integration is
// currently surfacing to agents.
type ToolsAvailableEntry struct {
	Type          string
	AccountId     string
	IntegrationId string
	ToolCount     int
}

// RegisterToolHealthSnapshot lets tools/core supply a function used by the
// nb_llm_tool_healthy observable gauge. Safe to call more than once — each
// registered function's entries are concatenated in the gauge callback, so
// multiple tool-type packages can each report their own view.
func RegisterToolHealthSnapshot(fn func() []ToolHealthEntry) {
	toolHealthSnapshotMux.Lock()
	defer toolHealthSnapshotMux.Unlock()
	toolHealthSnapshotFns = append(toolHealthSnapshotFns, fn)
}

// RegisterToolsAvailableSnapshot lets tools/core supply a function used by
// the nb_llm_tools_available observable gauge. Safe to call more than
// once — see RegisterToolHealthSnapshot.
func RegisterToolsAvailableSnapshot(fn func() []ToolsAvailableEntry) {
	toolsAvailableMux.Lock()
	defer toolsAvailableMux.Unlock()
	toolsAvailableFns = append(toolsAvailableFns, fn)
}

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

		// LLM cache invalidations (provider cache torn down before its planned TTL)
		metricsLLMCacheInvalidationsTotal, err = meter.Int64Counter(
			"nb_llm_cache_invalidations",
			metric.WithDescription("Total LLM provider caches invalidated before expiry, by scope and reason"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_cache_invalidations metric", "error", err)
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

		// Tool-integration metrics — discovery + cache-lookup counter, plus
		// observable gauges for circuit-breaker state and per-integration
		// tool counts. Generic names with a type label so non-MCP tool types
		// can land later without another rename. Per-call invocation
		// counts and latency are NOT defined here — the planner emits
		// those on the generic nb_llm_tool_operations / nb_llm_tool_latency
		// at the central tool.Call choke point for every tool implementation.
		metricsToolDiscovery, err = meter.Int64Counter(
			"nb_llm_tool_discovery",
			metric.WithDescription("Tool-integration discovery attempts by type, integration, and outcome"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_discovery metric", "error", err)
		}

		metricsToolDiscoveryLatency, err = meter.Float64Histogram(
			"nb_llm_tool_discovery_latency",
			metric.WithDescription("Tool-integration discovery latency"),
			metric.WithUnit("s"),
			operationLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_discovery_latency metric", "error", err)
		}

		metricsToolCacheLookup, err = meter.Int64Counter(
			"nb_llm_tool_cache_lookup",
			metric.WithDescription("Tool-integration cache lookups by type and outcome (hit|miss)"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_cache_lookup metric", "error", err)
		}

		metricsToolHealthy, err = meter.Int64ObservableGauge(
			"nb_llm_tool_healthy",
			metric.WithDescription("Per-(type, account, integration) tool-integration health: 1 healthy, 0 circuit-open. Only entries with at least one observed failure are reported."),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tool_healthy metric", "error", err)
		}

		metricsToolsAvailable, err = meter.Int64ObservableGauge(
			"nb_llm_tools_available",
			metric.WithDescription("Per-(type, account, integration) cached tool count"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_tools_available metric", "error", err)
		}

		if metricsToolHealthy != nil && metricsToolsAvailable != nil {
			_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				toolHealthSnapshotMux.RLock()
				healthFns := append([]func() []ToolHealthEntry(nil), toolHealthSnapshotFns...)
				toolHealthSnapshotMux.RUnlock()
				for _, healthFn := range healthFns {
					for _, e := range healthFn() {
						var v int64
						if e.Healthy {
							v = 1
						}
						o.ObserveInt64(metricsToolHealthy, v, metric.WithAttributes(
							attribute.String("type", e.Type),
							attribute.String("account_id", e.AccountId),
							attribute.String("integration_id", e.IntegrationId),
						))
					}
				}

				toolsAvailableMux.RLock()
				toolsFns := append([]func() []ToolsAvailableEntry(nil), toolsAvailableFns...)
				toolsAvailableMux.RUnlock()
				for _, toolsFn := range toolsFns {
					for _, e := range toolsFn() {
						o.ObserveInt64(metricsToolsAvailable, int64(e.ToolCount), metric.WithAttributes(
							attribute.String("type", e.Type),
							attribute.String("account_id", e.AccountId),
							attribute.String("integration_id", e.IntegrationId),
						))
					}
				}
				return nil
			}, metricsToolHealthy, metricsToolsAvailable)
			if err != nil {
				slog.Error("metrics: failed to register tool-integration gauges callback", "error", err)
			}
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
// toolType is the implementation class label (e.g. "tool" for built-in,
// "mcp" for MCP integrations) so dashboards can slice by tool family.
func MetricsToolOperationsTotal(toolType, tool, status, accountID string) {
	InitMetrics()
	if metricsToolOperationsTotal == nil {
		slog.Warn("metrics: metricsToolOperationsTotal is not initialized")
		return
	}
	metricsToolOperationsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("type", toolType),
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
// toolType is the implementation class label (e.g. "tool" for built-in,
// "mcp" for MCP integrations).
func MetricsToolLatencySeconds(toolType, tool, accountID string, latencySeconds float64) {
	InitMetrics()
	if metricsToolLatencySeconds == nil {
		slog.Warn("metrics: metricsToolLatencySeconds is not initialized")
		return
	}
	metricsToolLatencySeconds.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("type", toolType),
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

// MetricsLLMCacheInvalidations increments the cache invalidation counter,
// recorded when a live provider cache is torn down before its planned TTL.
// reason should be one of: "content_changed", "explicit".
// Labels are intentionally low-cardinality (no account_id) so the series stays
// aggregatable for alerting on invalidation churn.
func MetricsLLMCacheInvalidations(provider, model, scope, reason string) {
	InitMetrics()
	if metricsLLMCacheInvalidationsTotal == nil {
		slog.Warn("metrics: metricsLLMCacheInvalidationsTotal is not initialized")
		return
	}
	metricsLLMCacheInvalidationsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("scope", scope),
		attribute.String("reason", reason),
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

// MetricsToolDiscovery increments the tool-integration discovery counter.
// Type identifies the tool implementation class (e.g. "mcp"); outcome
// distinguishes the failure mode (success | failure | parse_error |
// json_rpc_error) so dashboards can break down where discovery breaks.
// Labeled with accountId + integrationId (not the display name) so this
// joins cleanly with nb_llm_tool_healthy / nb_llm_tools_available, which
// use the same two identifiers, and so two accounts with an
// identically-named integration don't collapse into one series.
func MetricsToolDiscovery(toolType, accountId, integrationId, outcome string) {
	InitMetrics()
	if metricsToolDiscovery == nil {
		return
	}
	metricsToolDiscovery.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("type", toolType),
		attribute.String("account_id", accountId),
		attribute.String("integration_id", integrationId),
		attribute.String("outcome", outcome),
	))
}

// MetricsToolDiscoveryLatency records the tool-integration discovery
// latency histogram. See MetricsToolDiscovery for the label rationale.
func MetricsToolDiscoveryLatency(toolType, accountId, integrationId string, latencySeconds float64) {
	InitMetrics()
	if metricsToolDiscoveryLatency == nil {
		return
	}
	metricsToolDiscoveryLatency.Record(context.Background(), latencySeconds, metric.WithAttributes(
		attribute.String("type", toolType),
		attribute.String("account_id", accountId),
		attribute.String("integration_id", integrationId),
	))
}

// MetricsToolCacheLookup increments the tool-integration cache lookup
// counter. Type identifies the tool implementation class (e.g. "mcp");
// outcome is hit or miss.
func MetricsToolCacheLookup(toolType, outcome string) {
	InitMetrics()
	if metricsToolCacheLookup == nil {
		return
	}
	metricsToolCacheLookup.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("type", toolType),
		attribute.String("outcome", outcome),
	))
}
