package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"time"
)

var ClickhouseTraceTableDefinition = map[string]query.ColumnDefinition{
	"trace_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"span_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"parent_span_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"service_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"timestamp": {
		Type: "datetime",
	},
	"workload_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
	},
	"duration_ns": {
		Type: query.ColumnDefinitionTypeFloat,
	},
	"status_code": {
		Type: query.ColumnDefinitionTypeString,
		// Deliberately NO WhereDef: otel_traces stores span status in two vocabularies
		// (OTEL long form STATUS_CODE_ERROR/OK/UNSET and short Error/Ok/Unset), but a
		// column-only WhereDef normalization breaks existing short-form-VALUE callers —
		// notably the By-Spans trace UI, whose dropdown value is the raw stored status
		// (traces_label_values). This path is un-gated, and ClickHouse is carved out of
		// the v2 canonical agent, so normalizing here only ever helped a dev-only forced
		// path while regressing the live UI. If cross-vocabulary matching is wanted,
		// canonicalize the VALUE side (match {value, canonical(value)}), not the column.
	},
	"span_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"resource": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_workload_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"headers": {
		Type: query.ColumnDefinitionTypeString,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request query.QueryRequest) (string, query.QueryRequest, error) {
			return "base64Decode(headers)", request, nil
		},
	},
	"http_status_code": {
		Type: query.ColumnDefinitionTypeString,
	},
	"http_method": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "spanattributes['http.method']",
	},
	"deployment_environment": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "resourceattributes['deployment.environment']",
	},
	"request_payload": {
		Type: query.ColumnDefinitionTypeString,
	},
	"http_response": {
		Type: query.ColumnDefinitionTypeString,
	},
	"trace_source": {
		Type: query.ColumnDefinitionTypeString,
	},
	"resourceattributes": {
		Type: query.ColumnDefinitionTypeMap,
	},
	"spanattributes": {
		Type: query.ColumnDefinitionTypeMap,
	},
	"span_kind": {
		Type: query.ColumnDefinitionTypeString,
	},
	"trace_state": {
		Type: query.ColumnDefinitionTypeString,
	},
	"count": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "count(*)",
		IsAggregated: true,
	},
}

var ClickhouseTraceGroupingTableDefinition = map[string]query.ColumnDefinition{
	"account_id": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "''",
	},
	"timestamp": {
		Type: "datetime",
	},
	"workload_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
	},
	"workload_zone": {
		Type: query.ColumnDefinitionTypeString,
	},
	"duration_ns": {
		Type: query.ColumnDefinitionTypeFloat,
	},
	"status_code": {
		Type: query.ColumnDefinitionTypeString,
	},
	"span_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"resource": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_workload_name": {
		Type: query.ColumnDefinitionTypeString,
	},
	"destination_workload_zone": {
		Type: query.ColumnDefinitionTypeString,
	},
	"http_status_code": {
		Type: query.ColumnDefinitionTypeString,
	},
	"trace_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"span_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"parent_span_id": {
		Type: query.ColumnDefinitionTypeString,
	},
	"trace_source": {
		Type: query.ColumnDefinitionTypeString,
	},
	"count": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "count(*)",
		IsAggregated: true,
	},
	"error_count": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "SUM(CASE WHEN http_status_code LIKE '4%' OR http_status_code LIKE '5%' THEN 1 ELSE 0 END)",
		IsAggregated: true,
	},
	"p99_latency": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "quantile(0.99)(duration_ns)",
		IsAggregated: true,
	},
	"p50_latency": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "quantile(0.50)(duration_ns)",
		IsAggregated: true,
	},
	"p95_latency": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "quantile(0.95)(duration_ns)",
		IsAggregated: true,
	},
	"max_latency": {
		Type:         query.ColumnDefinitionTypeFloat,
		Def:          "MAX(duration_ns)",
		IsAggregated: true,
	},
	"service_name": {
		Type: query.ColumnDefinitionTypeString,
	},
}

type OtelClickhouseTraceSource struct{}

func (s *OtelClickhouseTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *OtelClickhouseTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like", "_ilike", "_nlike", "_gt", "_lt", "_gte", "_lte", "_is_null"}
}

// injectTimeFilter re-inserts timestamp._between into the where clause from StartTime/EndTime
// so the SQL generator can produce the correct time predicate.
func (s *OtelClickhouseTraceSource) injectTimeFilter(req *TracesV3Request) {
	if req.StartTime == 0 && req.EndTime == 0 {
		return
	}
	if req.QueryRequest.Where.Binary == nil {
		req.QueryRequest.Where.Binary = make(map[string]map[query.BinaryWhereClauseType]any)
	}
	between := map[string]any{}
	if req.StartTime != 0 {
		between["_gte"] = time.UnixMilli(req.StartTime).UTC().Format(time.RFC3339Nano)
	}
	if req.EndTime != 0 {
		between["_lte"] = time.UnixMilli(req.EndTime).UTC().Format(time.RFC3339Nano)
	}
	req.QueryRequest.Where.Binary["timestamp"] = map[query.BinaryWhereClauseType]any{
		Between: between,
	}
}

func (s *OtelClickhouseTraceSource) CountTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)
	if !hasAccess {
		return common.OpenTelemetryTraceCount{}, errors.New("user does not have access")
	}
	s.injectTimeFilter(&fetchTraceRequest)
	tableDef := s.getTraceTableDef(ctx, fetchTraceRequest.AccountId)
	tableDef.Type = query.Aggregate
	queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
	queryColumn := query.QueryColumn{}
	// queryColumn.Expr = ""
	queryColumn.Name = "count"

	queryRequest.Columns = []query.QueryColumn{queryColumn}
	sqlQuery, err := query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	result := common.OpenTelemetryTraceCount{}
	if len(rows) > 0 {
		// count(*) is a ClickHouse UInt64, which the relay's FORMAT JSON round-trip
		// delivers as a quoted string ("42"), not a float64. Decode via clickhouseInt64
		// so the count is not silently zeroed on the string shape.
		result.Count = int(clickhouseInt64(rows[0]["count"]))
	}
	return result, nil
}

