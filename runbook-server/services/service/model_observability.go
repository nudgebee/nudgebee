package service


type ObservabilityLog struct {
	Timestamp string         `json:"timestamp"`
	Message   string         `json:"message"`
	Labels    map[string]any `json:"labels"`
	Severity  string         `json:"severity"`
}

type ObservabilityLogMetadata struct {
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Limit     int    `json:"limit"`
	Query     string `json:"query"`
	Provider  string `json:"provider"`
}

type ObservabilityTraceMetadata struct {
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Limit     int    `json:"limit"`
	Query     string `json:"query"`
	Provider  string `json:"provider"`
}

type ObservabilityLogResponse struct {
	Logs     []ObservabilityLog       `json:"logs"`
	Metadata ObservabilityLogMetadata `json:"metadata"`
}

type ObservabilityTraceResponse struct {
	Traces   []ObservabilityTrace       `json:"traces"`
	Metadata ObservabilityTraceMetadata `json:"metadata"`
}

type ObservabilityLogLabel struct {
	Label      string         `json:"label"`
	Attributes map[string]any `json:"attributes"`
}

type ObservabilityLogLabelResponse struct {
	Labels []ObservabilityLogLabel `json:"labels"`
}

type ObservabilityMetricsSeries struct {
	Metric     string         `json:"metric"`
	Attributes map[string]any `json:"attributes"`
}

type ObservabilityMetricsSeriesResponse struct {
	Series []ObservabilityMetricsSeries `json:"series"`
}

type ObservabilityMetricsSeriesLabel struct {
	Label      string         `json:"label"`
	Attributes map[string]any `json:"attributes"`
}
type ObservabilityMetricsSeriesLabelsResponse struct {
	Labels []ObservabilityMetricsSeriesLabel `json:"labels"`
}

type ObservabilityMetricsLabelValue struct {
	Value      string         `json:"value"`
	Attributes map[string]any `json:"attributes"`
}
type ObservabilityMetricsLabelValuesResponse struct {
	Values []ObservabilityMetricsLabelValue `json:"values"`
}

type ObservabilityTrace struct {
	Timestamp            string                          `json:"timestamp,omitempty"`
	TraceID              string                          `json:"trace_id,omitempty"`
	SpanID               string                          `json:"span_id,omitempty"`
	ParentSpanID         string                          `json:"parent_span_id,omitempty"`
	TraceState           string                          `json:"trace_state,omitempty"`
	SpanName             string                          `json:"span_name,omitempty"`
	SpanKind             string                          `json:"span_kind,omitempty"`
	ServiceName          string                          `json:"service_name,omitempty"`
	ResourceAttributes   ObservabilityResourceAttributes `json:"resource_attributes,omitempty"`
	SpanAttributes       map[string]string               `json:"span_attributes,omitempty"`
	DurationNs           int64                           `json:"duration_ns,omitempty"`
	StatusCode           string                          `json:"status_code,omitempty"`
	StatusMessage        string                          `json:"status_message,omitempty"`
	EventsTimestamp      []string                        `json:"events_timestamp,omitempty"`
	EventsName           []string                        `json:"events_name,omitempty"`
	EventsAttributes     []map[string]string             `json:"events_attributes,omitempty"`
	LinksTraceID         []string                        `json:"links_trace_id,omitempty"`
	LinksSpanID          []string                        `json:"links_span_id,omitempty"`
	LinksTraceState      []string                        `json:"links_trace_state,omitempty"`
	LinksAttributes      []map[string]string             `json:"links_attributes,omitempty"`
	WorkloadName         string                          `json:"workload_name,omitempty"`
	WorkloadNamespace    string                          `json:"workload_namespace,omitempty"`
	Resource             string                          `json:"resource,omitempty"`
	DestinationName      string                          `json:"destination_name,omitempty"`
	DestinationWorkload  string                          `json:"destination_workload_name,omitempty"`
	DestinationNamespace string                          `json:"destination_workload_namespace,omitempty"`
	Headers              string                          `json:"headers,omitempty"`
	HTTPStatusCode       string                          `json:"http_status_code,omitempty"`
	RequestPayload       string                          `json:"request_payload,omitempty"`
	HTTPResponse         string                          `json:"http_response,omitempty"`
	QueryType            string                          `json:"query_type,omitempty"`
	TraceIDs             []string                        `json:"trace_ids,omitempty"`
	StartTime            string                          `json:"start_time,omitempty"`
	EndTime              string                          `json:"end_time,omitempty"`
	StartTimeUnixNano    string                          `json:"start_time_unix_nano,omitempty"`
	EndTimeUnixNano      string                          `json:"end_time_unix_nano,omitempty"`
	TraceSource          string                          `json:"trace_source,omitempty"`
	Service              string                          `json:"service,omitempty"`
	Operation            string                          `json:"operation,omitempty"`
	Attributes           map[string]interface{}          `json:"attributes,omitempty"`
	TagFilters           map[string]interface{}          `json:"tag_filters,omitempty"`
	Status               map[string]interface{}          `json:"status,omitempty"`
}

