package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"
)

// SolarWindsTraceSource implements TraceSource for SolarWinds Observability.
//
// SolarWinds Observability does not expose a REST API for querying individual spans or traces.
// APM trace data is instead accessible through the metrics API via the
// "trace.service.traced_response_time" gauge metric, where each recorded value is the
// response time (in microseconds) of one traced request.
//
// This source uses that metric to provide operation-level trace groupings, error rates,
// and latency statistics — the same data visible in the SolarWinds Traces Explorer UI.
//
// Supported filters (from TracesQueryBuilderRequest.Where):
//   - workload_name (_eq / _in) → service.name
//   - span_name     (_eq / _in) → sw.transaction
//   - status_code   (_eq / _in) → otel.status_code
//   - _and of the above
//
// Limitations:
//   - QueryTraces returns an empty slice (no individual span query API exists).
//   - QueryTracesHeatmap returns an empty slice (requires trace IDs which are not queryable).
//   - P95/P99 percentiles are unavailable from the metrics API; returned as 0.
//   - _or and _not filter operators are not supported by the SolarWinds filter syntax.
//   - HTTP-level context cancellation is not propagated into DoSolarWindsGET.
//     Cancelling the parent request does not abort in-flight SolarWinds API calls.
type SolarWindsTraceSource struct{}

// solarWindsTraceLabelMapping maps NudgeBee canonical trace field names to the SolarWinds
// APM metric attribute names used in trace.service.traced_response_time.
var solarWindsTraceLabelMapping = map[string]string{
	"workload_name": "service.name",
	"span_name":     "sw.transaction",
	"status_code":   "otel.status_code",
}

const swTraceConfigsErrFormat = "failed to get SolarWinds configs: %w"

// swTraceMetric is the SolarWinds gauge metric that records individual traced request durations.
// Each data point value is a response time in microseconds. Relevant attributes:
//   - service.name:     instrumented service name (maps to NudgeBee workload_name)
//   - sw.transaction:   operation/span name, e.g. "anomaly", "fetch_metrics" (maps to span_name)
//   - otel.status_code: "UNSET", "OK", or "ERROR" (maps to status_code)
//   - sw.span_kind:     "SERVER", "CLIENT", etc.
const swTraceMetric = "trace.service.traced_response_time"

// swMaxPages is a safety limit on measurement API pagination to prevent runaway loops.
const swMaxPages = 50

// GetLabelMapping returns the canonical-to-SolarWinds field name mapping for traces.
func (s *SolarWindsTraceSource) GetLabelMapping() map[string]string {
	return solarWindsTraceLabelMapping
}

// GetSupportedOperators returns the UI-facing operator tokens for SolarWinds
// traces. The list is intentionally narrow: swFilterPart (below) only builds
// filter syntax for Eq and In — every other operator falls through to an empty
// string and is silently dropped when composing the request. Advertising
// operators the filter builder ignores produces broken filter behavior in the
// UI, so we surface only what actually works end-to-end. See issue #29227.
func (s *SolarWindsTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_in"}
}

// GetQuery returns a human-readable description of the metric query used for this request.
func (s *SolarWindsTraceSource) GetQuery(_ *security.RequestContext, _ TracesV3Request) (string, error) {
	return fmt.Sprintf(
		"GET /v1/metrics/%s/measurements?groupBy=service.name,sw.transaction,otel.status_code&aggregateBy=AVG|COUNT|MAX",
		swTraceMetric,
	), nil
}

// QueryTraces returns an empty slice. SolarWinds Observability does not provide a REST API
// for querying individual spans or traces — only pre-aggregated APM metrics are accessible.
func (s *SolarWindsTraceSource) QueryTraces(_ *security.RequestContext, _ TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return []common.OpenTelemetryTrace{}, nil
}

