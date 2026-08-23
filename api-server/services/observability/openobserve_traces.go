package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// OpenObserveTraceSource implements TraceSource interface for OpenObserve
type OpenObserveTraceSource struct{}

// openObserveTraceLabelMapping maps canonical field names to the OpenObserve trace column a
// filter should target.
//
// A WHERE clause can name only one column, so each entry points at the spelling that covers
// the most spans. http_status_code and resource previously pointed at
// http_response_status_code and http_url — names from the newer OpenTelemetry HTTP
// conventions that most SDKs in a typical cluster do not emit — so filtering on them matched
// nothing. Display tolerates every spelling via openObserveTraceFieldCandidates; only
// filtering is limited to one.
var openObserveTraceLabelMapping = map[string]string{
	"workload_namespace":        "service_k8s_namespace_name",
	"workload_name":             "service_name",
	"destination_workload_name": "service_peer_name",
	"http_status_code":          "http_status_code",
	"span_name":                 "operation_name",
	"resource":                  "http_target",
	// Stays on the numeric column: the Ok/Error filter sends OTel status codes ('0'/'2'),
	// which would never match span_status's OK/ERROR/UNSET text. Display uses the readable
	// span_status independently.
	"status_code": "status_code",
	"trace_id":    "trace_id",
	"span_id":     "span_id",
	"parent_id":   "reference_parent_span_id",
}

// openObserveTraceFieldCandidates lists the accepted column names per rendered column, in
// priority order.
//
// A span's attribute names depend on which OpenTelemetry semantic-convention generation its
// SDK targets: HTTP attributes were renamed between versions (http.status_code →
// http.response.status_code, http.target → url.full), and a real cluster runs SDKs from both
// eras side by side, plus gRPC spans and spans with no HTTP at all. Reading a single name
// leaves the column blank for every span that uses another — which is why the Resource and
// HTTP Status columns were populated on some rows and empty on others.
var openObserveTraceFieldCandidates = struct {
	Resource       []string
	HTTPStatusCode []string
	Destination    []string
}{
	Resource:       []string{"http_target", "url_full", "url_path", "http_route", "http_url"},
	HTTPStatusCode: []string{"http_response_status_code", "http_status_code", "rpc_grpc_status_code"},
	Destination:    []string{"service_peer_name", "server_address", "net_peer_name", "net_sock_peer_addr", "net_peer_ip", "http_host"},
}

// openObserveDurationMicros reads a span duration, in microseconds.
//
// The value arrives as a bare number from the search API, but OpenObserve also renders it
// with an explicit unit ("1001216us"). Accepting both means a deployment that returns the
// annotated form does not silently produce zero-length spans — which would collapse every
// bar in the heatmap.
func openObserveDurationMicros(v any) (int64, bool) {
	if micros, ok := openObserveInt64(v); ok {
		return micros, true
	}

	s, isStr := v.(string)
	if !isStr {
		return 0, false
	}
	s = strings.TrimSpace(s)

	// Longest suffix first: "us"/"ms"/"ns" must be tested before the bare "s".
	for _, unit := range []struct {
		suffix string
		scale  func(float64) float64
	}{
		{"us", func(f float64) float64 { return f }},
		{"ms", func(f float64) float64 { return f * 1000 }},
		{"ns", func(f float64) float64 { return f / 1000 }},
		{"s", func(f float64) float64 { return f * 1000000 }},
	} {
		if !strings.HasSuffix(s, unit.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, unit.suffix), 64)
		if err != nil {
			return 0, false
		}
		return int64(unit.scale(n)), true
	}

	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(n), true
}

