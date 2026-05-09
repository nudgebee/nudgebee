package common

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metric attribute key constants
const (
	MetricKeyModule         = "module"
	MetricKeyReason         = "reason"
	MetricKeyEventSource    = "event_source"
	MetricKeyTenantID       = "tenant_id"
	MetricKeyAccountID      = "account_id"
	MetricKeyAggregationKey = "aggregation_key"
	MetricKeyActionName     = "action_name"
	MetricKeyStatus         = "status"
	MetricKeyDatabase       = "database"
	MetricKeyProvider       = "provider"
	MetricKeyNodeType       = "node_type"
)

// Metric attribute value constants
const (
	// Status values
	MetricStatusCompleted = "completed"

	// Event processing failure reasons
	MetricReasonMissingAccountID               = "missing_account_id"
	MetricReasonMissingTenantID                = "missing_tenant_id"
	MetricReasonEventInsertionFailed           = "event_insertion_failed"
	MetricReasonLLMServerNotConfigured         = "llm_server_not_configured"
	MetricReasonAIAnalysisPayloadMarshalFailed = "ai_analysis_payload_marshal_failed"
	MetricReasonAIAnalysisQueuePublishFailed   = "ai_analysis_queue_publish_failed"

	// Playbook action failure reasons
	MetricReasonActionExecutionError  = "action_execution_error"
	MetricReasonEvidenceMarshalFailed = "evidence_marshal_failed"
)

var (
	meter                          metric.Meter
	metricsApiRequestsFailedTotal  metric.Int64Counter
	metricsApiRequestsTotal        metric.Int64Counter
	metricsEventProcessingDuration metric.Float64Histogram
	metricsEventProcessingTotal    metric.Int64Counter
	metricsEventProcessingFailed   metric.Int64Counter
	metricsPlaybookActionDuration  metric.Float64Histogram
	metricsPlaybookActionTotal     metric.Int64Counter
	metricsPlaybookActionFailed    metric.Int64Counter
	metricsSubjectResolutionTotal  metric.Int64Counter
	metricsKGEndpointCollision     metric.Int64Counter
	metricsKGRoute53Unmatched      metric.Int64Counter
)

// Histogram bucket boundaries for event and playbook processing.
var eventProcessingBuckets = metric.WithExplicitBucketBoundaries(
	0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600,
)

// Helper functions for fail-fast metric initialization
func mustCreateInt64Counter(meter metric.Meter, name, description string) metric.Int64Counter {
	counter, err := meter.Int64Counter(
		name,
		metric.WithDescription(description),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create %s metric: %v", name, err))
	}
	return counter
}

func mustCreateFloat64Histogram(meter metric.Meter, name, description, unit string, opts ...metric.Float64HistogramOption) metric.Float64Histogram {
	baseOpts := []metric.Float64HistogramOption{
		metric.WithDescription(description),
		metric.WithUnit(unit),
	}
	baseOpts = append(baseOpts, opts...)
	histogram, err := meter.Float64Histogram(
		name,
		baseOpts...,
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create %s metric: %v", name, err))
	}
	return histogram
}

func InitMetrics() {
	meter = otel.Meter(config.SERVICE_NAME)

	// Counter names omit the _total suffix; the OTel-to-Prometheus
	// exporter appends it automatically. Including it in the OTel name
	// risks a double suffix (nb_services_…_total_total) on receivers that
	// do not deduplicate.

	metricsApiRequestsFailedTotal = mustCreateInt64Counter(
		meter,
		"nb_services_api_requests_failed",
		"Total number of API requests that failed",
	)

	metricsApiRequestsTotal = mustCreateInt64Counter(
		meter,
		"nb_services_api_requests",
		"Total number of API requests processed",
	)

	// Histogram names omit the unit suffix; the OTel SDK propagates
	// the unit via metadata and the Prometheus exporter appends _seconds.
	metricsEventProcessingDuration = mustCreateFloat64Histogram(
		meter,
		"nb_services_event_processing_duration",
		"Duration of event processing",
		"s",
		eventProcessingBuckets,
	)

	metricsEventProcessingTotal = mustCreateInt64Counter(
		meter,
		"nb_services_event_processing",
		"Total number of events processed",
	)

	metricsEventProcessingFailed = mustCreateInt64Counter(
		meter,
		"nb_services_event_processing_failed",
		"Total number of event processing failures",
	)

	metricsPlaybookActionDuration = mustCreateFloat64Histogram(
		meter,
		"nb_services_playbook_action_duration",
		"Duration of individual playbook actions",
		"s",
		eventProcessingBuckets,
	)

	metricsPlaybookActionTotal = mustCreateInt64Counter(
		meter,
		"nb_services_playbook_action",
		"Total number of playbook actions executed",
	)

	metricsPlaybookActionFailed = mustCreateInt64Counter(
		meter,
		"nb_services_playbook_action_failed",
		"Total number of playbook action failures",
	)

	metricsSubjectResolutionTotal = mustCreateInt64Counter(
		meter,
		"nb_services_subject_resolution",
		"Total number of webhook subject resolution attempts via LLM",
	)
}

