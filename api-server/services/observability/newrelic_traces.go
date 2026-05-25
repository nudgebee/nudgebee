package observability

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"
)

// NewRelicTraceSource implements TraceSource interface for New Relic
type NewRelicTraceSource struct{}

// newRelicTraceLabelMapping maps standard field names to New Relic Span field names
// Note: destination_workload_namespace is intentionally not mapped - no direct NR field provides namespace
var newRelicTraceLabelMapping = map[string]string{
	"workload_namespace":        "k8s.namespace.name",
	"workload_name":             "service.name",
	"destination_workload_name": "server.address",
	"http_status_code":          "http.response.status_code",
	"span_name":                 "name",
	"resource":                  "http.url",
	"status_code":               "otel.status_code",
	"trace_id":                  "trace.id",
	"span_id":                   "span.id",
	"parent_id":                 "parent.id",
	"@http.method":              "http.method",
	"@http.route":               "http.url",
	"@http.status_code":         "http.response.status_code",
	"@k8s.namespace.name":       "k8s.namespace.name",
	"@k8s.container.name":       "k8s.container.name",
	"@k8s.deployment.name":      "k8s.deployment.name",
	"@k8s.pod.name":             "k8s.pod.name",
	"@k8s.node.name":            "k8s.node.name",
	"@service.version":          "service.version",
	"@telemetry.sdk.language":   "telemetry.sdk.language",
}

// QueryTraces fetches spans from New Relic using NRQL
func (s *NewRelicTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryTraces: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Get time range (also removes timestamp and trace_source from Binary)
	startTime, endTime := s.getTimeRange(&req)

	// Build NRQL query
	nrqlQuery, err := s.buildNRQLSpanQuery(req, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to build NRQL span query: %w", err)
	}

	ctx.GetLogger().Info("NewRelic Trace Query", "query", nrqlQuery)

	// Execute NRQL query
	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryTraces: NRQL query failed", "query", nrqlQuery, "error", err)
		return nil, fmt.Errorf("failed to execute NRQL span query: %w", err)
	}

	// Convert to OpenTelemetryTrace format
	return s.convertNRSpansToOTelTraces(results), nil
}

// GetQuery returns the NRQL query string for the request
func (s *NewRelicTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	startTime, endTime := s.getTimeRange(&req)
	return s.buildNRQLSpanQuery(req, startTime, endTime)
}

// CountTraces returns count of matching traces
func (s *NewRelicTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.CountTraces: failed to get configs", "error", err)
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	startTime, endTime := s.getTimeRange(&req)

	// Build WHERE clause — use buildNRQLSpanWhereClause so duration_ns is converted to
	// duration.ms and service.name Eq is emitted correctly for span records.
	whereClause, err := buildNRQLSpanWhereClause(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build count query
	var sb strings.Builder
	sb.WriteString("SELECT uniqueCount(`trace.id`) as count FROM Span")
	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}
	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, sb.String())
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to execute count query: %w", err)
	}

	count := 0
	if len(results) > 0 {
		if c, ok := results[0]["count"].(float64); ok {
			count = int(c)
		}
	}

	return common.OpenTelemetryTraceCount{Count: count}, nil
}

// GetLabelValues returns unique values for a label
func (s *NewRelicTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.GetLabelValues: failed to get configs", "error", err)
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Map label name if needed
	labelName := req.Label
	if mapped, ok := newRelicTraceLabelMapping[labelName]; ok {
		labelName = mapped
	}

	// Step 1: resolve time range.
	// Prefer top-level fields; fall back to extracting from binary where clause
	// for backward compat with callers that do not yet send start_time/end_time.
	startTime := req.StartTime
	endTime := req.EndTime
	if startTime == 0 || endTime == 0 {
		binaryStart, binaryEnd := cleanBinaryWhereClause(req.QueryRequest.Where.Binary)
		if startTime == 0 {
			startTime = binaryStart
		}
		if endTime == 0 {
			endTime = binaryEnd
		}
	} else {
		// Top-level fields are set: skip extraction but still clean binary so
		// timestamp and trace_source are not emitted as NRQL WHERE conditions.
		cleanBinaryWhereClause(req.QueryRequest.Where.Binary)
	}
	// Convert milliseconds to seconds if needed
	if startTime > 1e12 {
		startTime = startTime / 1000
	}
	if endTime > 1e12 {
		endTime = endTime / 1000
	}
	// Default to last 24 hours if not specified
	if startTime == 0 {
		startTime = time.Now().Add(-24 * time.Hour).Unix()
	}
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	// Build WHERE clause — use buildNRQLSpanWhereClause so duration_ns is converted to
	// duration.ms and service.name Eq is emitted correctly for span records.
	whereClause, err := buildNRQLSpanWhereClause(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build query for unique values
	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT uniques(`%s`, 100) FROM Span", labelName)
	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}
	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, sb.String())
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to execute label values query: %w", err)
	}

	var values []string
	for _, r := range results {
		uniquesKey := fmt.Sprintf("uniques.%s", labelName)
		if uniques, ok := r[uniquesKey].([]any); ok {
			for _, val := range uniques {
				switch v := val.(type) {
				case string:
					values = append(values, v)
				case float64:
					// New Relic returns numeric fields (e.g. http.response.status_code) as float64.
					// Format without decimal when the value is a whole number.
					if v == float64(int64(v)) {
						values = append(values, fmt.Sprintf("%d", int64(v)))
					} else {
						values = append(values, fmt.Sprintf("%g", v))
					}
				}
			}
		}
	}

	return common.OpenTelemetryTraceLabelValues{
		Label:  req.Label,
		Values: values,
	}, nil
}

