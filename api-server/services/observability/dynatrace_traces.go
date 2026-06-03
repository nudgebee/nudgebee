package observability

import (
	"errors"
	"fmt"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// DynatraceTraceSource implements TraceSource interface for Dynatrace Grail
type DynatraceTraceSource struct{}

const (
	dtConfigsErrFormat     = "failed to get Dynatrace configs: %w"
	dqlHTTPStatusCodeField = "`http.response.status_code`"
)

// dynatraceTraceLabelMapping maps NudgeBee trace field names to Dynatrace Grail span field names.
// Field names confirmed against actual fetch spans DQL response.
var dynatraceTraceLabelMapping = map[string]string{
	"workload_name":                  "k8s.workload.name",
	"workload_namespace":             "k8s.namespace.name",
	"destination_workload_name":      "dt.kubernetes.workload.name",
	"destination_workload_namespace": "k8s.namespace.name",
	"http_status_code":               "http.response.status_code",
	"span_name":                      "span.name",
	"resource":                       "url.path",
	"status_code":                    "dt.failure_detection.verdict",
	"trace_id":                       "trace.id",
	"span_id":                        "span.id",
	"parent_id":                      "span.parent_id",
	"duration_ns":                    "duration",
}

// dynatraceSpanUIDFields lists Grail span fields whose type is uid,
// requiring touid() wrapping for equality comparisons in DQL.
var dynatraceSpanUIDFields = map[string]bool{
	"trace.id": true,
}

func (s *DynatraceTraceSource) GetLabelMapping() map[string]string {
	return dynatraceTraceLabelMapping
}

func (s *DynatraceTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_gt", "_lt", "_gte", "_lte", "_like", "_nlike", "_ilike", "_icontains", "_contains", "_regex", "_nregex"}
}

// QueryTraces fetches spans from Dynatrace Grail. If a trace_id filter is present in the
// where clause, fetches all spans for that specific trace; otherwise searches across spans.
func (s *DynatraceTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("DynatraceTraceSource.QueryTraces: failed to get configs", "error", err)
		return nil, fmt.Errorf(dtConfigsErrFormat, err)
	}

	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	dql, err := s.buildSpanDQL(req, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to build DQL: %w", err)
	}
	ctx.GetLogger().Info("DynatraceTraceSource.QueryTraces", "dql", dql)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		return nil, fmt.Errorf("dynatrace traces query failed: %w", err)
	}

	return s.convertSpanRecords(result.Records), nil
}

// buildSpanDQL constructs the DQL fetch spans statement.
// Priority: (1) trace_id equality filter with touid(), (2) raw req.Query, (3) structured QueryRequest.Where.
func (s *DynatraceTraceSource) buildSpanDQL(req TracesV3Request, from, to string) (string, error) {
	dql := fmt.Sprintf(`fetch spans, from: "%s", to: "%s"`, from, to)
	if traceID := s.extractTraceID(req); traceID != "" {
		// trace.id is a uid type in Grail — must use touid() for equality comparison.
		dql += fmt.Sprintf(` | filter trace.id == touid("%s")`, traceID)
		dql += " | sort start_time asc"
		return dql, nil
	}

	var filterExpr string
	if req.Query != "" {
		filterExpr = req.Query
	} else if hasWhereConditions(req.QueryRequest.Where) {
		var err error
		filterExpr, err = buildDQLWhereClauseWithMapping(req.QueryRequest.Where, dynatraceTraceLabelMapping, dynatraceSpanUIDFields)
		if err != nil {
			return "", fmt.Errorf("failed to build DQL span filter: %w", err)
		}
	}

	if filterExpr != "" {
		dql += " | filter " + filterExpr
	}
	dql += " | sort start_time desc | limit 100"
	return dql, nil
}

// GetQuery returns the Dynatrace Grail trace query string for the request.
func (s *DynatraceTraceSource) GetQuery(_ *security.RequestContext, req TracesV3Request) (string, error) {
	return req.Query, nil
}

// CountTraces returns the count of unique trace IDs matching the request.
func (s *DynatraceTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	traces, err := s.QueryTraces(ctx, req)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	seen := make(map[string]bool)
	for _, t := range traces {
		seen[t.TraceID] = true
	}
	return common.OpenTelemetryTraceCount{Count: len(seen)}, nil
}