// firstOpenObserveValue returns the first candidate present and non-empty on the hit.
func firstOpenObserveValue(hit map[string]any, candidates []string) string {
	for _, c := range candidates {
		if v, ok := hit[c]; ok && v != nil {
			if s := formatOpenObserveLabelValue(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func (s *OpenObserveTraceSource) GetLabelMapping() map[string]string {
	return openObserveTraceLabelMapping
}

func (s *OpenObserveTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_ilike"}
}

func buildOpenObserveTraceWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildOpenObserveBinaryClause(where.Binary, openObserveTraceLabelMapping)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildOpenObserveTraceWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}

	if len(where.Or) > 0 {
		var parts []string
		for _, c := range where.Or {
			part, err := buildOpenObserveTraceWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	if where.Not != nil {
		notPart, err := buildOpenObserveTraceWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return fmt.Sprintf("NOT (%s)", notPart), nil
		}
	}

	return "", nil
}

func (s *OpenObserveTraceSource) buildSQL(req TracesV3Request, stream string) (string, error) {
	// Callers pass a config-resolved stream, which GetOpenObserveConfigs has already
	// validated. Re-checked here so the function is safe for any caller, not just that one.
	if !integrations.IsSafeOpenObserveStreamName(stream) {
		return "", fmt.Errorf("invalid or unsafe stream name: %q", stream)
	}

	whereClause, err := buildOpenObserveTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	limit := req.QueryRequest.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}

	sql := fmt.Sprintf(`SELECT * FROM "%s"`, stream)
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}

	sql += fmt.Sprintf(" ORDER BY _timestamp DESC LIMIT %d", limit)

	return sql, nil
}

func (s *OpenObserveTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}
	return s.buildSQL(req, cfg.TraceStream)
}

func (s *OpenObserveTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	sql, err := s.buildSQL(req, cfg.TraceStream)
	if err != nil {
		return nil, err
	}

	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	// For traces, we query the _search endpoint with type=traces if possible, or just the default stream
	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errorBody)
		return nil, fmt.Errorf("OpenObserve trace query failed with status %d: %v", resp.StatusCode, errorBody)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	return parseOpenObserveTraceHits(searchResp.Hits), nil
}

// parseOpenObserveTraceHits maps OpenObserve's flattened span schema onto the shared
// OpenTelemetryTrace shape.
//
// The columns the trace list actually renders (see the `cols` list the frontend requests)
// are workload_name/_namespace, status_code, resource, http_status_code and the
// destination_* set — not just the span identifiers. Leaving them unset renders rows with
// empty cells even though the data is present in the hit, so each is populated from the
// same source column openObserveTraceLabelMapping already declares for filtering; a field
// that filters on service_k8s_namespace_name must display from it too, or the two disagree.
func parseOpenObserveTraceHits(hits []map[string]any) []common.OpenTelemetryTrace {
	outputs := make([]common.OpenTelemetryTrace, 0, len(hits))
	for _, hit := range hits {
		trace := common.OpenTelemetryTrace{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
			TraceSource:        "openobserve",
		}
		// Ingest time, kept aside as the fallback span origin for streams that carry no
		// start_time column.
		var timestampMicros int64

		for k, v := range hit {
			if v == nil {
				continue
			}
			// Shared with the log source: %v renders large numbers in scientific notation,
			// which corrupts numeric ids and status codes.
			strVal := formatOpenObserveLabelValue(v)
			switch k {
			case "trace_id":
				trace.TraceID = strVal
			case "span_id":
				trace.SpanID = strVal
			case "reference_parent_span_id":
				trace.ParentSpanID = strVal
			case "operation_name":
				trace.SpanName = strVal
				trace.Operation = strVal
			case "service_name":
				trace.ServiceName = strVal
				trace.Service = strVal
				// The trace list groups by workload; for OTel spans the service is the
				// workload, which is what openObserveTraceLabelMapping filters on.
				trace.WorkloadName = strVal
			case "service_k8s_namespace_name":
				trace.WorkloadNamespace = strVal
				trace.ResourceAttributes["k8s.namespace.name"] = strVal
			case "service_peer_name":
				trace.DestinationWorkload = strVal
				trace.DestinationName = strVal
			case "http_url":
				trace.Resource = strVal
			case "http_response_status_code":
				trace.HTTPStatusCode = strVal
			case "span_status":
				// The readable form (OK / ERROR / UNSET). status_code carries the numeric
				// OTel code, which renders as a bare "0" or "2" in the Status column.
				trace.StatusCode = strVal
			case "status_code":
				if trace.StatusCode == "" {
					trace.StatusCode = strVal
				}
				trace.SpanAttributes[k] = strVal
			case "status_message":
				trace.StatusMessage = strVal
			case "start_time":
				// Epoch nanoseconds. Only exact because the response is decoded with
				// UseNumber — a float64 loses the low digits at this magnitude.
				if ns, ok := openObserveInt64(v); ok && ns > 0 {
					trace.StartTimeUnixNano = strconv.FormatInt(ns, 10)
					trace.StartTime = time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
				}
			case "end_time":
				if ns, ok := openObserveInt64(v); ok && ns > 0 {
					trace.EndTimeUnixNano = strconv.FormatInt(ns, 10)
					trace.EndTime = time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
				}
			case "span_kind":
				trace.SpanKind = strVal
			case "duration":
				// OpenObserve stores span duration in microseconds.
				if micros, ok := openObserveDurationMicros(v); ok {
					trace.DurationNs = micros * 1000
				}
			case "_timestamp":
				// Ingest time in microseconds. Deliberately does not touch StartTime: the
				// span carries its own start_time column, and map iteration order is
				// random, so writing both here could clobber the authoritative value.
				if micros, ok := openObserveInt64(v); ok {
					timestampMicros = micros
					trace.Timestamp = time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
				} else if tStr, ok := v.(string); ok {
					trace.Timestamp = tStr
				}
			default:
				trace.SpanAttributes[k] = strVal
			}
		}

		// Columns whose source attribute varies by semantic-convention generation are
		// resolved after the switch, since the winning name differs per span.
		if trace.Resource == "" {
			trace.Resource = firstOpenObserveValue(hit, openObserveTraceFieldCandidates.Resource)
		}
		if trace.HTTPStatusCode == "" {
			trace.HTTPStatusCode = firstOpenObserveValue(hit, openObserveTraceFieldCandidates.HTTPStatusCode)
		}
		if trace.DestinationName == "" {
			trace.DestinationName = firstOpenObserveValue(hit, openObserveTraceFieldCandidates.Destination)
		}

		// Fall back to the ingest timestamp for spans whose stream carries no start_time,
		// so the waterfall still has an origin to lay the span out from.
		if trace.StartTime == "" {
			trace.StartTime = trace.Timestamp
		}
		if trace.StartTimeUnixNano == "" && timestampMicros > 0 {
			trace.StartTimeUnixNano = strconv.FormatInt(timestampMicros*1000, 10)
		}

		// Likewise derive the end from start + duration only when the span did not report
		// one; a waterfall needs the extent, not just the origin.
		if trace.EndTimeUnixNano == "" && trace.StartTimeUnixNano != "" && trace.DurationNs > 0 {
			if startNs, err := strconv.ParseInt(trace.StartTimeUnixNano, 10, 64); err == nil {
				endNs := startNs + trace.DurationNs
				trace.EndTimeUnixNano = strconv.FormatInt(endNs, 10)
				trace.EndTime = time.Unix(0, endNs).UTC().Format(time.RFC3339Nano)
			}
		}

		outputs = append(outputs, trace)
	}

	return outputs
}