// QueryGroupedTraces returns traces grouped by specified fields
func (s *NewRelicTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryGroupedTraces: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	startTime, endTime := s.getTimeRange(&req)

	// Build WHERE clause — use buildNRQLSpanWhereClause so duration_ns is converted to
	// duration.ms and service.name Eq is emitted correctly for span records.
	whereClause, err := buildNRQLSpanWhereClause(req.QueryRequest.Where)
	if err != nil {
		return nil, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build grouped query with aggregations
	var sb strings.Builder
	sb.WriteString("SELECT count(*) as count, ")
	sb.WriteString("filter(count(*), WHERE `otel.status_code` = 'ERROR') as error_count, ")
	sb.WriteString("average(`duration.ms`) as avg_duration, ")
	sb.WriteString("percentile(`duration.ms`, 95) as p95_latency, ")
	sb.WriteString("percentile(`duration.ms`, 99) as p99_latency, ")
	sb.WriteString("max(`duration.ms`) as max_latency, ")
	sb.WriteString("latest(`http.url`) as resource_url ")
	sb.WriteString("FROM Span")

	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}

	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)
	sb.WriteString(" FACET `service.name`, `k8s.namespace.name`, name, `http.response.status_code`")

	limit := req.QueryRequest.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	fmt.Fprintf(&sb, " LIMIT %d", limit)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, sb.String())
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryGroupedTraces: NRQL query failed", "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return nil, fmt.Errorf(
				"trace group query timed out — the selected time range contains too many spans. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return nil, fmt.Errorf("failed to execute grouped traces query: %w", err)
	}

	return s.convertNRGroupedTracesToTraceGroupingValues(results), nil
}

// QueryGroupedTracesCount returns count of grouped traces
func (s *NewRelicTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryGroupedTracesCount: failed to get configs", "error", err)
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	startTime, endTime := s.getTimeRange(&req)

	// Build WHERE clause — use buildNRQLSpanWhereClause so duration_ns is converted to
	// duration.ms and service.name Eq is emitted correctly for span records.
	whereClause, err := buildNRQLSpanWhereClause(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Count unique combinations of service.name, k8s.namespace.name, name, and http.response.status_code
	var sb strings.Builder
	sb.WriteString("SELECT uniqueCount(concat(`service.name`, '-', `k8s.namespace.name`, '-', name, '-', `http.response.status_code`)) as count FROM Span")

	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}

	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, sb.String())
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryGroupedTracesCount: NRQL query failed", "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf(
				"trace group count query timed out — the selected time range contains too many spans. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to execute grouped traces count query: %w", err)
	}

	count := 0
	if len(results) > 0 {
		if c, ok := results[0]["count"].(float64); ok {
			count = int(c)
		}
	}

	return common.OpenTelemetryTraceGroupCount{Count: count}, nil
}

// QueryTracesHeatmap fetches trace heatmap data for a specific trace
func (s *NewRelicTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicTraceSource.QueryTracesHeatmap: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Use a wide time range (last 30 days)
	endTime := time.Now().Unix()
	startTime := endTime - (30 * 24 * 60 * 60)

	// Build query for specific trace
	nrqlQuery := fmt.Sprintf("SELECT * FROM Span WHERE `trace.id` = '%s' SINCE %d UNTIL %d LIMIT 2000",
		escapeNRQLValue(req.TraceId), startTime, endTime)

	ctx.GetLogger().Info("NewRelic Trace Heatmap Query", "query", nrqlQuery)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to execute heatmap query: %w", err)
	}

	return s.convertNRSpansToOTelHeatmap(results), nil
}

// GetLabelMapping returns the field name mapping for traces
func (s *NewRelicTraceSource) GetLabelMapping() map[string]string {
	return newRelicTraceLabelMapping
}

func (s *NewRelicTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like", "_ilike", "_in", "_not_in", "_contains"}
}

// buildNRQLSpanQuery constructs the complete NRQL query for spans
func (s *NewRelicTraceSource) buildNRQLSpanQuery(req TracesV3Request, startTime, endTime int64) (string, error) {
	var sb strings.Builder
	sb.WriteString("SELECT * FROM Span")

	// Add WHERE clause if present
	var whereClause string
	var err error

	if req.Query != "" {
		whereClause = req.Query
	} else if hasWhereConditions(req.QueryRequest.Where) {
		whereClause, err = buildNRQLSpanWhereClause(req.QueryRequest.Where)
		if err != nil {
			return "", fmt.Errorf("failed to build WHERE clause: %w", err)
		}
	}

	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}

	// Add time range
	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	// Add limit
	limit := req.QueryRequest.Limit
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	fmt.Fprintf(&sb, " LIMIT %d", limit)

	return sb.String(), nil
}