// GetLabelValues samples recent spans and returns distinct values for the given label.
func (s *DynatraceTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf(dtConfigsErrFormat, err)
	}

	labelName := req.Label
	if mapped, ok := dynatraceTraceLabelMapping[labelName]; ok {
		labelName = mapped
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)
	dql := fmt.Sprintf(`fetch spans, from: "%s", to: "%s" | sort start_time desc | limit 500`, from, to)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("dynatrace traces label values query failed: %w", err)
	}

	values := s.collectLabelValues(result.Records, labelName, 50)
	return common.OpenTelemetryTraceLabelValues{
		Label:  req.Label,
		Values: values,
	}, nil
}

// QueryGroupedTraces returns spans summarized by workload, namespace, operation name, and HTTP status code.
// Uses a DQL server-side summarize query for accurate percentile calculation, avoiding the 100-span
// fetch limit of the previous client-side approach.
func (s *DynatraceTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("DynatraceTraceSource.QueryGroupedTraces: failed to get configs", "error", err)
		return nil, fmt.Errorf(dtConfigsErrFormat, err)
	}

	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	dql, err := s.buildGroupedSpansDQL(req, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to build grouped spans DQL: %w", err)
	}
	ctx.GetLogger().Info("DynatraceTraceSource.QueryGroupedTraces", "dql", dql)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		ctx.GetLogger().Error("DynatraceTraceSource.QueryGroupedTraces: DQL query failed", "error", err)
		if errors.Is(err, integrations.ErrDQLQueryTimeout) {
			return nil, fmt.Errorf(
				"trace group query timed out — the selected time range contains too many spans. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return nil, fmt.Errorf("dynatrace grouped traces query failed: %w", err)
	}

	return s.convertDQLGroupedToTraceGroupingValues(result.Records), nil
}

// QueryGroupedTracesCount returns the exact number of distinct span groups using a dedicated
// DQL query — a chained summarize that groups spans first and then counts the resulting rows.
// This avoids the group limit applied by QueryGroupedTraces and matches the pattern used by
// New Relic's uniqueCount(concat(...)) approach.
func (s *DynatraceTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("DynatraceTraceSource.QueryGroupedTracesCount: failed to get configs", "error", err)
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf(dtConfigsErrFormat, err)
	}

	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	dql, err := s.buildGroupedTracesCountDQL(req, from, to)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to build grouped traces count DQL: %w", err)
	}
	ctx.GetLogger().Info("DynatraceTraceSource.QueryGroupedTracesCount", "dql", dql)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		ctx.GetLogger().Error("DynatraceTraceSource.QueryGroupedTracesCount: DQL query failed", "error", err)
		if errors.Is(err, integrations.ErrDQLQueryTimeout) {
			return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf(
				"trace group count query timed out — the selected time range contains too many spans. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("dynatrace grouped traces count query failed: %w", err)
	}

	count := 0
	if len(result.Records) > 0 {
		count = int(parseDQLLong(result.Records[0]["count"]))
	}
	return common.OpenTelemetryTraceGroupCount{Count: count}, nil
}

// QueryTracesHeatmap returns per-span data for heatmap visualization of a specific trace.
func (s *DynatraceTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf(dtConfigsErrFormat, err)
	}

	// No explicit time range — Grail will use its defaultTimeframe (configured on the tenant).
	// trace.id is a uid type in Grail — must use touid() for equality comparison.
	dql := fmt.Sprintf(`fetch spans | filter trace.id == touid("%s") | sort start_time asc`, req.TraceId)
	ctx.GetLogger().Info("DynatraceTraceSource.QueryTracesHeatmap", "dql", dql)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		return nil, fmt.Errorf("dynatrace heatmap query failed: %w", err)
	}
	ctx.GetLogger().Info("DynatraceTraceSource.QueryTracesHeatmap raw result", "record_count", len(result.Records))

	spans := s.convertSpanRecords(result.Records)
	ctx.GetLogger().Info("DynatraceTraceSource.QueryTracesHeatmap converted spans", "span_count", len(spans))
	heatmap := make([]common.OpenTelemetryTraceHeatMap, 0, len(spans))
	for _, span := range spans {
		heatmap = append(heatmap, common.OpenTelemetryTraceHeatMap{
			TraceID:            span.TraceID,
			SpanID:             span.SpanID,
			SpanName:           span.SpanName,
			ServiceName:        span.ServiceName,
			StatusCode:         span.StatusCode,
			DurationNs:         span.DurationNs,
			Timestamp:          span.Timestamp,
			SpanAttributes:     span.SpanAttributes,
			ResourceAttributes: span.ResourceAttributes,
		})
	}
	return heatmap, nil
}