// CountTraces returns the total count of traced requests in the given time range by summing
// the COUNT measurement values across all operation groups.
func (s *SolarWindsTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf(swTraceConfigsErrFormat, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	from, to := swTraceTimeRange(req.StartTime, req.EndTime)

	filter := buildSwTraceFilter(req.QueryRequest.Where)
	groups, err := s.fetchMeasurements(apiToken, baseURL, from, to, "COUNT", filter)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("SolarWinds trace count query failed: %w", err)
	}

	total := 0
	for _, g := range groups {
		total += int(g.sum())
	}
	return common.OpenTelemetryTraceCount{Count: total}, nil
}

// GetLabelValues returns distinct values for a trace label by querying the SolarWinds
// metric attribute values API for trace.service.traced_response_time.
func (s *SolarWindsTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf(swTraceConfigsErrFormat, err)
	}

	labelName := req.Label
	if mapped, ok := solarWindsTraceLabelMapping[labelName]; ok {
		labelName = mapped
	}

	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	path := fmt.Sprintf("/v1/metrics/%s/attributes/%s", swTraceMetric, url.PathEscape(labelName))
	body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, path, map[string]string{})
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("SolarWinds trace label values request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("SolarWinds trace label values API returned HTTP %d", statusCode)
	}

	var resp struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to parse SolarWinds label values response: %w", err)
	}

	return common.OpenTelemetryTraceLabelValues{
		Label:  req.Label,
		Values: resp.Values,
	}, nil
}