func (s *OpenObserveTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	whereClause, err := buildOpenObserveTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	sql := fmt.Sprintf(`SELECT count(*) as count FROM "%s"`, cfg.TraceStream)
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}

	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to marshal count request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("OpenObserve count query failed with status %d", resp.StatusCode)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	count := 0
	if len(searchResp.Hits) > 0 {
		if c, ok := searchResp.Hits[0]["count"]; ok {
			if num, ok := openObserveInt64(c); ok {
				maxInt := int64(^uint(0) >> 1)
				if num >= 0 && num <= maxInt {
					count = int(num)
				}
			}
		}
	}

	return common.OpenTelemetryTraceCount{Count: count}, nil
}

func (s *OpenObserveTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	col := req.Label
	if mapped, ok := openObserveTraceLabelMapping[col]; ok {
		col = mapped
	}
	if !isSafeIdentifier(col) {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("invalid or unsafe label name: %q", col)
	}

	sql := fmt.Sprintf(`SELECT %s FROM "%s" GROUP BY %s LIMIT 100`, col, cfg.TraceStream, col)

	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("OpenObserve query failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var values []string
	for _, hit := range searchResp.Hits {
		if val, ok := hit[col]; ok && val != nil {
			// Shared with the log source: %v would render numeric ids and timestamps in
			// scientific notation, producing a filter value that matches nothing.
			strVal := formatOpenObserveLabelValue(val)
			if strVal != "" {
				values = append(values, strVal)
			}
		}
	}

	return common.OpenTelemetryTraceLabelValues{Label: col, Values: values}, nil
}

func (s *OpenObserveTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("QueryGroupedTraces not implemented for OpenObserve")
}

func (s *OpenObserveTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("QueryGroupedTracesCount not implemented for OpenObserve")
}

// openObserveHeatmapSpanLimit caps the spans returned for one trace. A trace with more
// spans than this cannot be laid out legibly anyway, and the cap keeps a runaway trace from
// stalling the request.
const openObserveHeatmapSpanLimit = 2000

// openObserveHeatmapWindowPadding widens a caller-supplied window when fetching a single
// trace. The window is derived from the row's timestamp, but a trace's earliest span can
// begin fractionally before it and its latest can end after — clipping either truncates the
// waterfall. The trace_id filter keeps the wider scan cheap.
const openObserveHeatmapWindowPadding = 5 * time.Minute

// openObserveHeatmapDefaultLookback is how far back the heatmap searches when the caller
// supplies no window at all.
//
// The trace-detail view asks by trace_id alone — see the traces_get_heatmap query, which
// sends only account_id and trace_id. Falling back to the usual one-hour default returned
// zero spans for any older trace: verified against a live instance, a 2-hour-old trace with
// 145 spans came back empty. NewRelicTraceSource.QueryTracesHeatmap uses 30 days for the
// same reason.
const openObserveHeatmapDefaultLookback = 30 * 24 * time.Hour

// openObserveHeatmapWindow resolves the search window for one trace, in microseconds.
func openObserveHeatmapWindow(startMs, endMs int64, now time.Time) (int64, int64) {
	if startMs > 0 && endMs > 0 {
		pad := openObserveHeatmapWindowPadding.Microseconds()
		return startMs*1000 - pad, endMs*1000 + pad
	}
	return now.Add(-openObserveHeatmapDefaultLookback).UnixMicro(), now.UnixMicro()
}

// QueryTracesHeatmap returns every span of one trace, which the UI lays out as the trace
// waterfall. Mirrors NewRelicTraceSource.QueryTracesHeatmap: fetch the whole trace by id,
// then project the spans onto the shared heatmap shape.
func (s *OpenObserveTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	if req.TraceId == "" {
		return nil, fmt.Errorf("trace_id is required for the trace heatmap")
	}

	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveTraceSource.QueryTracesHeatmap: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// Ordered by start_time so the waterfall arrives in execution order rather than
	// ingest order; _timestamp can differ from the span's own start.
	sql := fmt.Sprintf(`SELECT * FROM "%s" WHERE trace_id = '%s' ORDER BY start_time ASC LIMIT %d`,
		cfg.TraceStream, escapeOpenObserveString(req.TraceId), openObserveHeatmapSpanLimit)

	startMicros, endMicros := openObserveHeatmapWindow(req.StartTime, req.EndTime, time.Now())

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startMicros
	searchReq.Query.EndTime = endMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal heatmap request: %w", err)
	}

	ctx.GetLogger().Info("OpenObserve Trace Heatmap Query", "query", sql)

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", cfg.URL, cfg.OrgID)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(cfg.Username, cfg.Password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve heatmap request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenObserve heatmap query failed with status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	// Reuse the span parser so the heatmap resolves service, status and duration exactly
	// the way the trace list does — including the semantic-convention fallbacks.
	return openObserveTracesToHeatmap(parseOpenObserveTraceHits(searchResp.Hits)), nil
}

