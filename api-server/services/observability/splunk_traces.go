package observability

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"time"
)

// SplunkTraceSource implements TraceSource for Splunk Observability Cloud APM.
// Uses the Splunk APM trace search API:
//
//	GET https://api.<realm>.signalfx.com/v2/trace
//	Authorization: X-SF-TOKEN <access-token>
type SplunkTraceSource struct{}

// splunkO11yTraceLabelMapping maps standard trace field names to Splunk APM attribute names.
var splunkO11yTraceLabelMapping = map[string]string{
	"service":     "sf_service",
	"operation":   "sf_operation",
	"environment": "sf_environment",
	"status":      "sf_error",
	"duration":    "duration",
	"trace_id":    "traceId",
	"span_id":     "spanId",
}

func (s *SplunkTraceSource) GetLabelMapping() map[string]string {
	return splunkO11yTraceLabelMapping
}

func (s *SplunkTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_like"}
}

// QueryTraces searches for traces matching the given request via the Splunk APM API.
func (s *SplunkTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	query := s.buildTraceQuery(req)
	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)

	raw, err := integrations.ExecuteO11yTraceQuery(cfg, query, startMs, endMs, 100)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y trace query failed: %w", err)
	}

	return s.convertRawToTraces(raw), nil
}

// GetQuery returns the APM query string that would be executed for the given request.
func (s *SplunkTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return s.buildTraceQuery(req), nil
}

// CountTraces returns the number of traces matching the given request.
func (s *SplunkTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	query := s.buildTraceQuery(req)
	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)

	// Fetch up to 1000 and count (the API does not provide a separate count endpoint)
	raw, err := integrations.ExecuteO11yTraceQuery(cfg, query, startMs, endMs, 1000)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("splunk O11y trace count failed: %w", err)
	}

	return common.OpenTelemetryTraceCount{Count: len(raw)}, nil
}

// GetLabelValues returns distinct values for a given trace attribute from the APM catalog.
func (s *SplunkTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	labelName := req.Label
	if mapped, ok := splunkO11yTraceLabelMapping[labelName]; ok {
		labelName = mapped
	}

	// Fetch a sample of recent traces and extract distinct values for the label.
	// Use top-level StartTime/EndTime if provided; normalizeTimeRangeMs defaults to last 1h.
	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)

	raw, err := integrations.ExecuteO11yTraceQuery(cfg, "", startMs, endMs, 500)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("splunk O11y trace label values failed: %w", err)
	}

	seen := make(map[string]bool)
	var values []string
	for _, r := range raw {
		if val, ok := r[labelName]; ok {
			str := fmt.Sprintf("%v", val)
			if str != "" && !seen[str] {
				seen[str] = true
				values = append(values, str)
			}
		}
		if len(values) >= 50 {
			break
		}
	}

	return common.OpenTelemetryTraceLabelValues{
		Label:  req.Label,
		Values: values,
	}, nil
}

// QueryGroupedTraces groups traces by service/operation and returns aggregated statistics.
func (s *SplunkTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	query := s.buildTraceQuery(req)
	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)

	raw, err := integrations.ExecuteO11yTraceQuery(cfg, query, startMs, endMs, 1000)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y grouped trace query failed: %w", err)
	}

	return s.aggregateTraceGroups(raw), nil
}

// QueryGroupedTracesCount returns the count of grouped trace buckets.
func (s *SplunkTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	groups, err := s.QueryGroupedTraces(ctx, req)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(groups)}, nil
}

// QueryTracesHeatmap returns trace heatmap data (duration vs. time) for a given trace.
func (s *SplunkTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	// Fetch spans for the given trace ID.
	query := fmt.Sprintf("traceId:%s", integrations.EscapeO11yQueryString(req.TraceId))
	startMs := time.Now().Add(-24 * time.Hour).UnixMilli()
	endMs := time.Now().UnixMilli()

	raw, err := integrations.ExecuteO11yTraceQuery(cfg, query, startMs, endMs, 200)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y trace heatmap query failed: %w", err)
	}

	return s.convertRawToHeatmap(raw), nil
}

// --- Query builder ---

// buildTraceQuery constructs a Splunk APM query string from a TracesV3Request.
// Uses the free-text Query field or converts the structured Where clause to Lucene syntax.
func (s *SplunkTraceSource) buildTraceQuery(req TracesV3Request) string {
	if req.Query != "" {
		return req.Query
	}

	if hasWhereConditions(req.QueryRequest.Where) {
		// Strip timestamp — time range is passed via StartTime/EndTime to the API.
		delete(req.QueryRequest.Where.Binary, "timestamp")

		// duration_ns is our canonical field (nanoseconds). Splunk APM stores span duration
		// in the `duration` field in microseconds, so convert before building the query.
		if durOps, ok := req.QueryRequest.Where.Binary["duration_ns"]; ok {
			delete(req.QueryRequest.Where.Binary, "duration_ns")
			convertedOps := make(map[query.BinaryWhereClauseType]any, len(durOps))
			for op, val := range durOps {
				nsVal, ok := parseInt64Value(val)
				if !ok {
					continue // skip non-numeric values
				}
				convertedOps[op] = nsVal / 1000 // ns → µs
			}
			req.QueryRequest.Where.Binary["duration"] = convertedOps
		}

		if clause, err := buildO11yWhereClause(req.QueryRequest.Where); err == nil {
			return clause
		}
	}

	return ""
}

// --- Response converters ---