// --- Private helpers ---

// convertSpanRecords normalizes Dynatrace Grail span records to common.OpenTelemetryTrace format.
func (s *DynatraceTraceSource) convertSpanRecords(records []map[string]any) []common.OpenTelemetryTrace {
	result := make([]common.OpenTelemetryTrace, 0, len(records))
	for _, record := range records {
		span := s.recordToOTelSpan(record)
		if span.SpanID == "" {
			continue
		}
		result = append(result, span)
	}
	return result
}

// recordToOTelSpan converts a single Grail span record to common.OpenTelemetryTrace.
//
// Field names confirmed against actual Grail fetch spans response:
//
//	trace.id                  → TraceID
//	span.id                   → SpanID
//	span.parent_id            → ParentSpanID
//	span.name                 → SpanName
//	span.kind                 → SpanKind  (lowercase: "server", "client", etc.)
//	k8s.workload.name         → ServiceName, WorkloadName
//	k8s.namespace.name        → WorkloadNamespace
//	peer.service/server.addr  → DestinationWorkload, DestinationName
//	url.path                  → Resource
//	http.response.status_code → StatusCode, HTTPStatusCode
//	start_time                → Timestamp
//	duration                  → DurationNs (nanoseconds, returned as string)
func (s *DynatraceTraceSource) recordToOTelSpan(record map[string]any) common.OpenTelemetryTrace {
	spanID := s.firstNonEmpty(record, "span.id", "span_id")
	traceID := s.firstNonEmpty(record, "trace.id", "trace_id")
	parentSpanID := s.firstNonEmpty(record, "span.parent_id", "parent_span_id")
	spanName := s.firstNonEmpty(record, "span.name", "name")
	// Prefer HTTP status code; fall back to Dynatrace failure verdict
	statusCode := s.firstNonEmpty(record, "http.response.status_code", "dt.failure_detection.verdict", "status_code")
	// Prefer k8s workload name as service identifier
	serviceName := s.firstNonEmpty(record, "k8s.workload.name", "dt.kubernetes.workload.name", "service.name", "process.executable.name")

	// duration is returned as a string of nanoseconds (e.g. "4756000")
	durationNs := s.parseDurationNs(record["duration"])

	// Collect all string fields as span attributes
	attrs := make(map[string]string, len(record))
	for k, v := range record {
		if str, ok := v.(string); ok {
			attrs[k] = str
		}
	}

	workloadNamespace := grailStr(record, "k8s.namespace.name")
	resource := grailStr(record, "url.path")

	peerService := grailStr(record, "peer.service")
	serverAddr := strings.TrimSuffix(grailStr(record, "server.address"), ".")

	// Fallback chain for destination namespace:
	// 1. peer.namespace  (OTel standard — rarely set in Dynatrace but check first)
	// 2. net.peer.name   (some instrumentation libraries set this)
	// 3. Parse server.address for K8s service DNS (<svc>.<ns>.svc.cluster.local)
	// 4. Parse url.full host for the same K8s DNS pattern
	// Fallback chain for destination namespace:
	// 1. peer.namespace  (OTel standard — rarely set in Dynatrace but check first)
	// 2-4. Parse K8s service DNS (<svc>.<ns>.svc.cluster.local) from:
	//      net.peer.name → server.address → url.full host (first match wins)
	destNamespace := grailStr(record, "peer.namespace")
	var k8sService string
	if destNamespace == "" {
		for _, source := range []string{
			grailStr(record, "net.peer.name"),
			serverAddr,
			extractHostFromURL(grailStr(record, "url.full")),
		} {
			if source == "" {
				continue
			}
			if svc, ns := parseK8sDNS(source); ns != "" {
				destNamespace = ns
				k8sService = svc
				break
			}
		}
	}

	// Fallback chain for destination workload name:
	// 1. peer.service  (OTel standard)
	// 2. Service name part from K8s DNS parsed above
	// 3. Full server.address (verbose but better than empty)
	destWorkload := peerService
	if destWorkload == "" && k8sService != "" {
		destWorkload = k8sService
	}
	if destWorkload == "" {
		destWorkload = serverAddr
	}

	otelSpan := common.OpenTelemetryTrace{
		TraceID:              traceID,
		SpanID:               spanID,
		ParentSpanID:         parentSpanID,
		ServiceName:          serviceName,
		SpanName:             spanName,
		SpanKind:             s.mapSpanKind(grailStr(record, "span.kind")),
		StatusCode:           statusCode,
		HTTPStatusCode:       grailStr(record, "http.response.status_code"),
		DurationNs:           durationNs,
		SpanAttributes:       attrs,
		ResourceAttributes:   map[string]string{"service.name": serviceName},
		WorkloadName:         serviceName,
		WorkloadNamespace:    workloadNamespace,
		Resource:             resource,
		DestinationWorkload:  destWorkload,
		DestinationNamespace: destNamespace,
		DestinationName:      destWorkload,
	}
	// Grail returns span start time as "start_time", not "timestamp"
	if ts := s.firstNonEmpty(record, "start_time", "timestamp"); ts != "" {
		otelSpan.Timestamp = ts
	}
	return otelSpan
}