// buildNRQLSpanWhereClause builds a NRQL WHERE clause for Span (trace) queries.
// The shared buildNRQLWhereClause converts service.name Eq conditions to
// "pod_name LIKE 'value%'" for log queries. Span records do not have a pod_name
// field, so we intercept service.name Eq here and emit the correct NRQL directly.
// duration_ns is also intercepted to convert nanoseconds to milliseconds, since New Relic
// stores span duration in the `duration.ms` field (milliseconds).
func buildNRQLSpanWhereClause(where query.QueryWhereClause) (string, error) {
	var serviceNameEqClause string
	if len(where.Binary) > 0 {
		if svcOps, ok := where.Binary["service.name"]; ok {
			if eqVal, exists := svcOps[query.Eq]; exists {
				delete(svcOps, query.Eq)
				if len(svcOps) == 0 {
					delete(where.Binary, "service.name")
				}
				serviceNameEqClause = fmt.Sprintf("`service.name` = '%s'", escapeNRQLValue(eqVal))
			}
		}
	}

	// duration_ns is our canonical field (nanoseconds). New Relic spans use `duration.ms`
	// (milliseconds), so we convert the value before building the NRQL clause.
	var durationMsClauses []string
	if len(where.Binary) > 0 {
		if durOps, ok := where.Binary["duration_ns"]; ok {
			delete(where.Binary, "duration_ns")
			for op, val := range durOps {
				nsVal, ok := parseInt64Value(val)
				if !ok {
					continue
				}
				msVal := nsVal / 1_000_000
				switch op {
				case query.Gte:
					durationMsClauses = append(durationMsClauses, fmt.Sprintf("`duration.ms` >= %d", msVal))
				case query.Gt:
					durationMsClauses = append(durationMsClauses, fmt.Sprintf("`duration.ms` > %d", msVal))
				case query.Lte:
					durationMsClauses = append(durationMsClauses, fmt.Sprintf("`duration.ms` <= %d", msVal))
				case query.Lt:
					durationMsClauses = append(durationMsClauses, fmt.Sprintf("`duration.ms` < %d", msVal))
				case query.Eq:
					durationMsClauses = append(durationMsClauses, fmt.Sprintf("`duration.ms` = %d", msVal))
				}
			}
		}
	}

	rest, err := buildNRQLWhereClause(where)
	if err != nil {
		return "", err
	}

	var allParts []string
	for _, p := range []string{serviceNameEqClause, strings.Join(durationMsClauses, " AND "), rest} {
		if p != "" {
			allParts = append(allParts, p)
		}
	}
	return strings.Join(allParts, " AND "), nil
}

// cleanBinaryWhereClause removes non-NRQL fields (trace_source, timestamp) from
// the Binary where clause and returns any extracted start/end timestamps.
func cleanBinaryWhereClause(binary query.BinaryWhereClause) (startTime, endTime int64) {
	if len(binary) == 0 {
		return 0, 0
	}

	// Remove trace_source (not applicable to New Relic)
	delete(binary, "trace_source")

	timestampOps, hasTimestamp := binary["timestamp"]
	if !hasTimestamp {
		return 0, 0
	}

	// Handle Gte/Lte operators
	if gte, ok := timestampOps[query.Gte]; ok {
		if t, err := parseTimestamp(gte); err == nil {
			startTime = t
		}
	}
	if lte, ok := timestampOps[query.Lte]; ok {
		if t, err := parseTimestamp(lte); err == nil {
			endTime = t
		}
	}

	// Handle Between operator (map with _gte/_lte keys)
	start, end := parseTimestampBetween(timestampOps[query.Between])
	if start != 0 {
		startTime = start
	}
	if end != 0 {
		endTime = end
	}

	// Remove timestamp from Binary to avoid including in WHERE clause
	delete(binary, "timestamp")
	return startTime, endTime
}

// parseTimestampBetween extracts start/end from a Between operator value.
func parseTimestampBetween(val any) (startTime, endTime int64) {
	betweenMap, ok := val.(map[string]any)
	if !ok {
		return 0, 0
	}
	if gteVal, ok := betweenMap["_gte"]; ok {
		if t, err := parseTimestamp(gteVal); err == nil {
			startTime = t
		}
	}
	if lteVal, ok := betweenMap["_lte"]; ok {
		if t, err := parseTimestamp(lteVal); err == nil {
			endTime = t
		}
	}
	return startTime, endTime
}

// getTimeRange extracts and normalizes time range from request.
// It also removes "timestamp" and "trace_source" from the Binary where clause
// so they don't leak into the NRQL WHERE clause.
func (s *NewRelicTraceSource) getTimeRange(req *TracesV3Request) (int64, int64) {
	startTime := req.StartTime
	endTime := req.EndTime

	// Extract timestamps and clean non-NRQL fields from Binary
	if binaryStart, binaryEnd := cleanBinaryWhereClause(req.QueryRequest.Where.Binary); binaryStart != 0 || binaryEnd != 0 {
		if binaryStart != 0 {
			startTime = binaryStart
		}
		if binaryEnd != 0 {
			endTime = binaryEnd
		}
	}

	// Convert from milliseconds to seconds if needed
	if startTime > 1e12 {
		startTime = startTime / 1000
	}
	if endTime > 1e12 {
		endTime = endTime / 1000
	}

	// Default to last hour if not specified
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).Unix()
	}
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return startTime, endTime
}

// parseTimestamp parses various timestamp formats
func parseTimestamp(val any) (int64, error) {
	switch v := val.(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
			if err != nil {
				return 0, fmt.Errorf("failed to parse timestamp: %s", v)
			}
		}
		return t.UnixMilli(), nil
	default:
		return 0, fmt.Errorf("unsupported timestamp type: %T", val)
	}
}