// QueryGroupedTraces queries SolarWinds APM metrics to produce operation-level trace groupings.
//
// It fetches three aggregations of trace.service.traced_response_time in parallel:
//   - AVG:   average response time per 1-minute bucket (microseconds)
//   - COUNT: number of traced requests per 1-minute bucket
//   - MAX:   maximum response time per 1-minute bucket (microseconds)
//
// Results are grouped by (service.name, sw.transaction) and merged into TraceGroupingValues.
// Average latency is computed as a COUNT-weighted mean of per-bucket averages, ensuring that
// high-traffic buckets contribute proportionally to the overall average.
// Error counts are derived from groups where otel.status_code == "ERROR".
func (s *SolarWindsTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsTraceSource.QueryGroupedTraces: failed to get configs", "error", err)
		return nil, fmt.Errorf(swTraceConfigsErrFormat, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	from, to := swTraceTimeRange(req.StartTime, req.EndTime)

	type fetchResult struct {
		groups []swTraceGroupData
		err    error
	}

	avgCh := make(chan fetchResult, 1)
	cntCh := make(chan fetchResult, 1)
	maxCh := make(chan fetchResult, 1)

	filter := buildSwTraceFilter(req.QueryRequest.Where)
	go func() {
		g, err := s.fetchMeasurements(apiToken, baseURL, from, to, "AVG", filter)
		avgCh <- fetchResult{g, err}
	}()
	go func() {
		g, err := s.fetchMeasurements(apiToken, baseURL, from, to, "COUNT", filter)
		cntCh <- fetchResult{g, err}
	}()
	go func() {
		g, err := s.fetchMeasurements(apiToken, baseURL, from, to, "MAX", filter)
		maxCh <- fetchResult{g, err}
	}()

	avgRes := <-avgCh
	cntRes := <-cntCh
	maxRes := <-maxCh

	if avgRes.err != nil {
		return nil, fmt.Errorf("SolarWinds trace AVG query failed: %w", avgRes.err)
	}
	if cntRes.err != nil {
		return nil, fmt.Errorf("SolarWinds trace COUNT query failed: %w", cntRes.err)
	}
	if maxRes.err != nil {
		ctx.GetLogger().Warn("SolarWindsTraceSource.QueryGroupedTraces: MAX query failed, max latency set to 0", "error", maxRes.err)
	}

	return mergeSwTraceGroups(avgRes.groups, cntRes.groups, maxRes.groups), nil
}

// QueryGroupedTracesCount returns the number of distinct (service.name, sw.transaction)
// operation groups. Uses a single COUNT call — no need for the AVG and MAX calls that
// QueryGroupedTraces requires for latency data.
func (s *SolarWindsTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf(swTraceConfigsErrFormat, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	from, to := swTraceTimeRange(req.StartTime, req.EndTime)

	filter := buildSwTraceFilter(req.QueryRequest.Where)
	groups, err := s.fetchMeasurements(apiToken, baseURL, from, to, "COUNT", filter)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("SolarWinds trace group count query failed: %w", err)
	}

	// Collapse otel.status_code variants of the same operation into one group.
	seen := make(map[swTraceOpKey]struct{})
	for _, g := range groups {
		if len(g.buckets) > 0 && (g.key.serviceName != "" || g.key.transaction != "") {
			seen[g.key] = struct{}{}
		}
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(seen)}, nil
}

// QueryTracesHeatmap returns an empty slice. SolarWinds Observability does not provide
// individual trace IDs via REST API, making per-trace span heatmap visualization unavailable.
func (s *SolarWindsTraceSource) QueryTracesHeatmap(_ *security.RequestContext, _ TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return []common.OpenTelemetryTraceHeatMap{}, nil
}

// --- Internal types ---

// swMeasurementsResponse is the JSON envelope returned by
// GET /v1/metrics/{name}/measurements with groupBy.
type swMeasurementsResponse struct {
	Groupings []swMeasurementGrouping `json:"groupings"`
	// PageInfo holds the cursor for the next page. Nil when no further pages exist.
	PageInfo *swPageInfo `json:"pageInfo"`
}

// swPageInfo wraps the nextPage cursor URL returned by the SolarWinds measurements API.
// The URL contains a skipToken query parameter used to fetch the next page.
type swPageInfo struct {
	NextPage string `json:"nextPage"`
}

type swMeasurementGrouping struct {
	Attributes   []swMeasurementAttribute `json:"attributes"`
	Measurements []swBucketMeasurement    `json:"measurements"`
}

type swMeasurementAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// swBucketMeasurement is a single time-bucket data point. Value is nil when there is
// no data in the bucket (SolarWinds sparse series).
type swBucketMeasurement struct {
	Time  string   `json:"time"`
	Value *float64 `json:"value"`
}

// swTraceOpKey uniquely identifies a (service, transaction) operation pair.
type swTraceOpKey struct {
	serviceName string
	transaction string
}

// swTraceGroupData is the parsed, per-status representation of one measurements grouping.
// buckets maps ISO-8601 bucket timestamps to their non-null measurement values, enabling
// time-aligned joins between AVG and COUNT series for correct weighted-average computation.
type swTraceGroupData struct {
	key        swTraceOpKey
	statusCode string             // "UNSET", "OK", or "ERROR"
	buckets    map[string]float64 // ISO-8601 time → non-null bucket value
}

func (g *swTraceGroupData) sum() float64 {
	var s float64
	for _, v := range g.buckets {
		s += v
	}
	return s
}

func (g *swTraceGroupData) maxValue() float64 {
	var m float64
	for _, v := range g.buckets {
		if v > m {
			m = v
		}
	}
	return m
}

// --- Private helpers ---

// fetchMeasurements queries the trace metric with the given aggregation method,
// following all pagination cursors until the full result set is retrieved.
//
// groupBy always includes service.name, sw.transaction, otel.status_code so that error
// counts can be separated from total counts during merging.
//
// filter is a SolarWinds filter expression (e.g. "service.name: [anomaly]") built from
// the request's Where clause. Pass an empty string for no filtering.
func (s *SolarWindsTraceSource) fetchMeasurements(apiToken, baseURL, from, to, aggregateBy, filter string) ([]swTraceGroupData, error) {
	params := map[string]string{
		"groupBy":     "service.name,sw.transaction,otel.status_code",
		"aggregateBy": aggregateBy,
		"pageSize":    "100",
	}
	if from != "" {
		params["startTime"] = from
	}
	if to != "" {
		params["endTime"] = to
	}
	if filter != "" {
		params["filter"] = filter
	}

	path := fmt.Sprintf("/v1/metrics/%s/measurements", swTraceMetric)

	var allGroupings []swMeasurementGrouping
	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, path, params)
		if err != nil {
			return nil, fmt.Errorf("request to SolarWinds measurements API failed: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("SolarWinds measurements API returned HTTP %d: %s", statusCode, string(body))
		}

		var resp swMeasurementsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds measurements response: %w", err)
		}

		allGroupings = append(allGroupings, resp.Groupings...)

		// PageInfo.NextPage is a full URL; extract the skipToken cursor from it.
		if resp.PageInfo == nil || resp.PageInfo.NextPage == "" {
			break
		}
		u, err := url.Parse(resp.PageInfo.NextPage)
		if err != nil || u.Query().Get("skipToken") == "" {
			break
		}
		params["skipToken"] = u.Query().Get("skipToken")
	}

	result := make([]swTraceGroupData, 0, len(allGroupings))
	for _, g := range allGroupings {
		key := swTraceOpKey{}
		otelStatus := ""
		for _, a := range g.Attributes {
			switch a.Key {
			case "service.name":
				key.serviceName = a.Value
			case "sw.transaction":
				key.transaction = a.Value
			case "otel.status_code":
				otelStatus = a.Value
			}
		}

		buckets := make(map[string]float64, len(g.Measurements))
		for _, m := range g.Measurements {
			if m.Value != nil {
				buckets[m.Time] = *m.Value
			}
		}

		result = append(result, swTraceGroupData{
			key:        key,
			statusCode: otelStatus,
			buckets:    buckets,
		})
	}
	return result, nil
}