// MetricsSubjectResolution records a subject resolution attempt.
// source: datadog/pagerduty/zenduty (or webhook source name)
// stage: live/sync (live webhook vs one-time sync)
// result: matched/not_found/error
func MetricsSubjectResolution(ctx context.Context, source, stage, result, tenantId string) {
	if metricsSubjectResolutionTotal == nil {
		return
	}
	metricsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	metricsSubjectResolutionTotal.Add(metricsCtx, 1, metric.WithAttributes(
		attribute.String(MetricKeyEventSource, source),
		attribute.String("stage", stage),
		attribute.String("result", result),
		attribute.String(MetricKeyTenantID, tenantId),
	))
	metricsKGEndpointCollision = mustCreateInt64Counter(
		meter,
		"nb_services_kg_endpoint_collision",
		"Knowledge graph cloud-resource endpoint collisions during in-memory index build (first-write-wins)",
	)

	metricsKGRoute53Unmatched = mustCreateInt64Counter(
		meter,
		"nb_services_kg_route53_unmatched",
		"Knowledge graph external services where Route53 resolved an AWS endpoint but no matching cloud-resource node existed in the graph",
	)
}

// DBStats holds connection pool statistics for a single database.
type DBStats struct {
	Open      int
	InUse     int
	Idle      int
	MaxOpen   int
	WaitCount int64
	Status    int64 // 1 = ok, 0 = down/uninitialized
}

// DependencyStats is the snapshot returned by a provider callback.
type DependencyStats struct {
	Postgres      DBStats
	Clickhouse    DBStats
	MqConsumers   int
	MqPublishers  int
	MqStatus      int64 // 1 = ok, 0 = down
	CacheProvider string
	CacheStatus   int64 // 1 = ok, 0 = down
}

// DependencyStatsFunc returns current dependency health stats.
type DependencyStatsFunc func(ctx context.Context) DependencyStats