func (s *OtelClickhouseTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	hasAccess := s.CheckAccess(ctx, tracesRequest.AccountId)
	if !hasAccess {
		return common.OpenTelemetryTraceGroupCount{}, errors.New("user does not have access")
	}
	s.injectTimeFilter(&tracesRequest)
	sqlQuery := ""
	var err error
	if tracesRequest.Query == "" {
		tableDef := s.getTraceGroupingTableDef(ctx, tracesRequest.AccountId)
		queryRequest := getQueryRequest(ctx, tracesRequest.QueryRequest, tableDef, "traces_grouping_v2")
		queryColumn := query.QueryColumn{}
		// queryColumn.Expr = ""
		queryColumn.Name = "count"
		queryRequest.Columns = []query.QueryColumn{queryColumn}
		queryRequest.OrderBy = []query.QueryOrderBy{}
		queryRequest.GroupBy = []string{
			"workload_name",
			"workload_namespace",
			"destination_workload_name",
			"destination_workload_namespace",
			"resource",
			"span_name",
			"http_status_code",
		}
		sqlQuery, err = query.GenerateSqlQuery(ctx, tracesRequest.AccountId, queryRequest, tableDef)
	} else {
		sqlQuery = tracesRequest.Query
	}
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, tracesRequest.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	result := common.OpenTelemetryTraceGroupCount{}
	if len(rows) > 0 {
		result.Count = len(rows)
	} else {
		// handle empty rows
		result.Count = 0
	}
	return result, nil

}

func (s *OtelClickhouseTraceSource) GetQuery(ctx *security.RequestContext, tracesRequest TracesV3Request) (string, error) {
	hasAccess := s.CheckAccess(ctx, tracesRequest.AccountId)
	if !hasAccess {
		return "", errors.New("user does not have access")
	}
	tableDef := s.getTraceTableDef(ctx, tracesRequest.AccountId)
	queryRequest := getQueryRequest(ctx, tracesRequest.QueryRequest, tableDef, "traces_v2")
	sqlQuery, err := query.GenerateSqlQuery(ctx, tracesRequest.AccountId, queryRequest, tableDef)
	if err != nil {
		return "", err
	}
	return sqlQuery, nil
}

// QueryLabels enumerates the span/resource attribute keys actually present in the
// account's traces within the time window, so traces_list_labels can advertise the real
// backend vocabulary (not just the static canonical fields). Isolation is by relay
// routing: executeClickhouseQuery targets the account's own warehouse (same trust model
// as every other trace query here). Implements TraceSource.QueryLabels.
func (s *OtelClickhouseTraceSource) QueryLabels(ctx *security.RequestContext, request FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	if !s.CheckAccess(ctx, request.AccountId) {
		return nil, errors.New("user does not have access")
	}

	// Default to the last hour when the caller supplies no window; bound the scan.
	end := time.Now().UTC()
	if request.EndTime != 0 {
		end = time.UnixMilli(request.EndTime).UTC()
	}
	start := end.Add(-1 * time.Hour)
	if request.StartTime != 0 {
		start = time.UnixMilli(request.StartTime).UTC()
	}
	const chTimeLayout = "2006-01-02 15:04:05"
	startStr := start.Format(chTimeLayout)
	endStr := end.Format(chTimeLayout)

	// mapKeys over SpanAttributes/ResourceAttributes can't be expressed via the shared
	// query generator (closed expression set), so build the discovery SQL directly. The
	// only interpolated values are server-derived timestamps (no user input).
	sqlQuery := fmt.Sprintf(
		"SELECT label FROM ("+
			"SELECT DISTINCT arrayJoin(mapKeys(SpanAttributes)) AS label FROM otel_traces WHERE Timestamp >= '%s' AND Timestamp <= '%s' "+
			"UNION DISTINCT "+
			"SELECT DISTINCT arrayJoin(mapKeys(ResourceAttributes)) AS label FROM otel_traces WHERE Timestamp >= '%s' AND Timestamp <= '%s'"+
			") WHERE label != '' LIMIT 5000",
		startStr, endStr, startStr, endStr,
	)

	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, request.AccountId)
	if err != nil {
		return nil, err
	}
	labels := make([]OutputTraceLabel, 0, len(rows))
	for _, row := range rows {
		if v, ok := row["label"]; ok && v != nil {
			if str := fmt.Sprintf("%v", v); str != "" {
				// Discovered keys have no known type — attributes stay {}.
				labels = append(labels, OutputTraceLabel{Label: str, Attributes: map[string]any{}})
			}
		}
	}
	return labels, nil
}

func (s *OtelClickhouseTraceSource) GetLabelValues(ctx *security.RequestContext, fetchTraceRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)
	if !hasAccess {
		return common.OpenTelemetryTraceLabelValues{}, errors.New("user does not have access")
	}

	// Inject time range into where clause for SQL generator (mirrors injectTimeFilter for QueryTraces)
	if fetchTraceRequest.StartTime != 0 && fetchTraceRequest.EndTime != 0 {
		if fetchTraceRequest.QueryRequest.Where.Binary == nil {
			fetchTraceRequest.QueryRequest.Where.Binary = make(query.BinaryWhereClause)
		}
		fetchTraceRequest.QueryRequest.Where.Binary["timestamp"] = map[query.BinaryWhereClauseType]any{
			query.Between: map[string]any{
				"_gte": time.UnixMilli(fetchTraceRequest.StartTime).UTC().Format(time.RFC3339Nano),
				"_lte": time.UnixMilli(fetchTraceRequest.EndTime).UTC().Format(time.RFC3339Nano),
			},
		}
	}

	tableDef := s.getTraceTableDef(ctx, fetchTraceRequest.AccountId)
	tableDef.Type = query.Aggregate
	queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
	queryRequest.GroupBy = []string{fetchTraceRequest.Label}
	queryColumn := query.QueryColumn{}
	// queryColumn.Expr = ""
	queryColumn.Name = fetchTraceRequest.Label

	queryRequest.Columns = []query.QueryColumn{queryColumn}
	sqlQuery, err := query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	var traceLabels []string
	for _, row := range rows {
		label := row[fetchTraceRequest.Label]
		traceLabels = append(traceLabels, fmt.Sprintf("%v", label))
	}
	result := common.OpenTelemetryTraceLabelValues{}
	result.Label = fetchTraceRequest.Label
	result.Values = traceLabels
	return result, nil
}

