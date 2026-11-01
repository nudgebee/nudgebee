package observability

import (
	"encoding/json"
	"fmt"
	"math"
	neturl "net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// GrafanaTempoTraceSource reads spans from Grafana Tempo's HTTP API. Listing uses
// TraceQL search, the waterfall uses trace-by-id, grouping uses TraceQL metrics. Counts
// are estimates: Tempo has no count API, and deriving one from TraceQL metrics would tie
// the listing to an optional, 24h-capped engine.
type GrafanaTempoTraceSource struct{}

var _ TraceSource = (*GrafanaTempoTraceSource)(nil)

func tempoTraceConfig(ctx *security.RequestContext, accountId string) (integrations.GrafanaTempoConfig, error) {
	cfg, err := integrations.GetGrafanaTempoConfig(ctx, accountId)
	if err != nil {
		return cfg, fmt.Errorf("failed to get Grafana Tempo config: %w", err)
	}
	return cfg, nil
}

func sortTempoSpans(spans []common.OpenTelemetryTrace, orderBy []query.QueryOrderBy) {
	col, desc := "timestamp", true
	if len(orderBy) > 0 {
		col = orderBy[0].Column
		o := orderBy[0].Order
		desc = o == query.Desc || o == query.DescNullsFirst || o == query.DescNullsLast
	}
	key := func(s common.OpenTelemetryTrace) int64 {
		if col == "duration_ns" {
			return s.DurationNs
		}
		v, _ := strconv.ParseInt(s.StartTimeUnixNano, 10, 64)
		return v
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if desc {
			return key(spans[i]) > key(spans[j])
		}
		return key(spans[i]) < key(spans[j])
	})
}

// queryTempoSpans serves one page. /api/search has no offset, so it fetches the newest
// offset+size traces (with up to that many spans each), sorts and slices. Beyond
// tempoSpanCap the page is empty rather than a re-shuffled sample. The window used is
// returned so the caller can log a clamp.
func queryTempoSpans(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]common.OpenTelemetryTrace, tempoWindow, error) {
	size := req.QueryRequest.Limit
	if size <= 0 {
		size = tempoDefaultPageSize
	}
	offset := req.QueryRequest.Offset
	if offset < 0 {
		offset = 0
	}
	need := tempoSpanCap
	if offset < tempoSpanCap && size <= tempoSpanCap-offset {
		need = offset + size
	}
	win := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now)
	if offset >= need {
		return []common.OpenTelemetryTrace{}, win, nil
	}
	q, err := tempoSearchQuery(req.Query, req.QueryRequest.Where)
	if err != nil {
		return nil, win, err
	}
	params := win.params()
	params["q"] = q
	params["limit"] = strconv.Itoa(need)
	params["spss"] = strconv.Itoa(min(need, tempoMaxSpansPerSpanSet))
	body, err := tempoGet(cfg, "/api/search", params)
	if err != nil {
		return nil, win, err
	}
	spans, err := tempoSearchToSpans(body)
	if err != nil {
		return nil, win, err
	}
	sortTempoSpans(spans, req.QueryRequest.OrderBy)
	end := need
	if end > len(spans) {
		end = len(spans)
	}
	if offset >= end {
		return []common.OpenTelemetryTrace{}, win, nil
	}
	return spans[offset:end], win, nil
}