// RegisterDependencyMetrics creates observable gauges for dependency health
// and registers a batch callback that invokes provider on each collection cycle.
// Must be called after InitMetrics().
func RegisterDependencyMetrics(provider DependencyStatsFunc) {
	dbConnsOpen, err := meter.Int64ObservableGauge("nb_services_db_connections_open",
		metric.WithDescription("Current open database connections"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_connections_open metric: %v", err))
	}

	dbConnsInUse, err := meter.Int64ObservableGauge("nb_services_db_connections_in_use",
		metric.WithDescription("Database connections currently in use"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_connections_in_use metric: %v", err))
	}

	dbConnsIdle, err := meter.Int64ObservableGauge("nb_services_db_connections_idle",
		metric.WithDescription("Idle database connections"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_connections_idle metric: %v", err))
	}

	dbConnsMaxOpen, err := meter.Int64ObservableGauge("nb_services_db_connections_max_open",
		metric.WithDescription("Max open database connections configured"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_connections_max_open metric: %v", err))
	}

	// WaitCount is monotonically increasing since pool creation.
	// Use rate() in Prometheus to derive wait rate from this gauge snapshot.
	dbConnsWaitCount, err := meter.Int64ObservableGauge("nb_services_db_connections_wait_count",
		metric.WithDescription("Cumulative number of connections waited for since pool creation"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_connections_wait_count metric: %v", err))
	}

	dbStatus, err := meter.Int64ObservableGauge("nb_services_db_status",
		metric.WithDescription("Database status (1=ok, 0=down)"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_db_status metric: %v", err))
	}

	mqConsumers, err := meter.Int64ObservableGauge("nb_services_mq_consumers",
		metric.WithDescription("Active RabbitMQ consumers"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_mq_consumers metric: %v", err))
	}

	mqPublishers, err := meter.Int64ObservableGauge("nb_services_mq_publishers",
		metric.WithDescription("Active RabbitMQ publishers"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_mq_publishers metric: %v", err))
	}

	mqStatus, err := meter.Int64ObservableGauge("nb_services_mq_status",
		metric.WithDescription("RabbitMQ status (1=ok, 0=down)"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_mq_status metric: %v", err))
	}

	cacheStatus, err := meter.Int64ObservableGauge("nb_services_cache_status",
		metric.WithDescription("Cache status (1=ok, 0=down)"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create nb_services_cache_status metric: %v", err))
	}

	_, err = meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			s := provider(ctx)

			pgAttr := metric.WithAttributes(attribute.String(MetricKeyDatabase, "postgres"))
			o.ObserveInt64(dbConnsOpen, int64(s.Postgres.Open), pgAttr)
			o.ObserveInt64(dbConnsInUse, int64(s.Postgres.InUse), pgAttr)
			o.ObserveInt64(dbConnsIdle, int64(s.Postgres.Idle), pgAttr)
			o.ObserveInt64(dbConnsMaxOpen, int64(s.Postgres.MaxOpen), pgAttr)
			o.ObserveInt64(dbConnsWaitCount, s.Postgres.WaitCount, pgAttr)
			o.ObserveInt64(dbStatus, s.Postgres.Status, pgAttr)

			chAttr := metric.WithAttributes(attribute.String(MetricKeyDatabase, "clickhouse"))
			o.ObserveInt64(dbConnsOpen, int64(s.Clickhouse.Open), chAttr)
			o.ObserveInt64(dbConnsInUse, int64(s.Clickhouse.InUse), chAttr)
			o.ObserveInt64(dbConnsIdle, int64(s.Clickhouse.Idle), chAttr)
			o.ObserveInt64(dbConnsMaxOpen, int64(s.Clickhouse.MaxOpen), chAttr)
			o.ObserveInt64(dbConnsWaitCount, s.Clickhouse.WaitCount, chAttr)
			o.ObserveInt64(dbStatus, s.Clickhouse.Status, chAttr)

			o.ObserveInt64(mqConsumers, int64(s.MqConsumers))
			o.ObserveInt64(mqPublishers, int64(s.MqPublishers))
			o.ObserveInt64(mqStatus, s.MqStatus)

			providerAttr := metric.WithAttributes(attribute.String(MetricKeyProvider, s.CacheProvider))
			o.ObserveInt64(cacheStatus, s.CacheStatus, providerAttr)

			return nil
		},
		dbConnsOpen, dbConnsInUse, dbConnsIdle, dbConnsMaxOpen, dbConnsWaitCount, dbStatus,
		mqConsumers, mqPublishers, mqStatus,
		cacheStatus,
	)
	if err != nil {
		slog.Warn("failed to register dependency metrics callback", "error", err)
	}
}

func MetricsApiRequestsFailedTotal(ctx context.Context, apiModule string, reason string) {
	if metricsApiRequestsFailedTotal == nil {
		return
	}
	metricsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	metricsApiRequestsFailedTotal.Add(metricsCtx, 1, metric.WithAttributes(attribute.String("reason", reason), attribute.String("module", apiModule)))
}

func MetricsApiRequestsTotal(ctx context.Context, apiModule string) {
	if metricsApiRequestsTotal == nil {
		return
	}
	metricsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	metricsApiRequestsTotal.Add(metricsCtx, 1, metric.WithAttributes(attribute.String("module", apiModule)))
}

func MetricsEventProcessingDuration(ctx context.Context, duration float64, eventSource, tenantId, accountId, aggregationKey string) {
	if metricsEventProcessingDuration == nil {
		return
	}
	metricsEventProcessingDuration.Record(ctx, duration, metric.WithAttributes(
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
		attribute.String(MetricKeyAggregationKey, aggregationKey),
	))
}

func MetricsEventProcessingTotal(ctx context.Context, eventSource, tenantId, accountId, status string) {
	if metricsEventProcessingTotal == nil {
		return
	}
	metricsEventProcessingTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
		attribute.String(MetricKeyStatus, status),
	))
}

func MetricsEventProcessingFailed(ctx context.Context, eventSource, tenantId, accountId, reason string) {
	if metricsEventProcessingFailed == nil {
		return
	}
	metricsEventProcessingFailed.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
		attribute.String(MetricKeyReason, reason),
	))
}

func MetricsPlaybookActionDuration(ctx context.Context, duration float64, actionName, eventSource, tenantId, accountId string) {
	if metricsPlaybookActionDuration == nil {
		return
	}
	metricsPlaybookActionDuration.Record(ctx, duration, metric.WithAttributes(
		attribute.String(MetricKeyActionName, actionName),
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
	))
}

func MetricsPlaybookActionTotal(ctx context.Context, actionName, eventSource, tenantId, accountId, status string) {
	if metricsPlaybookActionTotal == nil {
		return
	}
	metricsPlaybookActionTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyActionName, actionName),
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
		attribute.String(MetricKeyStatus, status),
	))
}

func MetricsPlaybookActionFailed(ctx context.Context, actionName, eventSource, tenantId, accountId, reason string) {
	if metricsPlaybookActionFailed == nil {
		return
	}
	metricsPlaybookActionFailed.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyActionName, actionName),
		attribute.String(MetricKeyEventSource, eventSource),
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
		attribute.String(MetricKeyReason, reason),
	))
}

func MetricsKGEndpointCollision(ctx context.Context, tenantId, nodeType string) {
	if metricsKGEndpointCollision == nil {
		return
	}
	metricsKGEndpointCollision.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyNodeType, nodeType),
	))
}

func MetricsKGRoute53Unmatched(ctx context.Context, tenantId, accountId string) {
	if metricsKGRoute53Unmatched == nil {
		return
	}
	metricsKGRoute53Unmatched.Add(ctx, 1, metric.WithAttributes(
		attribute.String(MetricKeyTenantID, tenantId),
		attribute.String(MetricKeyAccountID, accountId),
	))
}