// mergeSwTraceGroups combines AVG, COUNT, and MAX measurement results into TraceGroupingValues.
//
// Groups are keyed by (serviceName, transaction) — the otel.status_code dimension is used
// only to separate error counts (statusCode == "ERROR") from total counts.
//
// Average latency uses a COUNT-weighted mean: for each (operation, statusCode, bucket-time)
// triple that appears in both the AVG and COUNT series, the bucket's average is weighted by
// its request count. This avoids over-representing quiet buckets relative to busy ones.
func mergeSwTraceGroups(avgGroups, cntGroups, maxGroups []swTraceGroupData) []TraceGroupingValues {
	type opStats struct {
		weightedAvgNumerator float64 // Σ(avg_µs_i × count_i) for aligned (key, statusCode, time) triples
		weightedAvgDenom     float64 // Σ(count_i) for the same aligned triples
		maxDurUS             float64 // overall maximum response time (microseconds)
		count                int     // total traced request count (all statuses)
		errorCount           int     // error traced request count (statusCode == "ERROR")
	}

	statsMap := make(map[swTraceOpKey]*opStats)
	ensureStats := func(k swTraceOpKey) *opStats {
		if statsMap[k] == nil {
			statsMap[k] = &opStats{}
		}
		return statsMap[k]
	}

	// Build AVG lookup keyed by (serviceName, transaction, statusCode) for bucket-time alignment.
	type opStatusKey struct {
		swTraceOpKey
		statusCode string
	}
	avgByOpStatus := make(map[opStatusKey]map[string]float64, len(avgGroups))
	for _, g := range avgGroups {
		if len(g.buckets) == 0 {
			continue
		}
		avgByOpStatus[opStatusKey{g.key, g.statusCode}] = g.buckets
	}

	// COUNT: accumulate totals and compute COUNT-weighted avg numerator/denominator.
	for _, g := range cntGroups {
		if len(g.buckets) == 0 {
			continue
		}
		st := ensureStats(g.key)
		for _, cnt := range g.buckets {
			st.count += int(cnt)
			if g.statusCode == "ERROR" {
				st.errorCount += int(cnt)
			}
		}
		// Join with the AVG series at each bucket timestamp.
		if avgMap, ok := avgByOpStatus[opStatusKey{g.key, g.statusCode}]; ok {
			for t, cnt := range g.buckets {
				if avg, ok := avgMap[t]; ok {
					st.weightedAvgNumerator += avg * cnt
					st.weightedAvgDenom += cnt
				}
			}
		}
	}

	// MAX: take the maximum across all status-code variants per operation.
	for _, g := range maxGroups {
		if len(g.buckets) == 0 {
			continue
		}
		st := ensureStats(g.key)
		if m := g.maxValue(); m > st.maxDurUS {
			st.maxDurUS = m
		}
	}

	result := make([]TraceGroupingValues, 0, len(statsMap))
	for k, st := range statsMap {
		if k.serviceName == "" && k.transaction == "" {
			continue
		}

		var avgDurationNS int64
		if st.weightedAvgDenom > 0 {
			avgDurationNS = swUsToNs(st.weightedAvgNumerator / st.weightedAvgDenom)
		}

		result = append(result, TraceGroupingValues{
			WorkloadName: k.serviceName,
			SpanName:     k.transaction,
			Resource:     k.transaction, // URL path not available; use operation name
			Count:        st.count,
			ErrorCount:   st.errorCount,
			DurationNS:   avgDurationNS,
			MaxLatency:   swUsToNs(st.maxDurUS),
			// P95/P99 are not available from SolarWinds REST API; returned as 0.
		})
	}

	return result
}