// parseDurationNs extracts a nanosecond duration from a Grail record value.
// Grail returns duration as a string (e.g. "4756000") but may also return float64.
func (s *DynatraceTraceSource) parseDurationNs(raw any) int64 {
	if raw == nil {
		return 0
	}
	var ns int64
	switch v := raw.(type) {
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			ns = parsed
		}
	case float64:
		ns = int64(v)
	case int64:
		ns = v
	}
	if ns < 0 {
		return 0
	}
	return ns
}

// firstNonEmpty returns the first non-empty string value found for the given keys.
func (s *DynatraceTraceSource) firstNonEmpty(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := grailStr(record, key); val != "" {
			return val
		}
	}
	return ""
}

// dqlGroupByClause is the shared DQL `by` clause used by both the grouped spans query and
// the count query. It defines the four dimensions that constitute a "trace group":
//   - workload: k8s.workload.name → service.name → process.executable.name (fallback chain)
//   - namespace: k8s.namespace.name
//   - span_name: span.name
//   - http_status: http.response.status_code (null for non-HTTP spans)
//
// All three workload candidates are guarded by isNotNull so the alias stays null (not the
// string "null") when none of the fields are set on a span.
const dqlGroupByClause = " by: {" +
	"workload = if(isNotNull(`k8s.workload.name`), `k8s.workload.name`, else: if(isNotNull(`service.name`), `service.name`, else: if(isNotNull(`process.executable.name`), `process.executable.name`, else: null)))," +
	" namespace = `k8s.namespace.name`," +
	" span_name = `span.name`," +
	" http_status = " + dqlHTTPStatusCodeField +
	"}"

// buildGroupedSpansFilterExpr resolves the DQL filter expression to inject into a grouped
// query. It returns an empty string when no filter should be applied.
//
// If req.Query is a complete DQL statement (starts with "fetch"), it cannot be used as a
// filter expression inside the grouped query's fixed fetch+summarize structure, so it is
// silently skipped in favour of req.QueryRequest.Where. This matches the pattern in
// buildLogDQL and prevents invalid DQL like "| filter fetch spans | ...".
func (s *DynatraceTraceSource) buildGroupedSpansFilterExpr(req TracesV3Request) (string, error) {
	if req.Query != "" && !strings.HasPrefix(strings.TrimSpace(req.Query), "fetch ") {
		return req.Query, nil
	}
	if hasWhereConditions(req.QueryRequest.Where) {
		expr, err := buildDQLWhereClauseWithMapping(req.QueryRequest.Where, dynatraceTraceLabelMapping, dynatraceSpanUIDFields)
		if err != nil {
			return "", fmt.Errorf("failed to build DQL span filter: %w", err)
		}
		return expr, nil
	}
	return "", nil
}