// convertNRSpansToOTelTraces converts NRQL results to OpenTelemetryTrace format
func (s *NewRelicTraceSource) convertNRSpansToOTelTraces(results []map[string]any) []common.OpenTelemetryTrace {
	traces := make([]common.OpenTelemetryTrace, 0, len(results))

	for _, r := range results {
		trace := common.OpenTelemetryTrace{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
			TraceSource:        "newrelic",
		}

		// Map core fields
		if traceId, ok := r["trace.id"].(string); ok {
			trace.TraceID = traceId
		}

		// Map span_id with multiple fallbacks (New Relic eBPF uses 'id' field)
		if spanId, ok := r["span.id"].(string); ok && spanId != "" {
			trace.SpanID = spanId
		} else if spanId, ok := r["id"].(string); ok && spanId != "" {
			trace.SpanID = spanId
		} else if spanId, ok := r["guid"].(string); ok && spanId != "" {
			trace.SpanID = spanId
		}

		if parentId, ok := r["parent.id"].(string); ok {
			trace.ParentSpanID = parentId
		}
		if name, ok := r["name"].(string); ok {
			trace.SpanName = name
			trace.Operation = name
		}
		if svc, ok := r["service.name"].(string); ok {
			trace.ServiceName = svc
			trace.WorkloadName = svc
			trace.Service = svc
		}

		// Map duration (duration.ms to nanoseconds)
		var durationNs int64
		if dur, ok := r["duration.ms"].(float64); ok {
			durationNs = int64(dur * 1_000_000) // ms to ns
			trace.DurationNs = durationNs
		}

		// Map timestamp and calculate end_time
		var startTimeMs int64
		if ts, ok := r["timestamp"].(float64); ok {
			startTimeMs = int64(ts)
			t := time.UnixMilli(startTimeMs)
			trace.Timestamp = t.Format(time.RFC3339Nano)
			trace.StartTime = trace.Timestamp
			trace.StartTimeUnixNano = fmt.Sprintf("%d", startTimeMs*1_000_000)

			// Calculate end_time from start_time + duration
			if durationNs > 0 {
				endTimeNs := (startTimeMs * 1_000_000) + durationNs
				endTimeMs := endTimeNs / 1_000_000
				endTime := time.UnixMilli(endTimeMs)
				trace.EndTime = endTime.Format(time.RFC3339Nano)
				trace.EndTimeUnixNano = fmt.Sprintf("%d", endTimeNs)
			}
		}

		// Map span kind
		if kind, ok := r["span.kind"].(string); ok {
			trace.SpanKind = mapNRSpanKind(kind)
		}

		// Map HTTP status code with multiple field fallbacks (New Relic eBPF uses different field names)
		httpStatusCode := ""
		if httpStatus, ok := r["http.statusCode"].(float64); ok {
			httpStatusCode = fmt.Sprintf("%d", int(httpStatus))
		} else if httpStatus, ok := r["http.status_code"].(float64); ok {
			httpStatusCode = fmt.Sprintf("%d", int(httpStatus))
		} else if httpStatus, ok := r["http.response.status_code"].(string); ok && httpStatus != "" {
			httpStatusCode = httpStatus
		} else if httpStatus, ok := r["http.response.status_code"].(float64); ok {
			httpStatusCode = fmt.Sprintf("%d", int(httpStatus))
		} else if httpStatus, ok := r["httpResponseStatusCode"].(string); ok && httpStatus != "" {
			httpStatusCode = httpStatus
		} else if httpStatus, ok := r["httpResponseStatusCode"].(float64); ok {
			httpStatusCode = fmt.Sprintf("%d", int(httpStatus))
		}
		trace.HTTPStatusCode = httpStatusCode

		// Map status code - comprehensive fallback chain (similar to Datadog pattern)
		statusCode := ""
		statusMessage := ""

		// Priority 1: Check for explicit error indicators
		if hasError, errorMsg := extractNRError(r); hasError {
			statusCode = "STATUS_CODE_ERROR"
			statusMessage = errorMsg
		}

		// Priority 2: Use otel.status_code if available
		if statusCode == "" {
			if status, ok := r["otel.status_code"].(string); ok && status != "" {
				statusCode = mapNRStatusCode(status)
			}
		}

		// Priority 3: Derive from HTTP status code
		if statusCode == "" && httpStatusCode != "" {
			statusCode = deriveStatusCodeFromHTTP(httpStatusCode)
		}

		// Priority 4: Check general status field
		if statusCode == "" {
			if status, ok := r["status"].(string); ok {
				if strings.ToLower(status) == "error" {
					statusCode = "STATUS_CODE_ERROR"
				} else if strings.ToLower(status) == "ok" {
					statusCode = "STATUS_CODE_OK"
				}
			}
		}

		// Default to UNSET if no status determined
		if statusCode == "" {
			statusCode = "STATUS_CODE_UNSET"
		}

		trace.StatusCode = statusCode

		// Map status message - use extracted error message or otel.status_description
		if statusMessage == "" {
			if msg, ok := r["otel.status_description"].(string); ok && msg != "" {
				statusMessage = msg
			}
		}
		trace.StatusMessage = statusMessage

		// Map error to event (similar to Datadog pattern)
		eventTimestamps, eventNames, eventAttributes := extractNRErrorEvent(r, trace.Timestamp)
		trace.EventsTimestamp = eventTimestamps
		trace.EventsName = eventNames
		trace.EventsAttributes = eventAttributes

		// Auto-detect query_type from span attributes
		trace.QueryType = detectNRQueryType(r)

		// Extract span links if available
		linksTraceID, linksSpanID, linksTraceState, linksAttributes := extractNRSpanLinks(r)
		trace.LinksTraceID = linksTraceID
		trace.LinksSpanID = linksSpanID
		trace.LinksTraceState = linksTraceState
		trace.LinksAttributes = linksAttributes

		// Map HTTP method
		if method, ok := r["http.method"].(string); ok {
			trace.SpanAttributes["http.method"] = method
		} else if method, ok := r["http.request.method"].(string); ok {
			trace.SpanAttributes["http.method"] = method
		}

		// Map HTTP URL/resource with multiple fallbacks
		httpURL := ""
		if url, ok := r["http.url"].(string); ok && url != "" {
			httpURL = url
		} else if url, ok := r["http.target"].(string); ok && url != "" {
			httpURL = url
		}
		if httpURL != "" {
			trace.Resource = httpURL
			trace.SpanAttributes["http.url"] = httpURL
		}

		// Build resource from components if http.url is not available
		if trace.Resource == "" {
			trace.Resource = buildResourceURL(r)
		}

		// Map destination fields with multiple fallbacks
		destinationName := ""
		if dest, ok := r["server.address"].(string); ok && dest != "" {
			destinationName = dest
		} else if dest, ok := r["remote.host.name"].(string); ok && dest != "" {
			destinationName = dest
		} else if dest, ok := r["remote_addr"].(string); ok && dest != "" {
			destinationName = dest
		} else if dest, ok := r["peer.hostname"].(string); ok && dest != "" {
			destinationName = dest
		} else if dest, ok := r["net.peer.name"].(string); ok && dest != "" {
			destinationName = dest
		} else if dest, ok := r["http.host"].(string); ok && dest != "" {
			destinationName = dest
		}
		trace.DestinationName = destinationName

		// Try to resolve destination workload and namespace from DNS
		if destinationName != "" {
			workload, namespace, err := common.ResolveK8sDNS(destinationName)
			if err == nil {
				trace.DestinationWorkload = workload
				trace.DestinationNamespace = namespace
			}
		}

		// Add destination port if available
		if port, ok := r["server.port"].(string); ok && port != "" {
			trace.SpanAttributes["server.port"] = port
		} else if port, ok := r["server.port"].(float64); ok {
			trace.SpanAttributes["server.port"] = fmt.Sprintf("%d", int(port))
		} else if port, ok := r["remote_port"].(string); ok && port != "" {
			trace.SpanAttributes["server.port"] = port
		}

		// Map headers from http.request.headers.* fields
		headers := buildHeadersFromAttributes(r)
		if headers != "" {
			trace.Headers = headers
		}

		// Map Kubernetes fields
		if ns, ok := r["k8s.namespace.name"].(string); ok {
			trace.WorkloadNamespace = ns
			trace.ResourceAttributes["k8s.namespace.name"] = ns
		}
		if pod, ok := r["k8s.pod.name"].(string); ok {
			trace.ResourceAttributes["k8s.pod.name"] = pod
		}
		if container, ok := r["k8s.container.name"].(string); ok {
			trace.ResourceAttributes["k8s.container.name"] = container
		}
		if node, ok := r["k8s.node.name"].(string); ok {
			trace.ResourceAttributes["k8s.node.name"] = node
		}
		if deploy, ok := r["k8s.deployment.name"].(string); ok {
			trace.ResourceAttributes["k8s.deployment.name"] = deploy
		}

		// Copy remaining attributes
		for k, v := range r {
			// Skip already processed fields
			if isProcessedField(k) {
				continue
			}
			if str, ok := v.(string); ok {
				trace.SpanAttributes[k] = str
			} else if num, ok := v.(float64); ok {
				trace.SpanAttributes[k] = fmt.Sprintf("%v", num)
			}
		}

		traces = append(traces, trace)
	}

	return traces
}