// convertRawToTraces maps raw APM API response entries to common.OpenTelemetryTrace.
// The Splunk APM trace API returns a JSON array of trace objects. Each object may contain
// span-level fields such as traceId, spanId, operationName, duration, process, tags, etc.
func (s *SplunkTraceSource) convertRawToTraces(raw []map[string]any) []common.OpenTelemetryTrace {
	traces := make([]common.OpenTelemetryTrace, 0, len(raw))
	for _, r := range raw {
		t := common.OpenTelemetryTrace{
			SpanAttributes:     make(map[string]string),
			ResourceAttributes: make(map[string]string),
		}

		if v, ok := r["traceId"].(string); ok {
			t.TraceID = v
		}
		if v, ok := r["spanId"].(string); ok {
			t.SpanID = v
		}
		if v, ok := r["parentSpanId"].(string); ok {
			t.ParentSpanID = v
		}
		if v, ok := r["operationName"].(string); ok {
			t.SpanName = v
		}
		if v, ok := r["spanKind"].(string); ok {
			t.SpanKind = v
		}

		// Duration: Splunk APM typically returns microseconds; convert to nanoseconds.
		if v, ok := r["duration"].(float64); ok {
			t.DurationNs = int64(v) * 1000
		}

		// startTime: Splunk APM typically returns microseconds epoch
		if v, ok := r["startTime"].(float64); ok {
			ts := time.UnixMicro(int64(v)).UTC()
			t.Timestamp = ts.Format(time.RFC3339Nano)
		}

		// Process / service name
		if proc, ok := r["process"].(map[string]any); ok {
			if svc, ok := proc["serviceName"].(string); ok {
				t.ServiceName = svc
				t.ResourceAttributes["service.name"] = svc
			}
		}
		if svc, ok := r["sf_service"].(string); ok && t.ServiceName == "" {
			t.ServiceName = svc
		}

		// Tags (Splunk APM may return as []{"key":..., "value":...} or map)
		s.extractTags(r, t.SpanAttributes)

		// Status
		if v, ok := r["statusCode"].(string); ok {
			t.StatusCode = v
		}
		if v, ok := r["sf_error"].(string); ok {
			t.StatusCode = v
		}
		if v, ok := r["statusMessage"].(string); ok {
			t.StatusMessage = v
		}

		traces = append(traces, t)
	}
	return traces
}

// extractTags handles both tag formats from the Splunk APM API.
func (s *SplunkTraceSource) extractTags(r map[string]any, out map[string]string) {
	if tags, ok := r["tags"].([]any); ok {
		for _, tag := range tags {
			if m, ok := tag.(map[string]any); ok {
				key, _ := m["key"].(string)
				val, _ := m["value"].(string)
				if key != "" {
					out[key] = val
				}
			}
		}
	} else if tags, ok := r["tags"].(map[string]any); ok {
		for k, v := range tags {
			out[k] = fmt.Sprintf("%v", v)
		}
	}
}

// aggregateTraceGroups groups raw trace entries by service+operation and computes statistics.
func (s *SplunkTraceSource) aggregateTraceGroups(raw []map[string]any) []TraceGroupingValues {
	type groupKey struct {
		service   string
		operation string
	}

	type groupAgg struct {
		count      int
		errCount   int
		totalDurNs int64
		maxDurNs   int64
	}

	groups := make(map[groupKey]*groupAgg)

	for _, r := range raw {
		svc := ""
		op := ""
		if proc, ok := r["process"].(map[string]any); ok {
			svc, _ = proc["serviceName"].(string)
		}
		if v, ok := r["sf_service"].(string); ok && svc == "" {
			svc = v
		}
		if v, ok := r["operationName"].(string); ok {
			op = v
		}

		key := groupKey{service: svc, operation: op}
		if _, exists := groups[key]; !exists {
			groups[key] = &groupAgg{}
		}
		g := groups[key]
		g.count++

		if errStr, ok := r["sf_error"].(string); ok && errStr == "true" {
			g.errCount++
		}
		if dur, ok := r["duration"].(float64); ok {
			durNs := int64(dur) * 1000 // µs → ns
			g.totalDurNs += durNs
			if durNs > g.maxDurNs {
				g.maxDurNs = durNs
			}
		}
	}

	result := make([]TraceGroupingValues, 0, len(groups))
	for key, g := range groups {
		result = append(result, TraceGroupingValues{
			Count:        g.count,
			ErrorCount:   g.errCount,
			MaxLatency:   g.maxDurNs,
			WorkloadName: key.service,
			SpanName:     key.operation,
			DurationNS:   g.totalDurNs,
		})
	}
	return result
}

// convertRawToHeatmap converts raw APM trace entries to heatmap format.
func (s *SplunkTraceSource) convertRawToHeatmap(raw []map[string]any) []common.OpenTelemetryTraceHeatMap {
	heatmap := make([]common.OpenTelemetryTraceHeatMap, 0, len(raw))
	for _, r := range raw {
		h := common.OpenTelemetryTraceHeatMap{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
		}

		if v, ok := r["traceId"].(string); ok {
			h.TraceID = v
		}
		if v, ok := r["spanId"].(string); ok {
			h.SpanID = v
		}
		if v, ok := r["operationName"].(string); ok {
			h.SpanName = v
		}
		if v, ok := r["statusCode"].(string); ok {
			h.StatusCode = v
		}
		if dur, ok := r["duration"].(float64); ok {
			h.DurationNs = int64(dur) * 1000 // µs → ns
		}
		if v, ok := r["startTime"].(float64); ok {
			h.Timestamp = time.UnixMicro(int64(v)).UTC().Format(time.RFC3339Nano)
		}
		if proc, ok := r["process"].(map[string]any); ok {
			if svc, ok := proc["serviceName"].(string); ok {
				h.ServiceName = svc
			}
		}
		s.extractTags(r, h.SpanAttributes)

		heatmap = append(heatmap, h)
	}
	return heatmap
}
