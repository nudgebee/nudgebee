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
		if countVal, ok := rows[0]["count"]; ok {
			if countFloat, ok := countVal.(float64); ok {
				result.Count = int(countFloat)
			} else {
				// handle wrong type
				result.Count = 0
			}
		} else {
			// handle missing key
			result.Count = 0
		}
	} else {
		// handle empty rows
		result.Count = 0
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
	if v, ok := row["status_code"].(string); ok {
		trace.StatusCode = v
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
	if v, ok := row["count"].(float64); ok {
		trace.Count = int(v)
	}
	if v, ok := row["error_count"].(float64); ok {
		trace.ErrorCount = int(v)
	}

	if v, ok := row["p99_latency"].(float64); ok {
		trace.P99Latency = int64(v)
	}
	if v, ok := row["p95_latency"].(float64); ok {
		trace.P95Latency = int64(v)
	}

	if v, ok := row["max_latency"].(float64); ok {
		trace.MaxLatency = int64(v)
	}
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

func (s OtelClickhouseTraceSource) executeClickhouseQuery(ctx context.Context, clickhouseQuery string, accountId string) ([]map[string]any, error) {
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
		return nil, err
	}

	stringReader := bytes.NewReader(requestBody)
	httpRequest, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), stringReader)
	if err != nil {
		slog.Error("agent: unable to execute query", "error", err)
		return nil, fmt.Errorf("unable to execute query")
	}
	httpRequest.Header.Add("Content-Type", "application/json")
	httpRequest.Header.Add("Accept", "application/json")
	httpRequest.Header.Add("X-SECRET-KEY", config.Config.RelayServerSecretKey)

	resp, err := httpClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() == context.Canceled {
			slog.Warn("agent: query canceled by client", "error", err)
			return nil, fmt.Errorf("query canceled: %w", ctx.Err())
		}
		if ctx.Err() == context.DeadlineExceeded {
			slog.Warn("agent: query timeout exceeded", "error", err)
			return nil, fmt.Errorf("query timeout: %w", ctx.Err())
		}
		slog.Error("agent: unable to execute query", "error", err)
		return nil, fmt.Errorf("unable to execute query: %w", err)
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
			return nil, fmt.Errorf("unable to execute query (status %d)", resp.StatusCode)
		}

		// Try to parse error response as JSON to get detailed message
		var errorResponse map[string]any
		if err := json.Unmarshal(response, &errorResponse); err == nil {
			slog.Error("agent: query failed with detailed error", "status_code", resp.StatusCode, "error_response", errorResponse)

			// Extract error message if available
			if message, hasMsg := errorResponse["message"]; hasMsg {
				return nil, fmt.Errorf("clickhouse query error (status %d): %v", resp.StatusCode, message)
			} else if errorData, hasError := errorResponse["error"]; hasError {
				return nil, fmt.Errorf("clickhouse query error (status %d): %v", resp.StatusCode, errorData)
			}
		}

		// Fallback: return response body as text
		slog.Error("agent: unable to execute query", "status_code", resp.StatusCode, "response_body", string(response))
		return nil, fmt.Errorf("clickhouse query error (status %d): %s", resp.StatusCode, string(response))
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
		return nil, fmt.Errorf("unable to execute query")
	}

	jsonResponse := make(map[string]any)
	err = common.UnmarshalJson(response, &jsonResponse)
	if err != nil {
		return nil, fmt.Errorf("unable to execute query")
	}

	if jsonResponse["status_code"].(float64) != 200 {
		slog.Error("agent: unable to execute query", "status_code", jsonResponse["status_code"].(float64))
		return nil, fmt.Errorf("unable to execute query")
	}

	responseData := jsonResponse["data"]
	if responseData == nil {
		slog.Error("agent: unable to read response data", "response", slog.AnyValue(responseData))
		return nil, fmt.Errorf("unable to read response data")
	}

	responseDataOuterMap, ok := responseData.(map[string]any)
	if !ok {
		slog.Error("agent: unable to read response data, not a map", "response", slog.AnyValue(responseData))
		return nil, fmt.Errorf("unable to read response data: invalid format")
	}

	responseDataMapAny, ok := responseDataOuterMap["data"]
	if !ok {
		slog.Error("agent: unable to read inner response data", "response", slog.AnyValue(responseData))
		return nil, fmt.Errorf("unable to read response data: missing inner data")
	}

	responseDataMap, ok := responseDataMapAny.(map[string]any)
	if !ok {
		slog.Error("agent: unable to read inner response data map", "response", slog.AnyValue(responseDataMapAny))
		return nil, fmt.Errorf("unable to read response data: invalid inner data format")
	}

	if errorMessage, exists := responseDataMap["error_message"]; exists && errorMessage != nil {
		errorData := fmt.Sprintf("%v", errorMessage)
		if errorDetails, exists := responseDataMap["error_details"]; exists && errorDetails != nil {
			errorData = fmt.Sprintf("%s - %v", errorData, errorDetails)
		}
		slog.Error("agent: unable to execute query", "error", errorData)
		return nil, fmt.Errorf("%s", errorData)
	}

	dataCols, ok := responseDataMap["columns"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid columns format")
	}

	cols := make([]string, len(dataCols))
	for i, col := range dataCols {
		cols[i] = col.(string)
	}

	// column types (if you need them later)
	colTypesRaw, ok := responseDataMap["column_types"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid column_types format")
	}
	colTypes := make([]string, len(colTypesRaw))
	for i, ct := range colTypesRaw {
		colTypes[i] = ct.(string)
	}

	// actual data rows
	rowsRaw, ok := responseDataMap["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	// final result
	rowsMap := make([]map[string]any, 0, len(rowsRaw))

	for _, r := range rowsRaw {
		rowValues, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("invalid row format")
		}

		row := make(map[string]any, len(cols))
		for i, colName := range cols {
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
func (s *OtelClickhouseTraceSource) GetBaseTraceQuery(ctx *security.RequestContext, accountId string) string {
	hasMaterializedColumn := s.hasMaterializedColumn(ctx, accountId)
	baseQuery := `(SELECT TraceId AS trace_id, SpanId AS span_id, ServiceName as service_name, ParentSpanId AS parent_span_id, workload_namespace, workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source, SpanAttributes as spanattributes, SpanKind as span_kind, ResourceAttributes as resourceattributes, TraceState as trace_state FROM otel_traces) AS traces_v2`
	if !hasMaterializedColumn {
		baseQuery = `(SELECT TraceId AS trace_id,SpanKind as span_kind, SpanId AS span_id, ParentSpanId AS parent_span_id, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source,TraceState as trace_state,ResourceAttributes as resourceattributes,SpanAttributes as spanattributes, ServiceName as service_name FROM otel_traces) AS traces_v2`
	}

	return baseQuery
}

func (s *OtelClickhouseTraceSource) GetBaseGroupingTraceQuery(ctx *security.RequestContext, accountId string) string {
	hasMaterializedColumn := s.hasMaterializedColumn(ctx, accountId)
	baseQuery := `(SELECT workload_zone, destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, cloud_availability_zone, workload_namespace,workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source FROM otel_traces) AS traces_grouping_v2`
	if !hasMaterializedColumn {
		baseQuery = `(SELECT ResourceAttributes['cloud.availability_zone'] AS workload_zone, SpanAttributes['destination.cloud.availablity_zone'] AS destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, ResourceAttributes['cloud.availability_zone'] AS cloud_availability_zone, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source FROM otel_traces) AS traces_grouping_v2`
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