// buildGroupedSpansDQL constructs a DQL fetch spans + summarize query for trace grouping.
// Groups by workload name (with fallback chain), namespace, span name, and HTTP status code.
// Computes accurate server-side aggregations including p95/p99 percentiles.
//
// Error detection covers:
//   - Dynatrace failure verdict: dt.failure_detection.verdict == "failure"
//   - HTTP 4xx/5xx:              toLong(http.response.status_code) >= 400
//     (guarded with isNotNull so non-HTTP spans that lack the field are not misclassified)
//
// Note: DQL returns "long" and "duration" typed aggregation results as JSON strings,
// not JSON numbers — see parseDQLLong for the corresponding Go parsing.
func (s *DynatraceTraceSource) buildGroupedSpansDQL(req TracesV3Request, from, to string) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, `fetch spans, from: "%s", to: "%s"`, from, to)

	filterExpr, err := s.buildGroupedSpansFilterExpr(req)
	if err != nil {
		return "", err
	}
	if filterExpr != "" {
		sb.WriteString(" | filter " + filterExpr)
	}

	sb.WriteString(" | summarize" +
		" count = count()," +
		" error_count = countIf(`dt.failure_detection.verdict` == \"failure\"" +
		" or (isNotNull(" + dqlHTTPStatusCodeField + ") and toLong(" + dqlHTTPStatusCodeField + ") >= 400))," +
		" avg_duration = avg(duration)," +
		" p95_duration = percentile(duration, 95)," +
		" p99_duration = percentile(duration, 99)," +
		" max_duration = max(duration)," +
		" resource_url = takeFirst(`url.path`)," +
		dqlGroupByClause)

	limit := req.QueryRequest.Limit
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	fmt.Fprintf(&sb, " | limit %d", limit)

	return sb.String(), nil
}

// buildGroupedTracesCountDQL constructs a DQL query that returns the exact number of distinct
// span groups — without a row limit — using a chained summarize pattern:
//
//	inner: group spans by {workload, namespace, span_name, http_status}  (no limit)
//	outer: count the resulting group rows → one record {"count": N}
//
// This mirrors New Relic's uniqueCount(concat(...)) approach and avoids the group-limit
// truncation that would occur if QueryGroupedTraces (limit 50) were used for counting.
func (s *DynatraceTraceSource) buildGroupedTracesCountDQL(req TracesV3Request, from, to string) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, `fetch spans, from: "%s", to: "%s"`, from, to)

	filterExpr, err := s.buildGroupedSpansFilterExpr(req)
	if err != nil {
		return "", err
	}
	if filterExpr != "" {
		sb.WriteString(" | filter " + filterExpr)
	}

	// Inner summarize: one row per distinct group. count = count() satisfies DQL's requirement
	// for at least one aggregation function; the value is discarded — only the row count matters.
	sb.WriteString(` | summarize count = count(),` + dqlGroupByClause)

	// Outer summarize: count the group rows produced above.
	sb.WriteString(` | summarize count = count()`)

	return sb.String(), nil
}

// convertDQLGroupedToTraceGroupingValues maps DQL summarize records to TraceGroupingValues.
// Field names match the aliases defined in buildGroupedSpansDQL.
// All latency values are already in nanoseconds (Grail span duration unit).
// DQL returns aggregated "long" and "duration" typed fields as JSON strings, so parseDQLLong
// is used instead of float64 type assertions.
func (s *DynatraceTraceSource) convertDQLGroupedToTraceGroupingValues(records []map[string]any) []TraceGroupingValues {
	result := make([]TraceGroupingValues, 0, len(records))
	for _, r := range records {
		group := TraceGroupingValues{
			WorkloadName:      grailStr(r, "workload"),
			WorkloadNamespace: grailStr(r, "namespace"),
			SpanName:          grailStr(r, "span_name"),
			HTTPStatusCode:    grailStr(r, "http_status"),
		}

		// http.response.status_code is usually a string attribute, but some instrumentation
		// libraries store it as a numeric type. Handle float64 as a fallback.
		if group.HTTPStatusCode == "" {
			if code, ok := r["http_status"].(float64); ok {
				group.HTTPStatusCode = fmt.Sprintf("%d", int(code))
			}
		}

		// Counts
		group.Count = int(parseDQLLong(r["count"]))
		group.ErrorCount = int(parseDQLLong(r["error_count"]))

		// Latencies — DQL duration values are in nanoseconds, returned as JSON strings.
		// Clamp to zero: negative values indicate data corruption or clock skew on the DT side.
		group.DurationNS = clampNs(parseDQLLong(r["avg_duration"]))
		group.P95Latency = clampNs(parseDQLLong(r["p95_duration"]))
		group.P99Latency = clampNs(parseDQLLong(r["p99_duration"]))
		group.MaxLatency = clampNs(parseDQLLong(r["max_duration"]))

		// Resource URL — fall back to span name when url.path is absent (e.g. non-HTTP spans).
		group.Resource = grailStr(r, "resource_url")
		if group.Resource == "" {
			group.Resource = group.SpanName
		}

		// Drop rows where both workload and span name are empty (incomplete/anonymous spans).
		if group.WorkloadName == "" && group.SpanName == "" {
			continue
		}

		result = append(result, group)
	}
	return result
}