// deriveStatusCodeFromHTTP derives OpenTelemetry status code from HTTP status code
func deriveStatusCodeFromHTTP(httpStatus string) string {
	if httpStatus == "" {
		return "STATUS_CODE_UNSET"
	}

	// Parse the status code
	var statusCode int
	if _, err := fmt.Sscanf(httpStatus, "%d", &statusCode); err != nil {
		return "STATUS_CODE_UNSET"
	}

	if statusCode >= 400 {
		return "STATUS_CODE_ERROR"
	}
	return "STATUS_CODE_OK"
}

// extractNRError checks for error indicators in New Relic span data and extracts error message
func extractNRError(r map[string]any) (hasError bool, message string) {
	// Check boolean/string error flag
	if err, ok := r["error"].(bool); ok && err {
		hasError = true
	} else if err, ok := r["error"].(string); ok && strings.ToLower(err) == "true" {
		hasError = true
	}

	// Check error class/type presence (indicates an error occurred)
	if errClass, ok := r["error.class"].(string); ok && errClass != "" {
		hasError = true
	}
	if errType, ok := r["error.type"].(string); ok && errType != "" {
		hasError = true
	}
	if nrErrClass, ok := r["nr.errorClass"].(string); ok && nrErrClass != "" {
		hasError = true
	}

	// Extract error message from various fields (in priority order)
	if msg, ok := r["error.message"].(string); ok && msg != "" {
		message = msg
	} else if msg, ok := r["nr.errorMessage"].(string); ok && msg != "" {
		message = msg
	} else if msg, ok := r["error.expected"].(string); ok && msg != "" {
		message = msg
	} else if msg, ok := r["otel.status_description"].(string); ok && msg != "" {
		message = msg
	} else if errClass, ok := r["error.class"].(string); ok && errClass != "" {
		// Use error class as message if no explicit message
		message = errClass
	} else if errType, ok := r["error.type"].(string); ok && errType != "" {
		// Use error type as message if no explicit message
		message = errType
	}

	return hasError, message
}