func (s *OtelClickhouseTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	traceIDQueryRequest := TracesQueryBuilderRequest{
		Where: query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"trace_id": map[query.BinaryWhereClauseType]any{
					query.Eq: fetchHeatMapRequest.TraceId,
				},
			},
		},
	}
	sqlQuery := ""
	tableDef := s.getTraceTableDef(ctx, fetchHeatMapRequest.AccountId)
	queryRequest := getQueryRequest(ctx, traceIDQueryRequest, tableDef, "traces_v2")
	var err error
	sqlQuery, err = query.GenerateSqlQuery(ctx, fetchHeatMapRequest.AccountId, queryRequest, tableDef)
	if err != nil {
		return []common.OpenTelemetryTraceHeatMap{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchHeatMapRequest.AccountId)
	if err != nil {
		return []common.OpenTelemetryTraceHeatMap{}, err
	}
	var otelTraces = []common.OpenTelemetryTraceHeatMap{}
	for _, row := range rows {
		otelTrace, err := MapRowToOpenTelemetryHeatmapTrace(row)
		if err != nil {
			return []common.OpenTelemetryTraceHeatMap{}, err
		}
		otelTraces = append(otelTraces, otelTrace)
	}
	return otelTraces, nil

}

func MapReourceAttributes(resourceAttributesRaw map[string]string, spanAttributes map[string]string) map[string]string {
	// Return all attributes without filtering to preserve complete OTEL metadata
	// This includes k8s.*, process.*, telemetry.sdk.*, service.*, cloud.*, host.*, etc.
	resourceAttributes := make(map[string]string)
	for k, v := range resourceAttributesRaw {
		resourceAttributes[k] = v
	}
	return resourceAttributes
}

func MapRowToOpenTelemetryTrace(row map[string]interface{}) (common.OpenTelemetryTrace, error) {
	trace := common.OpenTelemetryTrace{}
	if v, ok := row["trace_id"].(string); ok {
		trace.TraceID = v
	}
	if v, ok := row["span_id"].(string); ok {
		trace.SpanID = v
	}
	if v, ok := row["parent_span_id"].(string); ok {
		trace.ParentSpanID = v
	}
	if v, ok := row["timestamp"].(string); ok {
		trace.Timestamp = v
	}
	if v, ok := row["workload_name"].(string); ok {
		trace.WorkloadName = v
	}
	if v, ok := row["workload_namespace"].(string); ok {
		trace.WorkloadNamespace = v
	}
	trace.DurationNs = clickhouseInt64(row["duration_ns"])
	// status_code / http_status_code may arrive as a string (traces_v2 baseQuery) or a number
	// (the traces_view projection uses toInt32OrZero); handle both so the fields populate either way.
	if v, ok := row["status_code"].(string); ok {
		trace.StatusCode = v
	} else if f, ok := row["status_code"].(float64); ok && f != 0 {
		trace.StatusCode = strconv.Itoa(int(f))
	}
	if v, ok := row["span_name"].(string); ok {
		trace.SpanName = v
	}
	if v, ok := row["span_kind"].(string); ok {
		trace.SpanKind = v
	}
	if v, ok := row["resource"].(string); ok {
		trace.Resource = v
	}
	if v, ok := row["destination_workload_namespace"].(string); ok {
		trace.DestinationNamespace = v
	}
	if v, ok := row["destination_name"].(string); ok {
		trace.DestinationName = v
	}
	if v, ok := row["destination_workload_name"].(string); ok {
		trace.DestinationWorkload = v
	}
	if v, ok := row["headers"].(string); ok {
		trace.Headers = v
	}
	if v, ok := row["http_status_code"].(string); ok {
		trace.HTTPStatusCode = v
	} else if f, ok := row["http_status_code"].(float64); ok && f != 0 {
		trace.HTTPStatusCode = strconv.Itoa(int(f))
	}
	if v, ok := row["request_payload"].(string); ok {
		trace.RequestPayload = v
	}
	if v, ok := row["http_response"].(string); ok {
		trace.HTTPResponse = v
	}
	if v, ok := row["service_name"].(string); ok {
		trace.ServiceName = v
	}
	if v, ok := row["trace_source"].(string); ok {
		trace.TraceSource = v
	}
	if v, ok := row["service"].(string); ok {
		trace.Service = v
	}
	if v, ok := row["operation"].(string); ok {
		trace.Operation = v
	}
	if v, ok := row["trace_state"].(string); ok {
		trace.TraceState = v
	}
	if v, ok := row["query_type"].(string); ok {
		trace.QueryType = v
	}
	if v, ok := row["start_time"].(string); ok {
		trace.StartTime = v
	}
	if v, ok := row["end_time"].(string); ok {
		trace.EndTime = v
	}
	if v, ok := row["start_time_unix_nano"].(string); ok {
		trace.StartTimeUnixNano = v
	}
	if v, ok := row["end_time_unix_nano"].(string); ok {
		trace.EndTimeUnixNano = v
	}

	// JSON fields → keep as map[string]interface{}
	if v, ok := row["attributes"].(map[string]interface{}); ok {
		trace.Attributes = v
	}
	if _, ok := row["events"].(map[string]interface{}); ok {
		trace.EventsAttributes = []map[string]string{} // optional: map raw JSON later
	}
	if _, ok := row["links"].(map[string]interface{}); ok {
		trace.LinksAttributes = []map[string]string{}
	}
	if v, ok := row["status"].(map[string]interface{}); ok {
		trace.Status = v
	}
	if v, ok := row["tag_filters"].(map[string]interface{}); ok {
		trace.TagFilters = v
	}
	if v, ok := row["spanattributes"].(map[string]interface{}); ok {
		spanAttributes := make(map[string]string)
		for key, val := range v {
			spanAttributes[key] = fmt.Sprintf("%v", val)
		}
		trace.SpanAttributes = spanAttributes
	}
	if v, ok := row["resourceattributes"].(map[string]interface{}); ok {
		resourceAttributes := make(map[string]string)
		for key, val := range v {
			resourceAttributes[key] = fmt.Sprintf("%v", val)
		}
		trace.ResourceAttributes = MapReourceAttributes(resourceAttributes, trace.SpanAttributes)
	}

	return trace, nil
}