// clampNs returns v if v >= 0, otherwise 0. Used to guard latency fields against negative
// values that can arise from data corruption or clock skew in the telemetry backend.
func clampNs(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// parseDQLLong extracts an int64 from a DQL summarize record value.
// DQL returns "long" and "duration" typed aggregation fields as JSON strings (e.g. "1006"),
// but defensively handles float64 and int64 as well.
func parseDQLLong(raw any) int64 {
	if raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
		// Handle float-formatted strings like "1006.5" from fractional averages.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f)
		}
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// extractTraceID checks the request's structured where clause for a trace_id equality filter.
func (s *DynatraceTraceSource) extractTraceID(req TracesV3Request) string {
	if req.QueryRequest.Where.Binary == nil {
		return ""
	}
	if ops, ok := req.QueryRequest.Where.Binary["trace_id"]; ok {
		for _, val := range ops {
			if str, ok := val.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

// collectLabelValues extracts up to limit distinct non-empty values for the given
// span field across Grail span records.
func (s *DynatraceTraceSource) collectLabelValues(records []map[string]any, labelName string, limit int) []string {
	seen := make(map[string]bool)
	var values []string
	for _, record := range records {
		val := grailStr(record, labelName)
		if val != "" && !seen[val] {
			seen[val] = true
			values = append(values, val)
			if len(values) >= limit {
				return values
			}
		}
	}
	return values
}

// parseK8sDNS extracts the service name and namespace from a Kubernetes service DNS name.
// K8s service DNS format: <service>.<namespace>.svc[.cluster.local][.]
// Returns empty strings if the address does not match the pattern.
func parseK8sDNS(addr string) (service, namespace string) {
	addr = strings.TrimSuffix(addr, ".")
	parts := strings.Split(addr, ".")
	if len(parts) >= 3 && parts[2] == "svc" {
		return parts[0], parts[1]
	}
	return "", ""
}

// extractHostFromURL extracts the hostname from a URL string using net/url for correct
// handling of ports, user info, and other edge cases.
func extractHostFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Prepend a scheme if absent so url.Parse handles bare host[:port] strings correctly.
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// mapSpanKind converts a Dynatrace/OTel span kind string to a canonical SpanKind value.
func (s *DynatraceTraceSource) mapSpanKind(kind string) string {
	switch strings.ToUpper(kind) {
	case "SERVER", "ENTRY":
		return "SERVER"
	case "CLIENT", "EXIT":
		return "CLIENT"
	case "PRODUCER":
		return "PRODUCER"
	case "CONSUMER":
		return "CONSUMER"
	case "INTERNAL", "LOCAL":
		return "INTERNAL"
	default:
		return "UNSPECIFIED"
	}
}

// getTimeRange converts epoch ms timestamps to ISO 8601 strings for the Dynatrace Grail API.
func (s *DynatraceTraceSource) getTimeRange(startTime, endTime int64) (string, string) {
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).UnixMilli()
	}
	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}
	if startTime < 1e12 {
		startTime *= 1000
	}
	if endTime < 1e12 {
		endTime *= 1000
	}
	return time.UnixMilli(startTime).UTC().Format(time.RFC3339),
		time.UnixMilli(endTime).UTC().Format(time.RFC3339)
}