// extractNRErrorEvent converts error data to OpenTelemetry event format (similar to Datadog pattern)
func extractNRErrorEvent(r map[string]any, timestamp string) ([]string, []string, []map[string]string) {
	hasError, _ := extractNRError(r)
	if !hasError {
		return []string{}, []string{}, []map[string]string{}
	}

	eventTimestamps := []string{timestamp}
	eventNames := []string{"exception"}
	eventAttrs := make(map[string]string)

	// Map error type/class
	if errType, ok := r["error.type"].(string); ok && errType != "" {
		eventAttrs["exception.type"] = errType
	} else if errClass, ok := r["error.class"].(string); ok && errClass != "" {
		eventAttrs["exception.type"] = errClass
	} else if nrErrClass, ok := r["nr.errorClass"].(string); ok && nrErrClass != "" {
		eventAttrs["exception.type"] = nrErrClass
	}

	// Map error message
	if msg, ok := r["error.message"].(string); ok && msg != "" {
		eventAttrs["exception.message"] = msg
	} else if msg, ok := r["nr.errorMessage"].(string); ok && msg != "" {
		eventAttrs["exception.message"] = msg
	} else if msg, ok := r["otel.status_description"].(string); ok && msg != "" {
		eventAttrs["exception.message"] = msg
	}

	// Map error stack trace
	if stack, ok := r["error.stack"].(string); ok && stack != "" {
		eventAttrs["exception.stacktrace"] = stack
	} else if stack, ok := r["error.stack_trace"].(string); ok && stack != "" {
		eventAttrs["exception.stacktrace"] = stack
	}

	// Map additional error attributes
	if errExpected, ok := r["error.expected"].(string); ok && errExpected != "" {
		eventAttrs["error.expected"] = errExpected
	}
	if errGroup, ok := r["error.group"].(string); ok && errGroup != "" {
		eventAttrs["error.group"] = errGroup
	}

	return eventTimestamps, eventNames, []map[string]string{eventAttrs}
}

// detectNRQueryType auto-detects the query type from span attributes
func detectNRQueryType(r map[string]any) string {
	// Check category field first (New Relic specific)
	if category, ok := r["category"].(string); ok && category != "" {
		switch strings.ToLower(category) {
		case "http":
			return "HTTP"
		case "datastore", "database", "db":
			return "DB"
		case "generic":
			return "INTERNAL"
		}
	}

	// Check span.kind for type hints
	if kind, ok := r["span.kind"].(string); ok {
		switch strings.ToLower(kind) {
		case "client":
			// Could be HTTP, DB, or gRPC - need to check other fields
		case "server":
			return "HTTP"
		case "producer", "consumer":
			return "MESSAGING"
		}
	}

	// Check for HTTP indicators
	if _, ok := r["http.method"].(string); ok {
		return "HTTP"
	}
	if _, ok := r["http.request.method"].(string); ok {
		return "HTTP"
	}
	if _, ok := r["http.url"].(string); ok {
		return "HTTP"
	}
	if _, ok := r["http.statusCode"]; ok {
		return "HTTP"
	}
	if _, ok := r["http.response.status_code"]; ok {
		return "HTTP"
	}

	// Check for database indicators
	if _, ok := r["db.system"].(string); ok {
		return "DB"
	}
	if _, ok := r["db.statement"].(string); ok {
		return "DB"
	}
	if _, ok := r["db.operation"].(string); ok {
		return "DB"
	}
	if _, ok := r["database.name"].(string); ok {
		return "DB"
	}

	// Check for gRPC/RPC indicators
	if _, ok := r["rpc.system"].(string); ok {
		return "GRPC"
	}
	if _, ok := r["rpc.service"].(string); ok {
		return "GRPC"
	}
	if _, ok := r["rpc.method"].(string); ok {
		return "GRPC"
	}

	// Check for messaging indicators
	if _, ok := r["messaging.system"].(string); ok {
		return "MESSAGING"
	}
	if _, ok := r["messaging.destination"].(string); ok {
		return "MESSAGING"
	}

	// Check span name patterns
	if name, ok := r["name"].(string); ok {
		nameLower := strings.ToLower(name)
		if strings.HasPrefix(nameLower, "http") || strings.Contains(nameLower, "external/") {
			return "HTTP"
		}
		if strings.Contains(nameLower, "database") || strings.Contains(nameLower, "datastore") ||
			strings.Contains(nameLower, "mysql") || strings.Contains(nameLower, "postgres") ||
			strings.Contains(nameLower, "redis") || strings.Contains(nameLower, "mongodb") {
			return "DB"
		}
		if strings.Contains(nameLower, "grpc") || strings.Contains(nameLower, "rpc") {
			return "GRPC"
		}
		if strings.Contains(nameLower, "kafka") || strings.Contains(nameLower, "rabbitmq") ||
			strings.Contains(nameLower, "queue") || strings.Contains(nameLower, "message") {
			return "MESSAGING"
		}
	}

	return ""
}

// extractNRSpanLinks extracts span links from New Relic span data
func extractNRSpanLinks(r map[string]any) ([]string, []string, []string, []map[string]string) {
	var traceIDs, spanIDs, traceStates []string
	var attributes []map[string]string

	// Check for link arrays (New Relic may store links as arrays)
	if linkTraceIDs, ok := r["link.traceId"].([]any); ok {
		for _, id := range linkTraceIDs {
			if str, ok := id.(string); ok {
				traceIDs = append(traceIDs, str)
			}
		}
	}
	if linkSpanIDs, ok := r["link.spanId"].([]any); ok {
		for _, id := range linkSpanIDs {
			if str, ok := id.(string); ok {
				spanIDs = append(spanIDs, str)
			}
		}
	}

	// Check for single link values
	if linkTraceID, ok := r["link.traceId"].(string); ok && linkTraceID != "" {
		traceIDs = append(traceIDs, linkTraceID)
	}
	if linkSpanID, ok := r["link.spanId"].(string); ok && linkSpanID != "" {
		spanIDs = append(spanIDs, linkSpanID)
	}

	// Check for span_link prefix (alternative naming)
	if linkTraceID, ok := r["span_link.trace_id"].(string); ok && linkTraceID != "" {
		traceIDs = append(traceIDs, linkTraceID)
	}
	if linkSpanID, ok := r["span_link.span_id"].(string); ok && linkSpanID != "" {
		spanIDs = append(spanIDs, linkSpanID)
	}

	// Ensure arrays have consistent length
	maxLen := len(traceIDs)
	if len(spanIDs) > maxLen {
		maxLen = len(spanIDs)
	}

	// Pad arrays to match length
	for len(traceIDs) < maxLen {
		traceIDs = append(traceIDs, "")
	}
	for len(spanIDs) < maxLen {
		spanIDs = append(spanIDs, "")
	}
	for len(traceStates) < maxLen {
		traceStates = append(traceStates, "")
	}
	for len(attributes) < maxLen {
		attributes = append(attributes, make(map[string]string))
	}

	return traceIDs, spanIDs, traceStates, attributes
}