func MapGroupingRowToTraceGroupingValues(row map[string]interface{}) (TraceGroupingValues, error) {
	trace := TraceGroupingValues{}
	// ClickHouse's FORMAT JSON round-trip (via the relay) serialises 64-bit
	// integer aggregates — count(*), SUM(...), MAX(duration_ns) — as quoted
	// strings, so a strict .(float64) assertion misses them and zeroes the
	// field. clickhouseInt64 handles both the float64 (quantile) and string
	// (UInt64) encodings. See PR #33750 for the same fix on CountTraces.
	trace.Count = int(clickhouseInt64(row["count"]))
	trace.ErrorCount = int(clickhouseInt64(row["error_count"]))
	trace.P99Latency = clickhouseInt64(row["p99_latency"])
	trace.P95Latency = clickhouseInt64(row["p95_latency"])
	trace.MaxLatency = clickhouseInt64(row["max_latency"])
	if v, ok := row["workload_namespace"].(string); ok {
		trace.WorkloadNamespace = v
	}
	if v, ok := row["destination_workload_name"].(string); ok {
		trace.DestinationWorkloadName = v
	}
	if v, ok := row["destination_workload_namespace"].(string); ok {
		trace.DestinationWorkloadNamespace = v
	}
	if v, ok := row["resource"].(string); ok {
		trace.Resource = v
	}
	// if v, ok := row["http_status_code"].(string); ok {
	// 	trace.DurationNS = v
	// }
	if v, ok := row["http_status_code"].(string); ok {
		trace.HTTPStatusCode = v
	}
	if v, ok := row["span_name"].(string); ok {
		trace.SpanName = v
	}

	return trace, nil
}

func MapRowToOpenTelemetryHeatmapTrace(row map[string]interface{}) (common.OpenTelemetryTraceHeatMap, error) {
	trace := common.OpenTelemetryTraceHeatMap{}
	var spanAttributes map[string]string
	var resourceAttributes map[string]string

	if v, ok := row["trace_id"].(string); ok {
		trace.TraceID = v
	}
	if v, ok := row["span_id"].(string); ok {
		trace.SpanID = v
	}
	if v, ok := row["timestamp"].(string); ok {
		trace.Timestamp = v
	}
	trace.DurationNs = clickhouseInt64(row["duration_ns"])
	if v, ok := row["status_code"].(string); ok {
		trace.StatusCode = v
	}
	if v, ok := row["span_name"].(string); ok {
		trace.SpanName = v
	}
	if v, ok := row["service_name"].(string); ok {
		trace.ServiceName = v
	}
	if _, ok := row["events"].(map[string]interface{}); ok {
		trace.EventsAttributes = []map[string]string{} // optional: map raw JSON later
	}
	// ClickHouse Map columns come back as map[string]interface{} via the HTTP
	// JSON driver — this is what the non-heatmap mapper (MapRowToOpenTelemetryTrace)
	// already handles. The old string-only assertion silently missed every row,
	// so span_attributes / resource_attributes always arrived at the client as
	// null. We handle both shapes: the native map (common case) and a
	// stringified-JSON fallback for any path that produces it.
	spanAttributes = normalizeClickhouseAttrMap(row["spanattributes"])
	trace.SpanAttributes = spanAttributes
	resourceAttributes = normalizeClickhouseAttrMap(row["resourceattributes"])
	if len(resourceAttributes) > 0 {
		trace.ResourceAttributes = MapReourceAttributes(resourceAttributes, spanAttributes)
	}

	return trace, nil
}

// normalizeClickhouseAttrMap normalises a ClickHouse Map column value into a
// map[string]string regardless of whether the driver returned it as a native
// map or as a stringified-JSON representation. Returns nil when the input is
// nil or an unrecognised shape — caller treats nil as "attribute absent".
func normalizeClickhouseAttrMap(raw interface{}) map[string]string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for key, val := range v {
			out[key] = fmt.Sprintf("%v", val)
		}
		return out
	case map[string]string:
		// Some driver configurations return map[string]string directly.
		out := make(map[string]string, len(v))
		for key, val := range v {
			out[key] = val
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return m
		}
		// Fall through to map[string]interface{} unmarshal as last resort.
		var anyMap map[string]interface{}
		if err := json.Unmarshal([]byte(v), &anyMap); err == nil {
			out := make(map[string]string, len(anyMap))
			for key, val := range anyMap {
				out[key] = fmt.Sprintf("%v", val)
			}
			return out
		}
	}
	return nil
}

