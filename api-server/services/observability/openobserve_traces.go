package observability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"
)

// OpenObserveTraceSource implements TraceSource interface for OpenObserve
type OpenObserveTraceSource struct{}

// openObserveTraceLabelMapping maps standard field names to OpenObserve trace field names
var openObserveTraceLabelMapping = map[string]string{
	"workload_namespace":        "service_k8s_namespace_name",
	"workload_name":             "service_name",
	"destination_workload_name": "service_peer_name",
	"http_status_code":          "http_response_status_code",
	"span_name":                 "operation_name",
	"resource":                  "http_url",
	"status_code":               "status_code",
	"trace_id":                  "trace_id",
	"span_id":                   "span_id",
	"parent_id":                 "reference_parent_span_id",
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

func (s *OpenObserveTraceSource) buildSQL(req TracesV3Request) (string, error) {
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

	sql := `SELECT * FROM "default"`
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}

	sql += fmt.Sprintf(" ORDER BY _timestamp DESC LIMIT %d", limit)

	return sql, nil
}

func (s *OpenObserveTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return s.buildSQL(req)
}

func (s *OpenObserveTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	url, orgID, username, password, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	sql, err := s.buildSQL(req)
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
	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", url, orgID)
	authHeader := fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password)))

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

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	return parseOpenObserveTraceHits(searchResp.Hits), nil
}

func parseOpenObserveTraceHits(hits []map[string]any) []common.OpenTelemetryTrace {
	outputs := make([]common.OpenTelemetryTrace, 0, len(hits))
	for _, hit := range hits {
		trace := common.OpenTelemetryTrace{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
		}

		for k, v := range hit {
			if v == nil {
				continue
			}
			strVal := fmt.Sprintf("%v", v)
			switch k {
			case "trace_id":
				trace.TraceID = strVal
			case "span_id":
				trace.SpanID = strVal
			case "reference_parent_span_id":
				trace.ParentSpanID = strVal
			case "operation_name":
				trace.SpanName = strVal
			case "service_name":
				trace.ServiceName = strVal
			case "span_kind":
				trace.SpanKind = strVal
			case "duration":
				if dur, ok := v.(float64); ok {
					trace.DurationNs = int64(dur) * 1000
				}
			case "_timestamp":
				if tVal, ok := v.(float64); ok {
					ts := time.UnixMicro(int64(tVal))
					trace.Timestamp = ts.UTC().Format(time.RFC3339Nano)
				} else if tStr, ok := v.(string); ok {
					trace.Timestamp = tStr
				}
			default:
				trace.SpanAttributes[k] = strVal
			}
		}
		outputs = append(outputs, trace)
	}

	return outputs
}

func (s *OpenObserveTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	url, orgID, username, password, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	whereClause, err := buildOpenObserveTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	sql := `SELECT count(*) as count FROM "default"`
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

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", url, orgID)
	authHeader := fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password)))

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

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	count := 0
	if len(searchResp.Hits) > 0 {
		if c, ok := searchResp.Hits[0]["count"]; ok {
			if num, ok := c.(float64); ok {
				count = int(num)
			}
		}
	}

	return common.OpenTelemetryTraceCount{Count: count}, nil
}

func (s *OpenObserveTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	url, orgID, username, password, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
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

	sql := fmt.Sprintf(`SELECT %s FROM "default" GROUP BY %s LIMIT 100`, col, col)

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

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=traces", url, orgID)
	authHeader := fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password)))

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

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var values []string
	for _, hit := range searchResp.Hits {
		if val, ok := hit[col]; ok && val != nil {
			strVal := fmt.Sprintf("%v", val)
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

func (s *OpenObserveTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return nil, fmt.Errorf("QueryTracesHeatmap not implemented for OpenObserve")
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