// buildResourceURL builds a resource URL from available fields
func buildResourceURL(r map[string]any) string {
	// Try to build URL from components
	scheme := "http"
	if s, ok := r["url.scheme"].(string); ok && s != "" {
		scheme = s
	}

	host := ""
	if h, ok := r["http.host"].(string); ok && h != "" {
		host = h
	} else if h, ok := r["server.address"].(string); ok && h != "" {
		host = h
		// Add port if available and not standard
		if port, ok := r["server.port"].(string); ok && port != "" && port != "80" && port != "443" {
			host = fmt.Sprintf("%s:%s", host, port)
		} else if port, ok := r["server.port"].(float64); ok && int(port) != 80 && int(port) != 443 {
			host = fmt.Sprintf("%s:%d", host, int(port))
		}
	}

	path := ""
	if p, ok := r["http.target"].(string); ok && p != "" {
		path = p
	} else if p, ok := r["http.route"].(string); ok && p != "" {
		path = p
	} else if p, ok := r["url.path"].(string); ok && p != "" {
		path = p
	}

	if host == "" && path == "" {
		return ""
	}

	if host != "" && path != "" {
		return fmt.Sprintf("%s://%s%s", scheme, host, path)
	} else if host != "" {
		return host
	}
	return path
}

// buildHeadersFromAttributes extracts HTTP headers from span attributes
func buildHeadersFromAttributes(r map[string]any) string {
	headers := make(map[string]string)

	for k, v := range r {
		// Match http.request.headers.* pattern
		if strings.HasPrefix(k, "http.request.headers.") {
			headerName := strings.TrimPrefix(k, "http.request.headers.")
			if str, ok := v.(string); ok {
				headers[headerName] = str
			}
		}
	}

	if len(headers) == 0 {
		return ""
	}

	// Convert to string representation
	var parts []string
	for k, v := range headers {
		parts = append(parts, fmt.Sprintf("%s: %s", k, v))
	}
	return strings.Join(parts, "\n")
}

// convertNRSpansToOTelHeatmap converts NRQL results to OpenTelemetryTraceHeatMap format
func (s *NewRelicTraceSource) convertNRSpansToOTelHeatmap(results []map[string]any) []common.OpenTelemetryTraceHeatMap {
	heatmaps := make([]common.OpenTelemetryTraceHeatMap, 0, len(results))

	for _, r := range results {
		hm := common.OpenTelemetryTraceHeatMap{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
			EventsAttributes:   []map[string]string{},
		}

		// Map core fields
		if traceId, ok := r["trace.id"].(string); ok {
			hm.TraceID = traceId
		}
		if spanId, ok := r["span.id"].(string); ok {
			hm.SpanID = spanId
		}
		if name, ok := r["name"].(string); ok {
			hm.SpanName = name
		}
		if svc, ok := r["service.name"].(string); ok {
			hm.ServiceName = svc
		}

		// Map duration
		if dur, ok := r["duration.ms"].(float64); ok {
			hm.DurationNs = int64(dur * 1_000_000)
		}

		// Map timestamp
		if ts, ok := r["timestamp"].(float64); ok {
			hm.Timestamp = time.UnixMilli(int64(ts)).Format(time.RFC3339Nano)
		}

		// Map status
		if status, ok := r["otel.status_code"].(string); ok {
			hm.StatusCode = mapNRStatusCode(status)
		}

		// Copy attributes
		for k, v := range r {
			if isProcessedField(k) {
				continue
			}
			if str, ok := v.(string); ok {
				hm.SpanAttributes[k] = str
			}
		}

		heatmaps = append(heatmaps, hm)
	}

	return heatmaps
}