type ObservabilityResourceAttributes struct {
	HostID                string `json:"host_id,omitempty"`
	HostName              string `json:"host_name,omitempty"`
	ServiceName           string `json:"service_name,omitempty"`
	CloudAccountID        string `json:"cloud_account_id,omitempty"`
	CloudAvailabilityZone string `json:"cloud_availability_zone,omitempty"`
	CloudRegion           string `json:"cloud_region,omitempty"`
	ContainerID           string `json:"container_id,omitempty"`
}

type ObservabilityTracesV3Request struct {
	AccountId      string                   `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string                   `json:"provider_type" mapstructure:"provider_type"`
	ProviderSource string                   `json:"provider_source"`
	Query          string                   `json:"query" mapstructure:"query"`
	StartTime      int64                    `json:"start_time" mapstructure:"start_time"`
	EndTime        int64                    `json:"end_time" mapstructure:"end_time"`
	Limit          int                      `json:"limit" mapstructure:"limit"`
	Offset         int                      `json:"offset" mapstructure:"offset"`
	SortFields     []ObservabilitySortField `json:"sort_fields"`
	QueryRequest   TraceQueryBuilder        `json:"query_request" mapstructure:"query_request"`
}

type SortField struct {
	ColumnName string `json:"column_name"`
	Order      string `json:"order"`
}

type TraceQueryBuilder struct {
	Where   QueryWhereClause `json:"where,omitempty" mapstructure:"where,omitempty"`
	GroupBy []string         `json:"group_by,omitempty" mapstructure:"group_by,omitempty"`
	Having  QueryWhereClause `json:"having,omitempty" mapstructure:"having,omitempty"`
	Limit   int              `json:"limit,omitempty" mapstructure:"limit,omitempty"`
	Offset  int              `json:"offset,omitempty" mapstructure:"offset,omitempty"`
	OrderBy []QueryOrderBy   `json:"order_by,omitempty" mapstructure:"order_by,omitempty"`
}

type QueryColumn struct {
	Name string   `json:"name,omitempty" mapstructure:"name,omitempty"`
	Expr string   `json:"expr,omitempty" mapstructure:"expr,omitempty"`
	Args []string `json:"args,omitempty" mapstructure:"args,omitempty"`
}

type QueryOrderBy struct {
	Column string         `json:"column,omitempty" mapstructure:"column,omitempty"`
	Order  QuerySortOrder `json:"order,omitempty" mapstructure:"order,omitempty"`
}

type QuerySortOrder string

type ObservabilityMetricsQueryRequest struct {
	AccountId            string            `json:"account_id"`
	MetricProvider       string            `json:"metric_provider"`
	MetricProviderSource string            `json:"metric_provider_source"`
	Queries              map[string]string `json:"queries"`
	StartTime            int64             `json:"start_time"`
	EndTime              int64             `json:"end_time"`
	StepInterval         int               `json:"step_interval"`
	Instant              bool              `json:"instant"`
	Request              map[string]any    `json:"request"`
}

type ObservabilityMetricsQueryResponse struct {
	Results []ObservabilityMetricQueryResult `json:"results"`
}

type ObservabilityMetricQueryResult struct {
	QueryKey                        string                      `json:"query_key"`
	ObservabilityMetricQueryPayload []ObservabilityMetricResult `json:"payload"`
}

type ObservabilityMetricResult struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []int64           `json:"timestamps"`
	Values     []float64         `json:"values"`
}

// ObservabilityLogGroupQueryRequest mirrors api-server's FetchLogGroupRequest.
// Routing is by (LogProvider, LogProviderSource); provider-specific filters
// (region/log_group for AWS, index/query_type for ES, etc.) ride on Request.
type ObservabilityLogGroupQueryRequest struct {
	AccountId         string         `json:"account_id"`
	LogProvider       string         `json:"log_provider"`
	LogProviderSource string         `json:"log_provider_source"`
	Request           map[string]any `json:"request"`
	StartTime         int64          `json:"start_time"`
	EndTime           int64          `json:"end_time"`
}

// ObservabilityLogGroup mirrors api-server's LogGroup — one aggregated
// error/log pattern (sample + k8s metadata + pattern hash + time series).
type ObservabilityLogGroup struct {
	Sample      string    `json:"sample"`
	Namespace   string    `json:"namespace"`
	Workload    string    `json:"workload"`
	Container   string    `json:"container"`
	ContainerID string    `json:"container_id"`
	PatternHash string    `json:"pattern_hash"`
	Level       string    `json:"level"`
	Count       int64     `json:"count"`
	Timestamps  []int64   `json:"timestamps"`
	Values      []float64 `json:"values"`
}

type ObservabilityLogGroupQueryResponse struct {
	Groups []ObservabilityLogGroup `json:"groups"`
}
