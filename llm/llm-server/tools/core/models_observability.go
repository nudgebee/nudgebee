package core

import (
	"strings"
	"time"
)

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
	// Suggestion carries services-server's ValidateRequest empty-result diagnosis
	// (unknown label name / unknown label value) when a canonical query matched nothing.
	// Empty when the diagnosis found nothing actionable (a genuinely empty result).
	Suggestion string `json:"suggestion,omitempty"`
	// Coverage is set when the read stopped at its line limit. Nil means only
	// that the limit asked for was not reached — a backend that lowers the
	// limit on its own gives no sign of it here.
	Coverage *ObservabilityLogCoverage `json:"coverage,omitempty"`
}

// ObservabilityLogCoverage says a log read returned as many lines as its
// limit, and what those lines cover. The backend returns the lines it reaches
// first — the newest, on most — so on a busy workload a read of an incident
// window stops minutes short of the window's start, and nothing else in the
// response shows it: the lines look like the whole window.
type ObservabilityLogCoverage struct {
	Limit    int `json:"limit"`
	Returned int `json:"returned"`
	// Oldest and Newest bound the returned lines, RFC3339 UTC. Empty unless
	// every line carried a timestamp that could be read: a span drawn from
	// some of the lines would understate what was returned.
	Oldest string `json:"oldest,omitempty"`
	Newest string `json:"newest,omitempty"`
	// RequestedStart and RequestedEnd are the window that was asked for.
	RequestedStart string `json:"requested_start,omitempty"`
	RequestedEnd   string `json:"requested_end,omitempty"`
}

// logTimestampLayouts are the timestamp forms log backends return.
var logTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
}