// openObserveTracesToHeatmap projects parsed spans onto the heatmap shape.
func openObserveTracesToHeatmap(traces []common.OpenTelemetryTrace) []common.OpenTelemetryTraceHeatMap {
	out := make([]common.OpenTelemetryTraceHeatMap, 0, len(traces))
	for _, t := range traces {
		out = append(out, common.OpenTelemetryTraceHeatMap{
			Timestamp:          t.Timestamp,
			ResourceAttributes: t.ResourceAttributes,
			SpanName:           t.SpanName,
			StatusCode:         t.StatusCode,
			DurationNs:         t.DurationNs,
			SpanAttributes:     t.SpanAttributes,
			TraceID:            t.TraceID,
			SpanID:             t.SpanID,
			ServiceName:        t.ServiceName,
		})
	}
	return out
}

// QueryLabels has no cheap backend label-key discovery API for OpenObserve, so it returns an
// empty slice; FetchTraceLabels falls back to the derived canonical + mapping label set.
func (s *OpenObserveTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}

// QueryRootSpansByTrace backs the "By Traces" listing; OpenObserve reduces its span result to
// representative root spans via the shared helper.
func (s *OpenObserveTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

// CountTracesByTrace returns -1 (estimate) for the "By Traces" view; distinct-trace counting is
// not available cheaply for OpenObserve, so the frontend estimates pagination.
func (s *OpenObserveTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}
