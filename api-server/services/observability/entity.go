package observability

import (
	"encoding/json"
	"nudgebee/services/query"
	"strconv"
)

type SortField struct {
	ColumnName string `json:"column_name"`
	Order      string `json:"order"`
}

type LogsQueryBuilderRequest struct {
	Where query.QueryWhereClause `json:"where,omitempty" mapstructure:"where,omitempty"`
}

type FetchLogRequest struct {
	AccountId         string                  `json:"account_id"`
	LogProvider       string                  `json:"log_provider"`
	LogProviderSource string                  `json:"log_provider_source"`
	Query             string                  `json:"query"`
	StartTime         int64                   `json:"start_time"`
	EndTime           int64                   `json:"end_time"`
	Limit             int                     `json:"limit"`
	Offset            int                     `json:"offset"`
	SortFields        []SortField             `json:"sort_fields"`
	StepInterval      int                     `json:"step_interval"`
	Request           map[string]any          `json:"request"`
	QueryRequest      LogsQueryBuilderRequest `json:"query_request"`
}

type OutputLog struct {
	Timestamp string         `json:"timestamp"`
	Message   string         `json:"message"`
	Labels    map[string]any `json:"labels"`
	Severity  string         `json:"severity"`
}

type SearchResponse struct {
	Hits struct {
		Hits []struct {
			Source map[string]any `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type FetchLogLabelRequest struct {
	AccountId         string         `json:"account_id"`
	LogProvider       string         `json:"log_provider"`
	LogProviderSource string         `json:"log_provider_source"`
	Request           map[string]any `json:"request"`
	StartTime         int64          `json:"start_time"`
	EndTime           int64          `json:"end_time"`
	FetchIndex        bool           `json:"fetch_index"`
}

type OutputLogLabel struct {
	Label      string         `json:"label"`
	Attributes map[string]any `json:"attributes"`
}

type FieldsResponse struct {
	Indices []string       `json:"indices"`
	Fields  map[string]any `json:"fields"`
}

type OutputLogLabelFields struct {
	Field      string         `json:"field"`
	Attributes map[string]any `json:"attributes"`
}

type FetchLogLabelValuesRequest struct {
	LabelName         string            `json:"label_name"`
	AccountId         string            `json:"account_id"`
	LogProvider       string            `json:"log_provider"`
	LogProviderSource string            `json:"log_provider_source"`
	Request           map[string]any    `json:"request"`
	StartTime         int64             `json:"start_time"`
	EndTime           int64             `json:"end_time"`
	CurrentFilters    map[string]string `json:"current_filters"`
}

type OutputLogLabelValue struct {
	Value      string         `json:"value"`
	Attributes map[string]any `json:"attributes"`
}

// LabelMatcher carries a single label filter with its operator from the BUILDER UI.
// Operator is a wire token (_eq | _neq | _regex). _in / _not_in are advertised by
// GetSupportedOperators but rejected by injectPromQLMatchers until a list-value
// editor and end-to-end value-shape contract land — see plan in repo.
type LabelMatcher struct {
	Label    string `json:"label"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// QueryItem is the per-key shape used by the metrics_get_query action: each
// item carries the metric name, its own label matchers (chips), and an
// optional aggregation operator. The service-level GetMetricsQuery
// orchestrator iterates QueryItems and produces a map of rendered PromQL
// keyed the same way.
type QueryItem struct {
	Metric            string         `json:"metric"`
	LabelMatchers     []LabelMatcher `json:"label_matchers,omitempty"`
	AggregateOperator string         `json:"aggregate_operator,omitempty"` // sum|avg|min|max|count|stddev|stdvar|group; empty = no aggregation
}

type FetchMetricsRequest struct {
	AccountId            string               `json:"account_id"`
	MetricProvider       string               `json:"metric_provider"`
	MetricProviderSource string               `json:"metric_provider_source"`
	Queries              map[string]string    `json:"queries"`
	StartTime            int64                `json:"start_time"`
	EndTime              int64                `json:"end_time"`
	StepInterval         int                  `json:"step_interval"`
	Instant              bool                 `json:"instant"`
	Request              map[string]any       `json:"request"`
	Labels               map[string]string    `json:"labels"`         // eq-only; used by internal callers
	LabelMatchers        []LabelMatcher       `json:"label_matchers"` // synthesized per-item by GetMetricsQuery; not sent from UI
	QueryItems           map[string]QueryItem `json:"query_items"`    // BUILDER per-key shape: {key: {metric, label_matchers}}
}

type FetchMetricLabelsRequest struct {
	AccountId            string         `json:"account_id"`
	MetricProvider       string         `json:"metric_provider"`
	MetricProviderSource string         `json:"metric_provider_source"`
	MetricName           string         `json:"metric"`
	StartTime            int64          `json:"start_time"`
	EndTime              int64          `json:"end_time"`
	Request              map[string]any `json:"request"`
}

type FetchMetricsListRequest struct {
	AccountId            string         `json:"account_id"`
	MetricProvider       string         `json:"metric_provider"`
	MetricProviderSource string         `json:"metric_provider_source"`
	Metric               string         `json:"metric"`
	StartTime            int64          `json:"start_time"`
	EndTime              int64          `json:"end_time"`
	Request              map[string]any `json:"request"`
}

type FetchMetricsLabelValueRequest struct {
	AccountId            string         `json:"account_id"`
	MetricProvider       string         `json:"metric_provider"`
	MetricProviderSource string         `json:"metric_provider_source"`
	Label                string         `json:"label"`
	StartTime            int64          `json:"start_time"`
	EndTime              int64          `json:"end_time"`
	Request              map[string]any `json:"request"`
}

type GetUtilisationTrendRequest struct {
	AccountId            string         `json:"account_id"`
	MetricProvider       string         `json:"metric_provider"`
	MetricProviderSource string         `json:"metric_provider_source"`
	StartTime            int64          `json:"start_time"`
	EndTime              int64          `json:"end_time"`
	Request              map[string]any `json:"request"`
}

// OutputMetricQuery represents the response structure for metric and log group queries.
// Used by:
// - FetchLogGroup: Returns aggregated log patterns grouped by message, namespace, workload, container
// - FetchMetricsQuery: Returns time-series metric data
//
// Both Prometheus and NewRelic providers return this structure.
// The GraphQL schema declares this as the return type for log_group queries.
type OutputMetricQuery struct {
	Results []QueryResult `json:"results"`
}

type OutputLogQuery struct {
	Query string `json:"query"`
}

// FetchMetricQueryOutput is keyed by query_item key → rendered PromQL string.
type FetchMetricQueryOutput struct {
	Results map[string]string `json:"results"`
}

// QueryResult represents a single query result with its metadata and data points.
type QueryResult struct {
	QueryKey string   `json:"query_key"`       // Identifies the query (e.g., "log_group")
	Payload  []Result `json:"payload"`         // Array of metric/log data points
	Query    string   `json:"query"`           // The actual query executed (PromQL or NRQL)
	Error    *string  `json:"error,omitempty"` // Error message if query failed
}

// Result represents a single metric or log group data point with labels, timestamps, and values.
// For log groups:
//   - Metric contains: sample (message), namespace, workload, container, pattern_hash
//   - Timestamps contains aggregation time points
//   - Values contains log counts at each timestamp
type Result struct {
	Metric     map[string]string `json:"metric"`     // Label key-value pairs
	Timestamps []int64           `json:"timestamps"` // Unix epoch milliseconds
	Values     []float64         `json:"values"`     // Metric/count values
}

type DefaultProvider struct {
	AccountId      string `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string `json:"provider_type" mapstructure:"provider_type" validate:"required"`
	ProviderSource string `json:"provider_source"`
}

// ProviderCapabilities describes the features supported by a resolved provider.
type ProviderCapabilities struct {
	// Statically declared in allProviderCaps (service.go)
	SupportsServiceMap             bool `json:"supports_service_map"`
	SupportsTraceGrouping          bool `json:"supports_trace_grouping"`
	SupportsHeatmap                bool `json:"supports_heatmap"`
	SupportsCrossZoneCommunication bool `json:"supports_cross_zone_communication"` // traces only; false by default for all providers
	SupportsRawQuery               bool `json:"supports_raw_query"`
	SupportsLogGroups              bool `json:"supports_log_groups"`
	// Interface-derived at runtime (optional interface — not all providers implement it)
	SupportsAutoQuery bool `json:"supports_auto_query"`
	// Runtime-detected from source. SupportedOperatorDescriptors carries the
	// backend-authoritative display metadata (chip/line labels, kinds); the UI
	// migrates from SupportedOperators to SupportedOperatorDescriptors and the
	// raw-token field is removed in a later PR.
	SupportedOperators           []string                   `json:"supported_operators"`
	SupportedOperatorDescriptors []query.OperatorDescriptor `json:"supported_operator_descriptors"`
}

// DefaultProviderResponse is the response for the get_default_provider action.
type DefaultProviderResponse struct {
	Provider          string               `json:"provider"`
	IntegrationSource string               `json:"integration_source"`
	DefaultIndex      string               `json:"default_index"`
	Capabilities      ProviderCapabilities `json:"capabilities"`
}

// ListProviderCapabilitiesRequest is the request for the observability_list_provider_capabilities action.
type ListProviderCapabilitiesRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

// ProviderCapabilityEntry is one flat entry in the list_provider_capabilities response.
type ProviderCapabilityEntry struct {
	Provider     string               `json:"provider"`
	ProviderType string               `json:"provider_type"`
	Capabilities ProviderCapabilities `json:"capabilities"`
}

type TracesQueryBuilderRequest struct {
	Where   query.QueryWhereClause `json:"where,omitempty" mapstructure:"where,omitempty"`
	Limit   int                    `json:"limit,omitempty" mapstructure:"limit,omitempty"`
	Offset  int                    `json:"offset,omitempty" mapstructure:"offset,omitempty"`
	OrderBy []query.QueryOrderBy   `json:"order_by,omitempty" mapstructure:"order_by,omitempty"`
}

type TracesV3Request struct {
	AccountId      string                    `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string                    `json:"provider_type" mapstructure:"provider_type"`
	ProviderSource string                    `json:"provider_source"`
	Query          string                    `json:"query" mapstructure:"query"`
	StartTime      int64                     `json:"start_time" mapstructure:"start_time"`
	EndTime        int64                     `json:"end_time" mapstructure:"end_time"`
	Request        map[string]any            `json:"request"`
	QueryRequest   TracesQueryBuilderRequest `json:"query_request" mapstructure:"query_request"`
}

type TracesHeatMapRequest struct {
	AccountId      string `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string `json:"provider_type" mapstructure:"provider_type"`
	ProviderSource string `json:"provider_source"`
	TraceId        string `json:"trace_id" validate:"required"`
	StartTime      int64  `json:"start_time" mapstructure:"start_time"`
	EndTime        int64  `json:"end_time" mapstructure:"end_time"`
}

type TracesV3LabelValuesRequest struct {
	AccountId      string                    `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string                    `json:"provider_type" mapstructure:"provider_type"`
	ProviderSource string                    `json:"provider_source"`
	Label          string                    `json:"label" mapstructure:"label" validate:"required"`
	StartTime      int64                     `json:"start_time" mapstructure:"start_time"`
	EndTime        int64                     `json:"end_time" mapstructure:"end_time"`
	QueryRequest   TracesQueryBuilderRequest `json:"query_request" mapstructure:"query_request"`
}

// OpenTelemetry trace structures
type OpenTelemetryTrace struct {
	Timestamp          string                 `json:"Timestamp"`
	TraceID            string                 `json:"TraceId"`
	SpanID             string                 `json:"SpanId"`
	ParentSpanID       string                 `json:"ParentSpanId"`
	TraceState         string                 `json:"TraceState"`
	SpanName           string                 `json:"SpanName"`
	SpanKind           string                 `json:"SpanKind"`
	ServiceName        string                 `json:"ServiceName"`
	ResourceAttributes OTelResourceAttributes `json:"ResourceAttributes"`
	SpanAttributes     map[string]string      `json:"SpanAttributes"`
	Duration           int64                  `json:"Duration"`
	StatusCode         string                 `json:"StatusCode"`
	StatusMessage      string                 `json:"StatusMessage"`
	EventsTimestamp    []string               `json:"Events.Timestamp"`
	EventsName         []string               `json:"Events.Name"`
	EventsAttributes   []map[string]string    `json:"Events.Attributes"`
	LinksTraceID       []string               `json:"Links.TraceId"`
	LinksSpanID        []string               `json:"Links.SpanId"`
	LinksTraceState    []string               `json:"Links.TraceState"`
	LinksAttributes    []map[string]string    `json:"Links.Attributes"`
}

// OTelResourceAttributes is now a type alias for map[string]string
// This allows preserving ALL OTEL resource attributes without data loss
type OTelResourceAttributes = map[string]string

type IntOrString int

func (i *IntOrString) UnmarshalJSON(b []byte) error {
	// Try to unmarshal as number first
	var intVal int
	if err := json.Unmarshal(b, &intVal); err == nil {
		*i = IntOrString(intVal)
		return nil
	}

	// Try as string
	var strVal string
	if err := json.Unmarshal(b, &strVal); err != nil {
		return err
	}
	intVal, err := strconv.Atoi(strVal)
	if err != nil {
		return err
	}

	*i = IntOrString(intVal)
	return nil
}

// Datadog trace structures
type DatadogTrace struct {
	Data []DatadogSpan `json:"data"`
}

type DatadogSpan struct {
	Attributes DatadogAttributes `json:"attributes"`
	ID         string            `json:"id"`
	Type       string            `json:"type"`
}

type DatadogAttributes struct {
	Custom         DatadogCustom `json:"custom"`
	EndTimestamp   string        `json:"end_timestamp"`
	Env            string        `json:"env"`
	Error          any           `json:"error"`
	Host           string        `json:"host"`
	OperationName  string        `json:"operation_name"`
	ParentID       string        `json:"parent_id"`
	ResourceHash   string        `json:"resource_hash"`
	ResourceName   string        `json:"resource_name"`
	Service        string        `json:"service"`
	SpanID         string        `json:"span_id"`
	StartTimestamp string        `json:"start_timestamp"`
	Status         string        `json:"status"`
	Tags           []string      `json:"tags"`
	TraceID        string        `json:"trace_id"`
	Type           string        `json:"type"`
}

type DatadogCustom struct {
	Component string          `json:"component"`
	DB        DatadogDBInfo   `json:"db,omitempty"`
	Duration  int64           `json:"duration"`
	HTTP      *DatadogHTTP    `json:"http,omitempty"`
	GRPC      *DatadogGRPC    `json:"grpc,omitempty"`
	RPC       *DatadogRPC     `json:"rpc,omitempty"`
	Language  string          `json:"language"`
	Version   string          `json:"version"`
	Span      DatadogSpanInfo `json:"span"`
	ProcessID string          `json:"process_id"`
	RuntimeID string          `json:"runtime-id"`
	Service   string          `json:"service"`
	Network   *DatadogNetwork `json:"network,omitempty"`
	Peer      *DatadogPeer    `json:"peer,omitempty"`
	Flask     *DatadogFlask   `json:"flask,omitempty"`
}

type DatadogHTTP struct {
	Host       string              `json:"host"`
	Method     string              `json:"method"`
	PathGroup  string              `json:"path_group"`
	StatusCode string              `json:"status_code"`
	URL        string              `json:"url"`
	URLDetails *DatadogURLDetails  `json:"url_details,omitempty"`
	UserAgent  string              `json:"useragent,omitempty"`
	Request    *DatadogHTTPRequest `json:"request,omitempty"`
	Route      string              `json:"route,omitempty"`
}

type DatadogURLDetails struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Port   string `json:"port,omitempty"`
	Scheme string `json:"scheme"`
}

type DatadogHTTPRequest struct {
	Headers map[string]string `json:"headers"`
}

type DatadogGRPC struct {
	Method map[string]string `json:"method"`
}

type DatadogRPC struct {
	GRPC    *DatadogRPCGRPC `json:"grpc,omitempty"`
	Method  string          `json:"method"`
	Service string          `json:"service"`
}

type DatadogRPCGRPC struct {
	Kind       string      `json:"kind"`
	Package    string      `json:"package"`
	Path       string      `json:"path"`
	StatusCode IntOrString `json:"status_code"`
}

type DatadogSpanInfo struct {
	Kind string `json:"kind"`
}

type DatadogDBInfo struct {
	Application string `json:"application"`
	Instance    string `json:"instance"`
	Statement   string `json:"statement"`
	System      string `json:"system"`
	User        string `json:"user"`
}

type TraceGroupingValues struct {
	Count                        int     `json:"count"`
	ErrorCount                   int     `json:"error_count"`
	P99Latency                   int64   `json:"p99_latency"`
	P95Latency                   int64   `json:"p95_latency"`
	MaxLatency                   int64   `json:"max_latency"`
	WorkloadName                 string  `json:"workload_name"`
	WorkloadNamespace            string  `json:"workload_namespace"`
	DestinationWorkloadName      string  `json:"destination_workload_name"`
	DestinationWorkloadNamespace string  `json:"destination_workload_namespace"`
	DestinationWorkloadZone      *string `json:"destination_workload_zone"` // nullable
	Resource                     string  `json:"resource"`
	DurationNS                   int64   `json:"duration_ns"`
	HTTPStatusCode               string  `json:"http_status_code"`
	SpanName                     string  `json:"span_name"`
}

type DatadogNetwork struct {
	Destination *DatadogDestination `json:"destination,omitempty"`
}

type DatadogDestination struct {
	IP   string      `json:"ip"`
	Port IntOrString `json:"port,omitempty"`
}

type DatadogPeer struct {
	Hostname string      `json:"hostname"`
	RPC      *DatadogRPC `json:"rpc,omitempty"`
}

type DatadogFlask struct {
	Endpoint string `json:"endpoint,omitempty"`
	URLRule  string `json:"url_rule,omitempty"`
	Version  string `json:"version,omitempty"`
}

type OutputMetrics struct {
	Metric     string         `json:"metric"`
	Attributes map[string]any `json:"attributes"`
}

type OutputMetricsLabelValues struct {
	Value      string         `json:"value"`
	Attributes map[string]any `json:"attributes"`
}

type OutputMetricLabels struct {
	Label      string         `json:"label"`
	Attributes map[string]any `json:"attributes"`
}

type UserHistoryRequest struct {
	Data      string `json:"data"`
	AccountId string `json:"account_id"`
	Module    string `json:"module"`
	Duration  int64  `json:"duration"`
	Status    string `json:"status"`
}

type FetchLogGroupRequest struct {
	AccountId         string         `json:"account_id"`
	LogProvider       string         `json:"log_provider"`
	LogProviderSource string         `json:"log_provider_source"`
	Request           map[string]any `json:"request"`
	StartTime         int64          `json:"start_time"`
	EndTime           int64          `json:"end_time"`
}

// LogGroupOutput is the dedicated response type for the log_group API.
type LogGroupOutput struct {
	Groups []LogGroup `json:"groups"`
}

// LogGroup represents a single aggregated log group (error pattern).
type LogGroup struct {
	Sample      string    `json:"sample"`       // Representative log message
	Namespace   string    `json:"namespace"`    // Kubernetes namespace
	Workload    string    `json:"workload"`     // Workload (deployment/statefulset) name
	Container   string    `json:"container"`    // Container name
	ContainerID string    `json:"container_id"` // Full container path (e.g. /k8s/ns/pod/container)
	PatternHash string    `json:"pattern_hash"` // Unique hash for pattern grouping & ticket linking
	Level       string    `json:"level"`        // Severity level (error, critical, fatal)
	Count       int64     `json:"count"`        // Total occurrences in the time window
	Timestamps  []int64   `json:"timestamps"`   // Time points for the aggregation
	Values      []float64 `json:"values"`       // Counts at each time point
}