// clickhouseInt64 reads a ClickHouse numeric column out of a row map.
// ClickHouse's FORMAT JSON serialises 64-bit integers as quoted strings by
// default (to avoid precision loss in JSON parsers), so a UInt64 column
// like otel_traces.Duration arrives as a Go string, not a float64. We
// handle both shapes — float64 (for Float-typed or aggregate columns) and
// string (the default 64-bit-integer encoding). Returns 0 when the value
// is absent or unrecognised.
func clickhouseInt64(raw interface{}) int64 {
	switch v := raw.(type) {
	case float64:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func (s *OtelClickhouseTraceSource) CheckAccess(ctx *security.RequestContext, accountId string) bool {
	return ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead)
}

// executeClickhouseQuery runs a ClickHouse query via the relay and maps each row into a
// column-keyed map[string]any. Column order is lost in this shape; callers that need the raw
// ordered columns/types (e.g. the free-form agent trace path) should use executeClickhouseQueryRaw.
func (s OtelClickhouseTraceSource) executeClickhouseQuery(ctx context.Context, clickhouseQuery string, accountId string) ([]map[string]any, error) {
	raw, err := s.executeClickhouseQueryRaw(ctx, clickhouseQuery, accountId)
	if err != nil {
		return nil, err
	}

	rowsMap := make([]map[string]any, 0, len(raw.Rows))
	for _, rowValues := range raw.Rows {
		row := make(map[string]any, len(raw.Columns))
		for i, colName := range raw.Columns {
			if i < len(rowValues) {
				row[colName] = rowValues[i]
			} else {
				row[colName] = nil // handle missing values
			}
		}
		rowsMap = append(rowsMap, row)
	}

	return rowsMap, nil
}

// executeClickhouseQueryRaw runs a ClickHouse query via the relay and returns the result set with
// column order and types preserved. This is the source of truth for the round-trip + response
// envelope parsing; executeClickhouseQuery is a thin column-keyed wrapper over it.
func (s OtelClickhouseTraceSource) executeClickhouseQueryRaw(ctx context.Context, clickhouseQuery string, accountId string) (RawTraceResult, error) {
	httpClient := &http.Client{}
	requestData := map[string]any{
		"no_sinks": true,
		"cache":    false,
		"body": map[string]any{
			"account_id":  accountId,
			"action_name": "query_data",
			"action_params": map[string]any{
				"query": clickhouseQuery,
			},
		},
	}

	requestBody, err := common.MarshalJson(requestData)
	if err != nil {
		return RawTraceResult{}, err
	}

	stringReader := bytes.NewReader(requestBody)
	httpRequest, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), stringReader)
	if err != nil {
		slog.Error("agent: unable to execute query", "error", err)
		return RawTraceResult{}, fmt.Errorf("unable to execute query")
	}
	httpRequest.Header.Add("Content-Type", "application/json")
	httpRequest.Header.Add("Accept", "application/json")
	httpRequest.Header.Add("X-SECRET-KEY", config.Config.RelayServerSecretKey)

	resp, err := httpClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() == context.Canceled {
			slog.Warn("agent: query canceled by client", "error", err)
			return RawTraceResult{}, fmt.Errorf("query canceled: %w", ctx.Err())
		}
		if ctx.Err() == context.DeadlineExceeded {
			slog.Warn("agent: query timeout exceeded", "error", err)
			return RawTraceResult{}, fmt.Errorf("query timeout: %w", ctx.Err())
		}
		slog.Error("agent: unable to execute query", "error", err)
		return RawTraceResult{}, fmt.Errorf("unable to execute query: %w", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		// Read response body to get detailed error message
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("agent: unable to execute query", "status_code", resp.StatusCode)
			return RawTraceResult{}, fmt.Errorf("unable to execute query (status %d)", resp.StatusCode)
		}

		// Try to parse error response as JSON to get detailed message
		var errorResponse map[string]any
		if err := json.Unmarshal(response, &errorResponse); err == nil {
			slog.Error("agent: query failed with detailed error", "status_code", resp.StatusCode, "error_response", errorResponse)

			// Extract error message if available
			if message, hasMsg := errorResponse["message"]; hasMsg {
				return RawTraceResult{}, fmt.Errorf("clickhouse query error (status %d): %v", resp.StatusCode, message)
			} else if errorData, hasError := errorResponse["error"]; hasError {
				return RawTraceResult{}, fmt.Errorf("clickhouse query error (status %d): %v", resp.StatusCode, errorData)
			}
		}

		// Fallback: return response body as text
		slog.Error("agent: unable to execute query", "status_code", resp.StatusCode, "response_body", string(response))
		return RawTraceResult{}, fmt.Errorf("clickhouse query error (status %d): %s", resp.StatusCode, string(response))
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()

	response, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("agent: unable to read response", "error", err)
		return RawTraceResult{}, fmt.Errorf("unable to execute query")
	}

	jsonResponse := make(map[string]any)
	err = common.UnmarshalJson(response, &jsonResponse)
	if err != nil {
		return RawTraceResult{}, fmt.Errorf("unable to execute query")
	}

	if jsonResponse["status_code"].(float64) != 200 {
		slog.Error("agent: unable to execute query", "status_code", jsonResponse["status_code"].(float64))
		return RawTraceResult{}, fmt.Errorf("unable to execute query")
	}

	responseData := jsonResponse["data"]
	if responseData == nil {
		slog.Error("agent: unable to read response data", "response", slog.AnyValue(responseData))
		return RawTraceResult{}, fmt.Errorf("unable to read response data")
	}

	responseDataOuterMap, ok := responseData.(map[string]any)
	if !ok {
		slog.Error("agent: unable to read response data, not a map", "response", slog.AnyValue(responseData))
		return RawTraceResult{}, fmt.Errorf("unable to read response data: invalid format")
	}

	responseDataMapAny, ok := responseDataOuterMap["data"]
	if !ok {
		slog.Error("agent: unable to read inner response data", "response", slog.AnyValue(responseData))
		return RawTraceResult{}, fmt.Errorf("unable to read response data: missing inner data")
	}

	responseDataMap, ok := responseDataMapAny.(map[string]any)
	if !ok {
		slog.Error("agent: unable to read inner response data map", "response", slog.AnyValue(responseDataMapAny))
		return RawTraceResult{}, fmt.Errorf("unable to read response data: invalid inner data format")
	}

	return parseRawTraceResult(responseDataMap)
}

// parseRawTraceResult converts the relay `query_data` inner payload (the `data` map carrying
// `columns`, `column_types`, and `data` rows) into a RawTraceResult with column order and types
// preserved. It also surfaces ClickHouse-level errors that the relay reports inside a 200 envelope
// (`error_message` / a non-empty string `error`) so a failed query is not mistaken for empty data.
// Extracted as a pure function so the column/row ordering logic can be unit-tested without HTTP.
func parseRawTraceResult(responseDataMap map[string]any) (RawTraceResult, error) {
	if errorMessage, exists := responseDataMap["error_message"]; exists && errorMessage != nil {
		errorData := fmt.Sprintf("%v", errorMessage)
		if errorDetails, exists := responseDataMap["error_details"]; exists && errorDetails != nil {
			errorData = fmt.Sprintf("%s - %v", errorData, errorDetails)
		}
		slog.Error("agent: unable to execute query", "error", errorData)
		return RawTraceResult{}, fmt.Errorf("%s", errorData)
	}

	// The agent reports a successful round-trip (status_code 200, success=true) even when the
	// underlying ClickHouse query failed, surfacing the DB error under the `error` key with an
	// empty `data`/`columns` payload (e.g. an UNKNOWN_IDENTIFIER when the generated SQL references
	// a column that does not exist). Without this check that error was dropped and an empty result
	// returned, so a failed trace query looked like "no traces exist" to the caller.
	// Only a non-empty string under `error` indicates a failure. Some success envelopes carry
	// `"error": false` (or other non-string falsy values); those must not be treated as an error.
	if queryError, exists := responseDataMap["error"]; exists {
		if errStr, ok := queryError.(string); ok && strings.TrimSpace(errStr) != "" {
			slog.Error("agent: clickhouse query returned an error", "error", errStr)
			return RawTraceResult{}, fmt.Errorf("%s", errStr)
		}
	}

	dataCols, ok := responseDataMap["columns"].([]any)
	if !ok {
		return RawTraceResult{}, fmt.Errorf("invalid columns format")
	}

	cols := make([]string, len(dataCols))
	for i, col := range dataCols {
		colStr, ok := col.(string)
		if !ok {
			return RawTraceResult{}, fmt.Errorf("invalid column name at index %d: expected string, got %T", i, col)
		}
		cols[i] = colStr
	}

	colTypesRaw, ok := responseDataMap["column_types"].([]any)
	if !ok {
		return RawTraceResult{}, fmt.Errorf("invalid column_types format")
	}
	colTypes := make([]string, len(colTypesRaw))
	for i, ct := range colTypesRaw {
		ctStr, ok := ct.(string)
		if !ok {
			return RawTraceResult{}, fmt.Errorf("invalid column type at index %d: expected string, got %T", i, ct)
		}
		colTypes[i] = ctStr
	}

	// actual data rows
	rowsRaw, ok := responseDataMap["data"].([]any)
	if !ok {
		return RawTraceResult{}, fmt.Errorf("invalid data format")
	}

	rows := make([][]any, 0, len(rowsRaw))
	for _, r := range rowsRaw {
		rowValues, ok := r.([]any)
		if !ok {
			return RawTraceResult{}, fmt.Errorf("invalid row format")
		}
		rows = append(rows, rowValues)
	}

	return RawTraceResult{Columns: cols, ColumnTypes: colTypes, Rows: rows}, nil
}
func (s *OtelClickhouseTraceSource) GetBaseTraceQuery(ctx *security.RequestContext, accountId string) string {
	hasMaterializedColumn := s.hasMaterializedColumn(ctx, accountId)
	baseQuery := `(SELECT TraceId AS trace_id, SpanId AS span_id, ServiceName as service_name, ParentSpanId AS parent_span_id, workload_namespace, workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source, SpanAttributes as spanattributes, SpanKind as span_kind, ResourceAttributes as resourceattributes, TraceState as trace_state FROM otel_traces) AS traces_v2`
	if !hasMaterializedColumn {
		baseQuery = `(SELECT TraceId AS trace_id,SpanKind as span_kind, SpanId AS span_id, ParentSpanId AS parent_span_id, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN ScopeName = 'nudgebee-node-agent' OR SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source,TraceState as trace_state,ResourceAttributes as resourceattributes,SpanAttributes as spanattributes, ServiceName as service_name FROM otel_traces) AS traces_v2`
	}

	return baseQuery
}