// NewObservabilityLogCoverage returns the coverage of a read that reached its
// limit, or nil when it did not. startMs and endMs are the requested window in
// epoch milliseconds; zero leaves that bound out.
func NewObservabilityLogCoverage(logs []ObservabilityLog, limit int, startMs, endMs int64) *ObservabilityLogCoverage {
	if limit <= 0 || len(logs) < limit {
		return nil
	}
	coverage := &ObservabilityLogCoverage{Limit: limit, Returned: len(logs)}
	// The lines are not sorted: several backends return them grouped by stream.
	var oldest, newest time.Time
	spanKnown := true
	for _, entry := range logs {
		at, ok := parseLogTimestamp(entry.Timestamp)
		if !ok {
			spanKnown = false
			break
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
		if newest.IsZero() || at.After(newest) {
			newest = at
		}
	}
	if spanKnown && !oldest.IsZero() {
		coverage.Oldest = oldest.UTC().Format(time.RFC3339)
		coverage.Newest = newest.UTC().Format(time.RFC3339)
	}
	if startMs > 0 {
		coverage.RequestedStart = time.UnixMilli(startMs).UTC().Format(time.RFC3339)
	}
	if endMs > 0 {
		coverage.RequestedEnd = time.UnixMilli(endMs).UTC().Format(time.RFC3339)
	}
	return coverage
}

func parseLogTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range logTimestampLayouts {
		if at, err := time.Parse(layout, value); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

type ObservabilityTraceResponse struct {
	Traces   []ObservabilityTrace       `json:"traces"`
	Metadata ObservabilityTraceMetadata `json:"metadata"`
	// Result is populated only on the free-form ClickHouse agent path (IncludeRawResult). When
	// non-nil it carries the raw query result with column order + types preserved, so aggregation /
	// custom-projection queries render their real values instead of zeroed span structs. Nil means
	// the services-server returned the legacy typed array (Traces).
	Result *ObservabilityTraceRawTable `json:"result,omitempty"`
	// Suggestion carries services-server's ValidateRequest empty-result diagnosis (unknown field
	// name / unknown field value) when it fires. It arrives with an EMPTY Traces and a 200, so an
	// empty trace result can say WHY it is empty instead of leaving the agent to guess and retry.
	Suggestion string `json:"suggestion,omitempty"`
}

// ObservabilityTraceRawTable mirrors observability.RawTraceResult: an arbitrary ClickHouse result
// set with column order and types preserved.
type ObservabilityTraceRawTable struct {
	Columns     []string `json:"columns"`
	ColumnTypes []string `json:"column_types"`
	Rows        [][]any  `json:"rows"`
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

// ObservabilityMetricSeriesMatch is one (namespace-label, workload-label) grouping the
// metrics_list_series action found for a workload, with the metric families under it.
// MatchType is "exact" or "prefix" (pod names carry a replica-hash suffix). Mirrors the
// api-server observability.MetricSeriesMatch wire shape.
type ObservabilityMetricSeriesMatch struct {
	NamespaceLabel string   `json:"namespace_label"`
	WorkloadLabel  string   `json:"workload_label"`
	MatchType      string   `json:"match_type"`
	Families       []string `json:"families"`
}

type ObservabilityMetricSeriesMatchResponse struct {
	Matches   []ObservabilityMetricSeriesMatch `json:"matches"`
	Truncated bool                             `json:"truncated"`
}

type ObservabilityMetricsLabelValue struct {
	Value      string         `json:"value"`
	Attributes map[string]any `json:"attributes"`
}
type ObservabilityMetricsLabelValuesResponse struct {
	Values []ObservabilityMetricsLabelValue `json:"values"`
}

// ObservabilityMetricsQueryRequest mirrors api-server's FetchMetricsRequest for metrics_query.
type ObservabilityMetricsQueryRequest struct {
	AccountId      string            `json:"account_id"`
	MetricProvider string            `json:"metric_provider"`
	Queries        map[string]string `json:"queries"`
	StartTime      int64             `json:"start_time"`
	EndTime        int64             `json:"end_time"`
	// StepInterval is the range step in seconds; zero lets the provider pick.
	StepInterval int            `json:"step_interval,omitempty"`
	Request      map[string]any `json:"request,omitempty"`
}

// ObservabilityMetricsQueryResponse mirrors api-server's OutputMetricQuery.
type ObservabilityMetricsQueryResponse struct {
	Results []ObservabilityMetricsQueryResult `json:"results"`
}

type ObservabilityMetricsQueryResult struct {
	QueryKey string                            `json:"query_key"`
	Payload  []ObservabilityMetricsQuerySeries `json:"payload"`
	Query    string                            `json:"query"`
	Error    *string                           `json:"error,omitempty"`

	// DocsMatched is how many documents the query matched, independent of how many
	// series were extracted. An empty Payload with DocsMatched > 0 means the query was
	// correct and the projection was not — a different problem from "nothing matched",
	// and previously indistinguishable.
	DocsMatched *int64 `json:"docs_matched,omitempty"`
	// Note explains an empty Payload when the backend knows why.
	Note string `json:"note,omitempty"`
}

type ObservabilityMetricsQuerySeries struct {
	Metric     map[string]string `json:"metric"`
	Timestamps []int64           `json:"timestamps"`
	Values     []float64         `json:"values"`
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
	Attributes           map[string]any                  `json:"attributes,omitempty"`
	TagFilters           map[string]any                  `json:"tag_filters,omitempty"`
	Status               map[string]any                  `json:"status,omitempty"`
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
	AccountId      string            `json:"account_id" mapstructure:"account_id" validate:"required"`
	ProviderType   string            `json:"provider_type" mapstructure:"provider_type"`
	ProviderSource string            `json:"provider_source"`
	Query          string            `json:"query" mapstructure:"query"`
	StartTime      int64             `json:"start_time" mapstructure:"start_time"`
	EndTime        int64             `json:"end_time" mapstructure:"end_time"`
	Limit          int               `json:"limit" mapstructure:"limit"`
	Offset         int               `json:"offset" mapstructure:"offset"`
	SortFields     []SortField       `json:"sort_fields"`
	QueryRequest   TraceQueryBuilder `json:"query_request" mapstructure:"query_request"`
	// IncludeRawResult asks the services-server traces action to return the raw
	// {columns, column_types, rows} table (ObservabilityTraceRawTable) instead of the typed span
	// array. Set only by the free-form ClickHouse agent tool so aggregation / custom-projection
	// queries keep their real values. Mirrors observability.TracesV3Request.IncludeRawResult.
	IncludeRawResult bool `json:"include_raw_result" mapstructure:"include_raw_result"`
	// ValidateRequest opts the query into services-server's empty-result diagnosis: a query that
	// matches nothing comes back naming the mistyped field or value instead of a silent empty
	// list. Mirrors observability.TracesV3Request.ValidateRequest and the log path's
	// LogQueryRequest.ValidateRequest.
	ValidateRequest bool `json:"validate_request,omitempty" mapstructure:"validate_request"`
	// IncludeExecutedQuery asks services-server to resolve the provider query it actually ran and
	// return it on the envelope (query + provider), on the empty result as much as the successful
	// one. Without it an answer of "no traces found" cannot say what was asked or which backend
	// answered it. Mirrors observability.TracesV3Request.IncludeExecutedQuery.
	IncludeExecutedQuery bool `json:"include_executed_query,omitempty" mapstructure:"include_executed_query"`
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