// swUsToNs converts a SolarWinds response-time value in microseconds to nanoseconds.
// trace.service.traced_response_time values are stored in microseconds.
func swUsToNs(us float64) int64 {
	if us <= 0 {
		return 0
	}
	return int64(us * 1000)
}

// swTraceTimeRange converts epoch timestamps to ISO 8601 UTC strings for the SolarWinds
// metrics API. Falls back to [now-1h, now] when timestamps are zero.
//
// Callers may pass timestamps in seconds or milliseconds. Values below 1e12 are treated
// as seconds and promoted to milliseconds — 1e12 ms is roughly year 2001, so any real
// "current time" value must be above that threshold. This matches the same guard used in
// DynatraceTraceSource.getTimeRange.
func swTraceTimeRange(startTime, endTime int64) (string, string) {
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).UnixMilli()
	}
	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}
	// Normalise seconds → milliseconds.
	if startTime < 1e12 {
		startTime *= 1000
	}
	if endTime < 1e12 {
		endTime *= 1000
	}
	return swMsToISO8601(startTime), swMsToISO8601(endTime)
}

// buildSwTraceFilter converts a QueryWhereClause into a SolarWinds filter string.
//
// Supported operators: _eq (single value) and _in (multiple values).
// Compound _and conditions are flattened; _or and _not are not supported by the
// SolarWinds filter syntax and are silently ignored.
//
// Canonical field names are mapped to SolarWinds attribute names using solarWindsTraceLabelMapping.
// Example output: "service.name: [anomaly] otel.status_code: [ERROR]"
func buildSwTraceFilter(where query.QueryWhereClause) string {
	var parts []string
	collectSwFilterParts(where, &parts)
	return strings.Join(parts, " ")
}

func collectSwFilterParts(where query.QueryWhereClause, parts *[]string) {
	for field, ops := range where.Binary {
		swField := field
		if mapped, ok := solarWindsTraceLabelMapping[field]; ok {
			swField = mapped
		}
		for op, val := range ops {
			if part := swFilterPart(swField, op, val); part != "" {
				*parts = append(*parts, part)
			}
		}
	}
	for _, sub := range where.And {
		collectSwFilterParts(sub, parts)
	}
}

// swFilterPart converts a single binary operator+value into a SolarWinds filter token.
// Returns an empty string for unsupported operators or empty values.
func swFilterPart(swField string, op query.BinaryWhereClauseType, val any) string {
	switch op {
	case query.Eq:
		if s, ok := val.(string); ok && s != "" {
			return fmt.Sprintf("%s: [%s]", swField, s)
		}
	case query.In:
		if strs := swInValues(val); len(strs) > 0 {
			return fmt.Sprintf("%s: [%s]", swField, strings.Join(strs, ","))
		}
	}
	return ""
}

// swInValues extracts non-empty string values from an _in operand ([]any).
func swInValues(val any) []string {
	vals, ok := val.([]any)
	if !ok {
		return nil
	}
	strs := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			strs = append(strs, s)
		}
	}
	return strs
}