func (s *OtelClickhouseTraceSource) GetBaseGroupingTraceQuery(ctx *security.RequestContext, accountId string) string {
	hasMaterializedColumn := s.hasMaterializedColumn(ctx, accountId)
	baseQuery := `(SELECT workload_zone, destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, cloud_availability_zone, workload_namespace,workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source FROM otel_traces) AS traces_grouping_v2`
	if !hasMaterializedColumn {
		baseQuery = `(SELECT ResourceAttributes['cloud.availability_zone'] AS workload_zone, SpanAttributes['destination.cloud.availablity_zone'] AS destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, ResourceAttributes['cloud.availability_zone'] AS cloud_availability_zone, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN ScopeName = 'nudgebee-node-agent' OR SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source FROM otel_traces) AS traces_grouping_v2`
	}

	return baseQuery
}

func (s *OtelClickhouseTraceSource) hasMaterializedColumn(ctx *security.RequestContext, accountId string) bool {
	agentDetails, err := account.GetAgentConnectionDetails(accountId)
	hasMaterializedColumn := false
	if err != nil {
		ctx.GetLogger().Error("query: unable to identify traces provider, returning default 'otel_clickhouse'", "error", err)
		return hasMaterializedColumn
	}
	if config := agentDetails.Features.TraceProviderConfig; config != nil {
		if val, ok := config["hasMaterializedColumn"].(bool); ok {
			hasMaterializedColumn = val
		} else {
			hasMaterializedColumn = false
		}
	}
	return hasMaterializedColumn
}
func (s *OtelClickhouseTraceSource) getTraceTableDef(ctx *security.RequestContext, AccountId string) query.TableDefinition {
	var tableDef query.TableDefinition
	tableDef.Columns = ClickhouseTraceTableDefinition
	tableDef.Source = database.AgentWarehouse
	tableDef.Type = query.Normal
	tableDef.Def = s.GetBaseTraceQuery(ctx, AccountId)
	tableDef.AccountIdColumnName = "account_id"
	tableDef.TenantIdColumnName = "tenant_id"
	tableDef.NamespaceColumnName = "workload_namespace"
	return tableDef
}

// chAttrQuote escapes a map key for safe inclusion as a ClickHouse string literal
// inside an attribute-map access (e.g. spanattributes['<key>']). The key comes from
// the model-supplied where clause, so it must never be interpolated raw.
func chAttrQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return "'" + s + "'"
}