// convertNRGroupedTracesToTraceGroupingValues converts grouped query results
func (s *NewRelicTraceSource) convertNRGroupedTracesToTraceGroupingValues(results []map[string]any) []TraceGroupingValues {
	groupedTraces := make([]TraceGroupingValues, 0, len(results))

	for _, r := range results {
		group := TraceGroupingValues{}

		// Extract facet array - New Relic returns facets as an array when multiple FACET fields are used
		// Order matches FACET clause: service.name, k8s.namespace.name, name, http.response.status_code
		if facets, ok := r["facet"].([]any); ok && len(facets) > 0 {
			// Extract available facet values based on array length
			// Handles partial arrays gracefully (e.g., if query has fewer FACETs)
			if len(facets) > 0 {
				if svc, ok := facets[0].(string); ok {
					group.WorkloadName = svc
				}
			}
			if len(facets) > 1 {
				if ns, ok := facets[1].(string); ok {
					group.WorkloadNamespace = ns
				}
			}
			if len(facets) > 2 {
				if name, ok := facets[2].(string); ok {
					group.SpanName = name
				}
			}
			if len(facets) > 3 {
				// Status code can be string ("200") or numeric (200.0)
				if statusCode, ok := facets[3].(string); ok {
					group.HTTPStatusCode = statusCode
				} else if statusCode, ok := facets[3].(float64); ok {
					group.HTTPStatusCode = fmt.Sprintf("%d", int(statusCode))
				}
				// nil status codes (missing http.response.status_code) result in empty string - correct behavior
			}
		} else if facetStr, ok := r["facet"].(string); ok {
			// Single FACET case - New Relic returns string instead of array
			// Also includes direct field keys like "service.name"
			group.WorkloadName = facetStr
			// Try to get additional fields from direct keys (available in single FACET responses)
			if ns, ok := r["k8s.namespace.name"].(string); ok {
				group.WorkloadNamespace = ns
			}
			if name, ok := r["name"].(string); ok {
				group.SpanName = name
			}
			if statusCode, ok := r["http.response.status_code"].(float64); ok {
				group.HTTPStatusCode = fmt.Sprintf("%d", int(statusCode))
			} else if statusCode, ok := r["http.response.status_code"].(string); ok {
				group.HTTPStatusCode = statusCode
			}
		} else {
			// Direct field access fallback (backward compatibility for unexpected formats)
			if svc, ok := r["service.name"].(string); ok {
				group.WorkloadName = svc
			}
			if ns, ok := r["k8s.namespace.name"].(string); ok {
				group.WorkloadNamespace = ns
			}
			if name, ok := r["name"].(string); ok {
				group.SpanName = name
			}
			if statusCode, ok := r["http.response.status_code"].(float64); ok {
				group.HTTPStatusCode = fmt.Sprintf("%d", int(statusCode))
			} else if statusCode, ok := r["http.response.status_code"].(string); ok {
				group.HTTPStatusCode = statusCode
			}
		}

		// Map resource - prefer resource_url, fallback to span name
		if url, ok := r["resource_url"].(string); ok && url != "" {
			group.Resource = url
		} else {
			group.Resource = group.SpanName
		}

		// Map aggregations
		if count, ok := r["count"].(float64); ok {
			group.Count = int(count)
		}
		if errorCount, ok := r["error_count"].(float64); ok {
			group.ErrorCount = int(errorCount)
		}

		// Handle percentile results - New Relic returns percentiles as nested objects {"95": value}
		if p95Map, ok := r["p95_latency"].(map[string]any); ok {
			if p95, ok := p95Map["95"].(float64); ok {
				group.P95Latency = int64(p95 * 1_000_000) // ms to ns
			}
		} else if p95, ok := r["p95_latency"].(float64); ok {
			group.P95Latency = int64(p95 * 1_000_000) // ms to ns
		}

		if p99Map, ok := r["p99_latency"].(map[string]any); ok {
			if p99, ok := p99Map["99"].(float64); ok {
				group.P99Latency = int64(p99 * 1_000_000) // ms to ns
			}
		} else if p99, ok := r["p99_latency"].(float64); ok {
			group.P99Latency = int64(p99 * 1_000_000) // ms to ns
		}

		if maxLatency, ok := r["max_latency"].(float64); ok {
			group.MaxLatency = int64(maxLatency * 1_000_000) // ms to ns
		}

		// Map average duration for the Duration column
		if avgDuration, ok := r["avg_duration"].(float64); ok {
			group.DurationNS = int64(avgDuration * 1_000_000) // ms to ns
		}

		// Note: DestinationWorkloadName, DestinationWorkloadNamespace, and
		// DestinationWorkloadZone cannot be reliably extracted in grouped queries
		// due to NRQL FACET limitations and the need for K8s DNS resolution.

		groupedTraces = append(groupedTraces, group)
	}

	return groupedTraces
}

// mapNRSpanKind maps New Relic span kinds to OpenTelemetry format
func mapNRSpanKind(kind string) string {
	switch strings.ToLower(kind) {
	case "client":
		return "SPAN_KIND_CLIENT"
	case "server":
		return "SPAN_KIND_SERVER"
	case "producer":
		return "SPAN_KIND_PRODUCER"
	case "consumer":
		return "SPAN_KIND_CONSUMER"
	case "internal":
		return "SPAN_KIND_INTERNAL"
	default:
		return "SPAN_KIND_UNSPECIFIED"
	}
}

// mapNRStatusCode maps New Relic status codes to OpenTelemetry format
func mapNRStatusCode(status string) string {
	switch strings.ToUpper(status) {
	case "ERROR":
		return "STATUS_CODE_ERROR"
	case "OK":
		return "STATUS_CODE_OK"
	default:
		return "STATUS_CODE_UNSET"
	}
}

// isProcessedField checks if a field has already been processed
func isProcessedField(field string) bool {
	processedFields := map[string]bool{
		"trace.id":                  true,
		"span.id":                   true,
		"id":                        true,
		"guid":                      true,
		"parent.id":                 true,
		"name":                      true,
		"service.name":              true,
		"duration.ms":               true,
		"timestamp":                 true,
		"span.kind":                 true,
		"otel.status_code":          true,
		"otel.status_description":   true,
		"http.statusCode":           true,
		"http.status_code":          true,
		"http.response.status_code": true,
		"httpResponseStatusCode":    true,
		"http.method":               true,
		"http.request.method":       true,
		"http.url":                  true,
		"http.target":               true,
		"http.host":                 true,
		"url.scheme":                true,
		"url.path":                  true,
		"http.route":                true,
		"server.address":            true,
		"server.port":               true,
		"remote.host.name":          true,
		"remote_addr":               true,
		"remote_port":               true,
		"peer.hostname":             true,
		"net.peer.name":             true,
		"k8s.namespace.name":        true,
		"k8s.pod.name":              true,
		"k8s.container.name":        true,
		"k8s.node.name":             true,
		"k8s.deployment.name":       true,
		// Error-related fields
		"error":           true,
		"error.class":     true,
		"error.type":      true,
		"error.message":   true,
		"error.expected":  true,
		"nr.errorClass":   true,
		"nr.errorMessage": true,
		"status":          true,
		"span.statusCode": true,
	}
	// Also check for header fields
	if strings.HasPrefix(field, "http.request.headers.") {
		return true
	}
	return processedFields[field]
}