func queryTempoLabels(cfg integrations.GrafanaTempoConfig, req FetchTraceLabelRequest, now time.Time) ([]OutputTraceLabel, tempoWindow, error) {
	win := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now)
	body, err := tempoGet(cfg, "/api/v2/search/tags", win.params())
	if err != nil {
		return nil, win, err
	}
	var resp struct {
		Scopes []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, win, fmt.Errorf("grafana tempo: decoding tags: %w", err)
	}
	out := []OutputTraceLabel{}
	for _, s := range resp.Scopes {
		if s.Name != "resource" && s.Name != "span" {
			continue // intrinsics are already covered by the canonical fields
		}
		for _, tag := range s.Tags {
			out = append(out, OutputTraceLabel{Label: s.Name + "." + tag, Attributes: map[string]any{}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, win, nil
}

func queryTempoLabelValues(cfg integrations.GrafanaTempoConfig, req TracesV3LabelValuesRequest, now time.Time) ([]string, tempoWindow, error) {
	win := tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now)
	// A field a Tempo span cannot express has no values, which is an answer, not an
	// error: the Traces tab requests destination_workload_namespace on every load.
	if tempoTraceUnsupportedFields[req.Label] {
		return []string{}, win, nil
	}
	attr, err := tempoAttrFor(req.Label)
	if err != nil {
		return nil, win, err
	}
	sel, err := tempoSelector(req.QueryRequest.Where)
	if err != nil {
		return nil, win, err
	}
	params := win.params()
	params["q"] = sel
	body, err := tempoGet(cfg, "/api/v2/search/tag/"+neturl.PathEscape(attr)+"/values", params)
	if err != nil {
		return nil, win, err
	}
	var resp struct {
		TagValues []struct {
			Value string `json:"value"`
		} `json:"tagValues"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, win, fmt.Errorf("grafana tempo: decoding tag values: %w", err)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, tv := range resp.TagValues {
		v := tv.Value
		if attr == "status" {
			v = tempoStatusCode(v)
		}
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, win, nil
}

// queryTempoHeatmap fetches one trace for the waterfall. The UI's waterfall call sends
// only the trace id; a default window would hide every trace older than an hour (Tempo
// answers `{"trace":{}}`), so without start/end none is sent and Tempo searches all
// blocks. A window the caller did send is used, clamped to Tempo's search limit.
func queryTempoHeatmap(cfg integrations.GrafanaTempoConfig, req TracesHeatMapRequest, now time.Time) ([]common.OpenTelemetryTraceHeatMap, tempoWindow, error) {
	var win tempoWindow
	var params map[string]string
	if req.StartTime > 0 || req.EndTime > 0 {
		win = tempoTimeWindow(req.StartTime, req.EndTime, tempoSearchMaxWindow, now)
		params = win.params()
	}
	body, err := tempoGet(cfg, "/api/v2/traces/"+neturl.PathEscape(req.TraceId), params)
	if err != nil {
		return nil, win, err
	}
	spans, err := tempoOTLPToHeatmap(body)
	if err != nil {
		return nil, win, err
	}
	if len(spans) == 0 {
		return nil, win, fmt.Errorf("grafana tempo: trace %s not found in Tempo", req.TraceId)
	}
	return spans, win, nil
}

func (s *GrafanaTempoTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	spans, win, err := queryTempoSpans(cfg, req, time.Now())
	logTempoClamp(ctx, "search", req.StartTime, req.EndTime, win)
	return spans, err
}

func (s *GrafanaTempoTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return tempoSearchQuery(req.Query, req.QueryRequest.Where)
}

func (s *GrafanaTempoTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

func (s *GrafanaTempoTraceSource) CountTracesByTrace(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

func (s *GrafanaTempoTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

func (s *GrafanaTempoTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	vals, win, err := queryTempoLabelValues(cfg, req, time.Now())
	logTempoClamp(ctx, "label_values", req.StartTime, req.EndTime, win)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: vals}, nil
}

func (s *GrafanaTempoTraceSource) QueryLabels(ctx *security.RequestContext, req FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	labels, win, err := queryTempoLabels(cfg, req, time.Now())
	logTempoClamp(ctx, "labels", req.StartTime, req.EndTime, win)
	return labels, err
}

func (s *GrafanaTempoTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	spans, win, err := queryTempoHeatmap(cfg, req, time.Now())
	logTempoClamp(ctx, "trace_by_id", req.StartTime, req.EndTime, win)
	return spans, err
}

type tempoGroupKey struct{ service, namespace, span, dest string }

func tempoKeyOf(l map[string]string) tempoGroupKey {
	return tempoGroupKey{l["resource.service.name"], l["resource.k8s.namespace.name"], l["name"], l["span.net.peer.name"]}
}

// mergeTempoGroups joins the four TraceQL-metrics results on their shared group labels.
// A label Tempo left out (a leaf service has no span.net.peer.name) is an empty key part,
// so the series still merge into one row. Durations arrive in seconds.
func mergeTempoGroups(count, errs, quantiles, maxes []tempoSeries) []TraceGroupingValues {
	rows := map[tempoGroupKey]*TraceGroupingValues{}
	var order []tempoGroupKey
	row := func(l map[string]string) *TraceGroupingValues {
		k := tempoKeyOf(l)
		if r, ok := rows[k]; ok {
			return r
		}
		r := &TraceGroupingValues{WorkloadName: k.service, WorkloadNamespace: k.namespace, SpanName: k.span, DestinationWorkloadName: k.dest}
		rows[k] = r
		order = append(order, k)
		return r
	}
	ns := func(sec float64) int64 { return int64(math.Round(sec * float64(time.Second))) }
	for _, s := range count {
		row(s.Labels).Count = int(s.Value)
	}
	for _, s := range errs {
		row(s.Labels).ErrorCount = int(s.Value)
	}
	for _, s := range quantiles {
		r := row(s.Labels)
		switch s.Labels["p"] {
		case "0.95":
			r.P95Latency = ns(s.Value)
		case "0.99":
			r.P99Latency = ns(s.Value)
			r.DurationNS = ns(s.Value)
		}
	}
	for _, s := range maxes {
		row(s.Labels).MaxLatency = ns(s.Value)
	}
	for _, k := range order {
		r := rows[k]
		if r.MaxLatency <= 0 {
			continue
		}
		// quantile_over_time interpolates within power-of-two duration buckets while
		// max_over_time is exact, so a bucketed P95/P99 can overshoot the true max;
		// cap it there rather than show a percentile above the observed maximum.
		if r.P95Latency > r.MaxLatency {
			r.P95Latency = r.MaxLatency
		}
		if r.P99Latency > r.MaxLatency {
			r.P99Latency = r.MaxLatency
		}
		if r.DurationNS > r.MaxLatency {
			r.DurationNS = r.MaxLatency
		}
	}
	out := make([]TraceGroupingValues, 0, len(order))
	for _, k := range order {
		out = append(out, *rows[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].WorkloadName < out[j].WorkloadName
	})
	return out
}

// queryTempoGroups runs the four instant queries concurrently over at most the last 24h
// (Tempo's TraceQL-metrics limit). Any failure fails the grouped view only; the span
// listing never calls this.
func queryTempoGroups(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]TraceGroupingValues, tempoWindow, error) {
	win := tempoTimeWindow(req.StartTime, req.EndTime, tempoMetricsMaxWindow, now)
	qs, err := buildTempoGroupQueries(req.Query, req.QueryRequest.Where)
	if err != nil {
		return nil, win, err
	}
	queries := []string{qs.Count, qs.Errors, qs.Quantiles, qs.Max}
	results := make([][]tempoSeries, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			params := win.params()
			params["q"] = q
			body, err := tempoGet(cfg, "/api/metrics/query", params)
			if err != nil {
				errs[i] = err
				return
			}
			results[i], errs[i] = parseTempoMetricSeries(body)
		}(i, q)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, win, fmt.Errorf("grafana tempo grouping: %w", err)
		}
	}
	return mergeTempoGroups(results[0], results[1], results[2], results[3]), win, nil
}

// queryTempoGroupPage serves one grouped-view page in the requested order (the UI sorts
// by error count, P99/P95/Max/Avg latency or count) through the helper the
// Elasticsearch sources share, which also clamps a negative offset to the first page.
func queryTempoGroupPage(cfg integrations.GrafanaTempoConfig, req TracesV3Request, now time.Time) ([]TraceGroupingValues, tempoWindow, error) {
	rows, win, err := queryTempoGroups(cfg, req, now)
	if err != nil {
		return nil, win, err
	}
	return sortAndPaginateTraceGroups(rows, req), win, nil
}

func (s *GrafanaTempoTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	rows, win, err := queryTempoGroupPage(cfg, req, time.Now())
	logTempoClamp(ctx, "grouping", req.StartTime, req.EndTime, win)
	return rows, err
}

func (s *GrafanaTempoTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := tempoTraceConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	rows, win, err := queryTempoGroups(cfg, req, time.Now())
	logTempoClamp(ctx, "grouping_count", req.StartTime, req.EndTime, win)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(rows)}, nil
}

func (s *GrafanaTempoTraceSource) GetLabelMapping() map[string]string { return tempoTraceLabelMapping }

func (s *GrafanaTempoTraceSource) GetSupportedOperators() []string { return tempoSupportedOperators }