// augmentTraceColumnsForAttributes returns the trace column set to use for WHERE
// resolution: the curated schema plus a dynamic definition for every referenced
// where-clause field that is not already a known column. Each dynamic field
// resolves to the ClickHouse attribute maps — span attributes first, resource
// attributes as fallback — so backend-discovered attributes advertised to the
// agent via traces_list_labels are queryable as flat fields (e.g. `rpc.method`,
// `db.system`). Returns the shared base map unchanged (no copy) when every field
// is already known; the base map is never mutated. Unknown/typo fields resolve to
// an empty attribute value (no rows) and are then surfaced by
// validateReferencedTraceLabels, which runs on empty/error results.
func augmentTraceColumnsForAttributes(base map[string]query.ColumnDefinition, where query.QueryWhereClause) map[string]query.ColumnDefinition {
	referenced := map[string]struct{}{}
	collectWhereFieldNames(where, referenced)
	var extra []string
	for k := range referenced {
		if k == "" {
			continue
		}
		if _, ok := base[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return base
	}
	cp := make(map[string]query.ColumnDefinition, len(base)+len(extra))
	for k, v := range base {
		cp[k] = v
	}
	for _, k := range extra {
		q := chAttrQuote(k)
		cp[k] = query.ColumnDefinition{
			Type:     query.ColumnDefinitionTypeString,
			WhereDef: fmt.Sprintf("if(mapContains(spanattributes, %s), spanattributes[%s], resourceattributes[%s])", q, q, q),
		}
	}
	return cp
}

// GetBaseRootSpanTraceQuery wraps the standard span base query in a subquery that keeps one row
// per trace — the root span (empty parent_span_id) or, when no root is captured in the window,
// the earliest span by timestamp. The time window is applied INSIDE this subquery (before the
// dedup) so `LIMIT 1 BY trace_id` only considers in-window spans; the outer SELECT generated by
// GenerateSqlQuery then applies the user filters, ordering, and pagination to the deduped root
// set (i.e. filters match the root span). startTime/endTime are unix-millis; both may be 0.
func (s *OtelClickhouseTraceSource) GetBaseRootSpanTraceQuery(ctx *security.RequestContext, accountId string, startTime, endTime int64) string {
	base := s.GetBaseTraceQuery(ctx, accountId)
	conds := []string{}
	// Wrap with parseDateTimeBestEffort so ClickHouse parses the RFC3339Nano string (T separator,
	// trailing Z) into a DateTime before comparing against the DateTime64(9) timestamp column. A
	// bare quoted literal triggers an implicit string->DateTime64 cast that rejects the ISO format
	// (TYPE_MISMATCH). This mirrors how the query builder renders datetime _between filters
	// (sql_dialect_clickhouse.go FuncStringToDatetime), keeping this path consistent with "By Spans".
	if startTime != 0 {
		conds = append(conds, fmt.Sprintf("timestamp >= parseDateTimeBestEffort('%s')", time.UnixMilli(startTime).UTC().Format(time.RFC3339Nano)))
	}
	if endTime != 0 {
		conds = append(conds, fmt.Sprintf("timestamp <= parseDateTimeBestEffort('%s')", time.UnixMilli(endTime).UTC().Format(time.RFC3339Nano)))
	}
	timeClause := ""
	if len(conds) > 0 {
		timeClause = " WHERE " + strings.Join(conds, " AND ")
	}
	// (parent_span_id = '') DESC sorts root spans first; timestamp ASC breaks ties / picks the
	// earliest span as a pseudo-root when the real root is outside the window. LIMIT 1 BY trace_id
	// then keeps exactly one row per trace.
	return fmt.Sprintf("(SELECT * FROM %s%s ORDER BY trace_id, (parent_span_id = '') DESC, timestamp ASC LIMIT 1 BY trace_id) AS traces_v2", base, timeClause)
}

func (s *OtelClickhouseTraceSource) getRootSpanTraceTableDef(ctx *security.RequestContext, AccountId string, startTime, endTime int64) query.TableDefinition {
	tableDef := s.getTraceTableDef(ctx, AccountId)
	tableDef.Def = s.GetBaseRootSpanTraceQuery(ctx, AccountId, startTime, endTime)
	return tableDef
}

func (s *OtelClickhouseTraceSource) getTraceGroupingTableDef(ctx *security.RequestContext, AccountId string) query.TableDefinition {
	var tableDef query.TableDefinition
	tableDef.Columns = ClickhouseTraceGroupingTableDefinition
	tableDef.Source = database.AgentWarehouse
	tableDef.Type = query.Aggregate
	tableDef.Def = s.GetBaseGroupingTraceQuery(ctx, AccountId)
	tableDef.AccountIdColumnName = "account_id"
	tableDef.TenantIdColumnName = "tenant_id"
	tableDef.NamespaceColumnName = "workload_namespace"
	return tableDef
}

func (s *OtelClickhouseTraceSource) QueryTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)

	// temp handling to be removed in future
	spanAttri, ok := fetchTraceRequest.QueryRequest.Where.Binary["spanattributes"]
	if ok {
		// Iterate over operators for spanattributes to correctly handle modifications.
		for op, val := range spanAttri {
			valueMap, ok := val.(map[string]interface{})
			if !ok {
				continue
			}

			if _, hasServiceName := valueMap["service.name"]; hasServiceName {
				// This temporary logic removes `service.name` from the spanattributes filter.
				// The intention is likely to handle it as a top-level `service_name` filter instead.
				delete(valueMap, "service.name")

				// If the attribute map for an operator becomes empty, remove the operator.
				if len(valueMap) == 0 {
					delete(spanAttri, op)
				}
			}
		}

		// If the spanattributes filter has no more operators, remove it entirely.
		if len(spanAttri) == 0 {
			delete(fetchTraceRequest.QueryRequest.Where.Binary, "spanattributes")
		} else {
			fetchTraceRequest.QueryRequest.Where.Binary["spanattributes"] = spanAttri
		}
	}
	if !hasAccess {
		return []common.OpenTelemetryTrace{}, errors.New("user does not have access")
	}
	s.injectTimeFilter(&fetchTraceRequest)
	sqlQuery := ""
	var err error
	if fetchTraceRequest.Query == "" {
		tableDef := s.getTraceTableDef(ctx, fetchTraceRequest.AccountId)
		// Make backend-discovered span/resource attributes (advertised to the agent
		// via traces_list_labels) queryable as flat canonical fields: any where-clause
		// field not in the curated schema resolves to the attribute maps. Only affects
		// WHERE resolution; SELECT still uses the curated column set.
		tableDef.Columns = augmentTraceColumnsForAttributes(tableDef.Columns, fetchTraceRequest.QueryRequest.Where)
		queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
		sqlQuery, err = query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	} else {
		sqlQuery = fetchTraceRequest.Query
	}
	if err != nil {
		return []common.OpenTelemetryTrace{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return []common.OpenTelemetryTrace{}, err
	}
	var otelTraces = []common.OpenTelemetryTrace{}
	for _, row := range rows {
		otelTrace, err := MapRowToOpenTelemetryTrace(row)
		if err != nil {
			return []common.OpenTelemetryTrace{}, err
		}
		otelTraces = append(otelTraces, otelTrace)
	}
	return otelTraces, nil
}

// sanitizeServiceNameSpanAttribute strips a service.name filter nested under spanattributes,
// mirroring QueryTraces: service.name is handled as a top-level service_name filter elsewhere, so
// leaving it under spanattributes would double-filter. Applied to BOTH the root-span list and its
// count so the two stay consistent when filtering by service. No-op when there is no binary where
// clause (Where is a struct value, so only Binary — a map — can be nil).
func sanitizeServiceNameSpanAttribute(fetchTraceRequest *TracesV3Request) {
	if fetchTraceRequest.QueryRequest.Where.Binary == nil {
		return
	}
	spanAttri, ok := fetchTraceRequest.QueryRequest.Where.Binary["spanattributes"]
	if !ok {
		return
	}
	for op, val := range spanAttri {
		valueMap, ok := val.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasServiceName := valueMap["service.name"]; hasServiceName {
			delete(valueMap, "service.name")
			if len(valueMap) == 0 {
				delete(spanAttri, op)
			}
		}
	}
	if len(spanAttri) == 0 {
		delete(fetchTraceRequest.QueryRequest.Where.Binary, "spanattributes")
	} else {
		fetchTraceRequest.QueryRequest.Where.Binary["spanattributes"] = spanAttri
	}
}

// QueryRootSpansByTrace backs the "By Traces" view for ClickHouse: it returns one root span per
// trace by deduping inside the table-def subquery (see GetBaseRootSpanTraceQuery). The time window
// is applied inside that subquery, so it is NOT injected into the outer where clause here; the
// user filters, order, and limit/offset are applied by the generator to the deduped root set.
func (s *OtelClickhouseTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)
	if !hasAccess {
		return []common.OpenTelemetryTrace{}, errors.New("user does not have access")
	}
	sanitizeServiceNameSpanAttribute(&fetchTraceRequest)
	sqlQuery := ""
	var err error
	if fetchTraceRequest.Query == "" {
		tableDef := s.getRootSpanTraceTableDef(ctx, fetchTraceRequest.AccountId, fetchTraceRequest.StartTime, fetchTraceRequest.EndTime)
		queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
		sqlQuery, err = query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	} else {
		sqlQuery = fetchTraceRequest.Query
	}
	if err != nil {
		return []common.OpenTelemetryTrace{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return []common.OpenTelemetryTrace{}, err
	}
	var otelTraces = []common.OpenTelemetryTrace{}
	for _, row := range rows {
		otelTrace, err := MapRowToOpenTelemetryTrace(row)
		if err != nil {
			return []common.OpenTelemetryTrace{}, err
		}
		otelTraces = append(otelTraces, otelTrace)
	}
	return otelTraces, nil
}

// CountTracesByTrace returns the number of distinct traces matching the filters for the
// "By Traces" view. Because the root-span table def already dedups to one row per trace, a plain
// count(*) over it equals the distinct-trace count. It applies the same service.name sanitization
// as QueryRootSpansByTrace so the count and the list stay consistent.
func (s *OtelClickhouseTraceSource) CountTracesByTrace(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)
	if !hasAccess {
		return common.OpenTelemetryTraceCount{}, errors.New("user does not have access")
	}
	sanitizeServiceNameSpanAttribute(&fetchTraceRequest)
	tableDef := s.getRootSpanTraceTableDef(ctx, fetchTraceRequest.AccountId, fetchTraceRequest.StartTime, fetchTraceRequest.EndTime)
	tableDef.Type = query.Aggregate
	queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
	queryColumn := query.QueryColumn{}
	queryColumn.Name = "count"
	queryRequest.Columns = []query.QueryColumn{queryColumn}
	queryRequest.OrderBy = []query.QueryOrderBy{}
	sqlQuery, err := query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	result := common.OpenTelemetryTraceCount{}
	if len(rows) > 0 {
		// count(*) is a ClickHouse UInt64, which the relay's FORMAT JSON round-trip
		// delivers as a quoted string ("42"), not a float64. Decode via clickhouseInt64
		// so the count is not silently zeroed on the string shape (mirrors CountTraces).
		result.Count = int(clickhouseInt64(rows[0]["count"]))
	}
	return result, nil
}

// QueryTracesRaw runs the same SQL path as QueryTraces but returns the raw ClickHouse result set
// (columns/types/rows) instead of coercing it into the fixed OpenTelemetryTrace span schema. It is
// used by the free-form agent trace path (IncludeRawResult) so aggregation / custom-projection
// queries return their real computed columns. The query executes exactly once.
func (s *OtelClickhouseTraceSource) QueryTracesRaw(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (RawTraceResult, error) {
	// Fail fast on an empty account before touching the data path — an unscoped
	// query must never reach ClickHouse in a multi-tenant deployment.
	if fetchTraceRequest.AccountId == "" {
		return RawTraceResult{}, errors.New("account_id is required")
	}
	if !s.CheckAccess(ctx, fetchTraceRequest.AccountId) {
		return RawTraceResult{}, errors.New("user does not have access")
	}

	s.injectTimeFilter(&fetchTraceRequest)
	sqlQuery := ""
	var err error
	if fetchTraceRequest.Query == "" {
		tableDef := s.getTraceTableDef(ctx, fetchTraceRequest.AccountId)
		queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_v2")
		sqlQuery, err = query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
		if err != nil {
			return RawTraceResult{}, err
		}
	} else {
		sqlQuery = fetchTraceRequest.Query
	}

	return s.executeClickhouseQueryRaw(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
}

func (s *OtelClickhouseTraceSource) QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error) {
	hasAccess := s.CheckAccess(ctx, fetchTraceRequest.AccountId)
	if !hasAccess {
		return []TraceGroupingValues{}, errors.New("user does not have access")
	}
	s.injectTimeFilter(&fetchTraceRequest)
	sqlQuery := ""
	var err error
	if fetchTraceRequest.Query == "" {
		tableDef := s.getTraceGroupingTableDef(ctx, fetchTraceRequest.AccountId)
		queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, tableDef, "traces_grouping_v2")
		queryRequest.Columns = []query.QueryColumn{
			{Name: "count"},
			{Name: "error_count"},
			{Name: "p99_latency"},
			{Name: "p95_latency"},
			{Name: "max_latency"},
			{Name: "workload_name"},
			{Name: "workload_namespace"},
			{Name: "destination_workload_name"},
			{Name: "destination_workload_namespace"},
			{Name: "resource"},
			{Name: "span_name"},
			{Name: "http_status_code"},
		}
		queryRequest.GroupBy = []string{
			"workload_name",
			"workload_namespace",
			"destination_workload_name",
			"destination_workload_namespace",
			"resource",
			"span_name",
			"http_status_code",
		}
		sqlQuery, err = query.GenerateSqlQuery(ctx, fetchTraceRequest.AccountId, queryRequest, tableDef)
	} else {
		sqlQuery = fetchTraceRequest.Query
	}
	if err != nil {
		return []TraceGroupingValues{}, err
	}
	rows, err := s.executeClickhouseQuery(ctx.GetContext(), sqlQuery, fetchTraceRequest.AccountId)
	if err != nil {
		return []TraceGroupingValues{}, err
	}
	var otelTraces = []TraceGroupingValues{}
	for _, row := range rows {
		otelTrace, err := MapGroupingRowToTraceGroupingValues(row)
		if err != nil {
			return []TraceGroupingValues{}, err
		}
		otelTraces = append(otelTraces, otelTrace)
	}
	return otelTraces, nil
}
