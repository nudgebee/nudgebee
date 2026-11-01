package observability

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTempoSearch returns n single-span traces, newest first, one second apart, and
// honours `limit` the way Tempo does (number of traces).
func fakeTempoSearch(t *testing.T, n int, calls *int32, lastParams *map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		q := r.URL.Query()
		if lastParams != nil {
			*lastParams = map[string]string{"q": q.Get("q"), "start": q.Get("start"), "end": q.Get("end"), "limit": q.Get("limit"), "spss": q.Get("spss")}
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit > n {
			limit = n
		}
		var traces []string
		for i := 0; i < limit; i++ {
			start := int64(1790626200000000000) - int64(i)*int64(time.Second)
			traces = append(traces, fmt.Sprintf(`{"traceID":"%032x","spanSets":[{"spans":[{"spanID":"%016x","name":"op","startTimeUnixNano":"%d","durationNanos":"1000","attributes":[{"key":"service.name","value":{"stringValue":"svc"}}]}]}]}`, i+1, i+1, start))
		}
		_, _ = w.Write([]byte(`{"traces":[` + strings.Join(traces, ",") + `]}`))
	}))
}

func tempoReq(offset, limit int) TracesV3Request {
	return TracesV3Request{AccountId: "acc", QueryRequest: TracesQueryBuilderRequest{Offset: offset, Limit: limit}}
}

func TestQueryTempoSpans_PagesDoNotOverlap(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 10, &calls, &params)
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}

	page0, _, err := queryTempoSpans(cfg, tempoReq(0, 3), time.Now())
	require.NoError(t, err)
	page1, _, err := queryTempoSpans(cfg, tempoReq(3, 3), time.Now())
	require.NoError(t, err)

	require.Len(t, page0, 3)
	require.Len(t, page1, 3)
	assert.Equal(t, "6", params["limit"], "page 2 fetches offset+size traces")
	seen := map[string]bool{}
	for _, s := range append(page0, page1...) {
		assert.False(t, seen[s.SpanID], "span %s appears on two pages", s.SpanID)
		seen[s.SpanID] = true
	}
	assert.Greater(t, page0[2].StartTimeUnixNano, page1[0].StartTimeUnixNano, "page 1 is newer than page 2")
	assert.Contains(t, params["q"], "with (most_recent=true)")
}

func TestQueryTempoSpans_OffsetBeyondCapIsEmpty(t *testing.T) {
	var calls int32
	srv := fakeTempoSearch(t, 10, &calls, nil)
	defer srv.Close()
	spans, _, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, tempoReq(tempoSpanCap, 50), time.Now())
	require.NoError(t, err)
	assert.Empty(t, spans)
	assert.Equal(t, int32(0), calls, "no request is made for a page past the cap")
}

func TestQueryTempoSpans_ClampsWindow(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 1, &calls, &params)
	defer srv.Close()
	now := time.Now()
	req := tempoReq(0, 10)
	req.StartTime = now.Add(-30 * 24 * time.Hour).UnixMilli()
	req.EndTime = now.UnixMilli()
	_, win, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, req, now)
	require.NoError(t, err)
	assert.True(t, win.Clamped, "the clamped window is returned so the source method can log it")
	start, _ := strconv.ParseInt(params["start"], 10, 64)
	end, _ := strconv.ParseInt(params["end"], 10, 64)
	assert.Less(t, end-start, int64(168*3600))
}

func TestQueryTempoLabelsAndValues(t *testing.T) {
	var gotPath, gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQ = r.URL.Path, r.URL.Query().Get("q")
		switch {
		case r.URL.Path == "/api/v2/search/tags":
			_, _ = w.Write([]byte(`{"scopes":[{"name":"intrinsic","tags":["status"]},{"name":"resource","tags":["service.name"]},{"name":"span","tags":["http.target"]}]}`))
		case strings.HasSuffix(r.URL.Path, "/status/values"):
			_, _ = w.Write([]byte(`{"tagValues":[{"type":"keyword","value":"error"},{"type":"keyword","value":"ok"}]}`))
		default:
			_, _ = w.Write([]byte(`{"tagValues":[{"type":"string","value":"nudgebee"},{"type":"string","value":"demo"}]}`))
		}
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}

	labels, _, err := queryTempoLabels(cfg, FetchTraceLabelRequest{AccountId: "acc"}, time.Now())
	require.NoError(t, err)
	var names []string
	for _, l := range labels {
		names = append(names, l.Label)
	}
	assert.Equal(t, []string{"resource.service.name", "span.http.target"}, names)

	vals, _, err := queryTempoLabelValues(cfg, TracesV3LabelValuesRequest{AccountId: "acc", Label: "workload_namespace"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/search/tag/resource.k8s.namespace.name/values", gotPath)
	assert.Equal(t, "{}", gotQ)
	assert.Equal(t, []string{"demo", "nudgebee"}, vals)

	vals, _, err = queryTempoLabelValues(cfg, TracesV3LabelValuesRequest{AccountId: "acc", Label: "status_code"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, []string{"STATUS_CODE_ERROR", "STATUS_CODE_OK"}, vals)
}

func TestQueryTempoHeatmap(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(tempoOTLPFixture))
	}))
	defer srv.Close()
	out, _, err := queryTempoHeatmap(integrations.GrafanaTempoConfig{URL: srv.URL},
		TracesHeatMapRequest{AccountId: "acc", TraceId: "5b8efff798038103d269b633813fc60c"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "/api/v2/traces/5b8efff798038103d269b633813fc60c", gotPath)
	require.Len(t, out, 1)
}

func TestGrafanaTempoTraceSource_StaticContract(t *testing.T) {
	var src TraceSource = &GrafanaTempoTraceSource{}
	assert.Equal(t, tempoTraceLabelMapping, src.GetLabelMapping())
	assert.Equal(t, tempoSupportedOperators, src.GetSupportedOperators())
	count, err := src.CountTraces(nil, TracesV3Request{})
	require.NoError(t, err)
	assert.Equal(t, -1, count.Count, "listing counts are estimates so they never depend on Tempo's metrics engine")
}

func tempoLabels(svc, ns, name, peer string) map[string]string {
	l := map[string]string{"resource.service.name": svc, "resource.k8s.namespace.name": ns, "name": name}
	if peer != "" {
		l["span.net.peer.name"] = peer
	}
	return l
}

func withP(l map[string]string, p string) map[string]string {
	out := map[string]string{"p": p}
	for k, v := range l {
		out[k] = v
	}
	return out
}

func TestMergeTempoGroups(t *testing.T) {
	fe := tempoLabels("frontend", "demo", "GET /checkout", "payments")
	rows := mergeTempoGroups(
		[]tempoSeries{{Labels: fe, Value: 12}},
		[]tempoSeries{{Labels: fe, Value: 2}},
		[]tempoSeries{{Labels: withP(fe, "0.95"), Value: 0.51}, {Labels: withP(fe, "0.99"), Value: 0.536870912}},
		[]tempoSeries{{Labels: fe, Value: 0.6}},
	)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, 12, r.Count)
	assert.Equal(t, 2, r.ErrorCount)
	assert.Equal(t, int64(510000000), r.P95Latency, "TraceQL metrics durations are seconds")
	assert.Equal(t, int64(536870912), r.P99Latency)
	assert.Equal(t, int64(600000000), r.MaxLatency)
	assert.Equal(t, r.P99Latency, r.DurationNS)
	assert.Equal(t, "frontend", r.WorkloadName)
	assert.Equal(t, "demo", r.WorkloadNamespace)
	assert.Equal(t, "GET /checkout", r.SpanName)
	assert.Equal(t, "payments", r.DestinationWorkloadName)
}

func TestMergeTempoGroups_MissingLabel(t *testing.T) {
	leaf := tempoLabels("ledger", "demo", "GET /entries", "")
	rows := mergeTempoGroups([]tempoSeries{{Labels: leaf, Value: 36}}, nil,
		[]tempoSeries{{Labels: withP(leaf, "0.95"), Value: 0.2}}, nil)
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].DestinationWorkloadName)
	assert.Equal(t, 36, rows[0].Count)
	assert.Equal(t, 0, rows[0].ErrorCount, "no error series means zero errors")
}

// TestMergeTempoGroups_CapsQuantilesAtMax covers a live-UI finding: Tempo's
// quantile_over_time interpolates within power-of-two duration buckets while
// max_over_time is exact, so a bucketed P95/P99 can land above the true max
// (0.536870912s = 2^29ns is a bucket bound). mergeTempoGroups caps P95/P99/DurationNS
// at MaxLatency when a max series exists.
func TestMergeTempoGroups_CapsQuantilesAtMax(t *testing.T) {
	bucketed := tempoLabels("frontend", "demo", "GET /checkout", "payments")
	noMax := tempoLabels("ledger", "demo", "GET /entries", "")
	rows := mergeTempoGroups(
		nil, nil,
		[]tempoSeries{
			{Labels: withP(bucketed, "0.95"), Value: 0.486256690},
			{Labels: withP(bucketed, "0.99"), Value: 0.528542268},
			{Labels: withP(noMax, "0.95"), Value: 0.2},
		},
		[]tempoSeries{{Labels: bucketed, Value: 0.4}},
	)
	require.Len(t, rows, 2)
	var capped, uncapped TraceGroupingValues
	for _, r := range rows {
		if r.WorkloadName == "frontend" {
			capped = r
		} else {
			uncapped = r
		}
	}
	assert.Equal(t, int64(400000000), capped.P95Latency, "bucketed P95 above the exact max is capped")
	assert.Equal(t, int64(400000000), capped.P99Latency, "bucketed P99 above the exact max is capped")
	assert.Equal(t, int64(400000000), capped.DurationNS)
	assert.Equal(t, int64(400000000), capped.MaxLatency)

	assert.Equal(t, int64(200000000), uncapped.P95Latency, "no max series means the raw quantile is kept")
	assert.Equal(t, int64(0), uncapped.MaxLatency)
}

func TestQueryTempoGroups_ClampsAndFailsWhole(t *testing.T) {
	var maxWindow atomic.Int64
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		e, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		for {
			cur := maxWindow.Load()
			if e-s <= cur || maxWindow.CompareAndSwap(cur, e-s) {
				break
			}
		}
		if fail.Load() && strings.Contains(q.Get("q"), "max_over_time") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("metrics query time range exceeds the maximum allowed duration of 24h0m0s"))
			return
		}
		_, _ = w.Write([]byte(`{"series":[{"labels":[{"key":"resource.service.name","value":{"stringValue":"frontend"}}],"value":3}]}`))
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}
	now := time.Now()
	req := TracesV3Request{AccountId: "acc", StartTime: now.Add(-7 * 24 * time.Hour).UnixMilli(), EndTime: now.UnixMilli()}

	rows, win, err := queryTempoGroups(cfg, req, now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, win.Clamped)
	assert.Less(t, maxWindow.Load(), int64(24*3600), "grouping is clamped to Tempo's 24h metrics limit")

	fail.Store(true)
	_, _, err = queryTempoGroups(cfg, req, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "24h0m0s")
}

// The grouped view pages through sortAndPaginateTraceGroups, so the UI's order_by
// (error count desc by default, or a latency column) is honoured rather than Tempo's
// count order.
func TestQueryTempoGroupPage_HonoursOrderBy(t *testing.T) {
	series := func(svc string, v float64) string {
		return fmt.Sprintf(`{"labels":[{"key":"resource.service.name","value":{"stringValue":%q}}],"value":%v}`, svc, v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		var body string
		switch {
		case strings.Contains(q, "status = error"):
			body = series("few", 9) + "," + series("many", 1)
		case strings.Contains(q, "count_over_time"):
			body = series("many", 100) + "," + series("few", 10)
		}
		_, _ = w.Write([]byte(`{"series":[` + body + `]}`))
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}
	req := tempoReq(0, 10)
	req.QueryRequest.OrderBy = []query.QueryOrderBy{{Column: "error_count", Order: query.Desc}}

	rows, _, err := queryTempoGroupPage(cfg, req, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "few", rows[0].WorkloadName, "error_count desc puts the 9-error group first")

	req = tempoReq(-1, 1)
	rows, _, err = queryTempoGroupPage(cfg, req, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1, "a negative offset clamps to the first page")
	assert.Equal(t, "many", rows[0].WorkloadName, "default order is count desc")
}

func TestGrafanaTempo_DispatchesAsTraceSourceOnly(t *testing.T) {
	src, err := getTraceSource("grafana_tempo", "user")
	require.NoError(t, err)
	assert.IsType(t, &GrafanaTempoTraceSource{}, src)

	_, err = getMetricsSource("grafana_tempo", "user")
	assert.Error(t, err, "Tempo must not become a metrics provider (docs/architecture-decisions.md)")

	caps, ok := allProviderCaps["grafana_tempo"]
	require.True(t, ok)
	assert.True(t, caps.SupportsTraceGrouping)
	assert.True(t, caps.SupportsHeatmap)
	assert.True(t, caps.SupportsRawQuery)
	assert.False(t, caps.SupportsServiceMap)
}

func TestQueryTempoSpans_SpssCappedAtTempoLimit(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 10, &calls, &params)
	defer srv.Close()
	_, _, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, tempoReq(150, 50), time.Now())
	require.NoError(t, err)
	assert.Equal(t, "200", params["limit"], "limit still covers offset+size traces")
	assert.Equal(t, "100", params["spss"], "Tempo rejects spss above max_spans_per_span_set (100 by default)")
}

// TestQueryTempoSpans_HugeLimitDoesNotOverflow covers offset+size overflowing int when
// either is caller-controlled and near math.MaxInt: `need` must still clamp to
// tempoSpanCap instead of wrapping negative and panicking or bypassing the cap.
func TestQueryTempoSpans_HugeLimitDoesNotOverflow(t *testing.T) {
	var calls int32
	var params map[string]string
	srv := fakeTempoSearch(t, 10, &calls, &params)
	defer srv.Close()

	spans, _, err := queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, tempoReq(5, math.MaxInt), time.Now())
	require.NoError(t, err)
	assert.LessOrEqual(t, len(spans), tempoSpanCap)
	assert.Equal(t, strconv.Itoa(tempoSpanCap), params["limit"], "limit clamps to the cap instead of overflowing")

	calls = 0
	spans, _, err = queryTempoSpans(integrations.GrafanaTempoConfig{URL: srv.URL}, tempoReq(math.MaxInt, 50), time.Now())
	require.NoError(t, err)
	assert.Empty(t, spans)
	assert.Equal(t, int32(0), calls, "an offset past the cap never calls Tempo")
}

func TestQueryTempoHeatmap_Window(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(tempoOTLPFixture))
	}))
	defer srv.Close()
	cfg := integrations.GrafanaTempoConfig{URL: srv.URL}

	// The waterfall sends only account_id + trace_id: a default 1h window would hide
	// every older trace, so no window is sent and Tempo searches all blocks.
	_, _, err := queryTempoHeatmap(cfg, TracesHeatMapRequest{AccountId: "acc", TraceId: "5b8efff798038103d269b633813fc60c"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "", gotQuery)

	now := time.Now()
	_, _, err = queryTempoHeatmap(cfg, TracesHeatMapRequest{AccountId: "acc", TraceId: "5b8efff798038103d269b633813fc60c",
		StartTime: now.Add(-2 * time.Hour).UnixMilli(), EndTime: now.UnixMilli()}, now)
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "start=")
	assert.Contains(t, gotQuery, "end=")
}

func TestQueryTempoHeatmap_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"trace":{},"metrics":{}}`))
	}))
	defer srv.Close()
	_, _, err := queryTempoHeatmap(integrations.GrafanaTempoConfig{URL: srv.URL},
		TracesHeatMapRequest{AccountId: "acc", TraceId: "abc123"}, time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trace abc123 not found in Tempo")
}

func TestQueryTempoLabelValues_UnsupportedFieldIsEmpty(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
	}))
	defer srv.Close()
	vals, _, err := queryTempoLabelValues(integrations.GrafanaTempoConfig{URL: srv.URL},
		TracesV3LabelValuesRequest{AccountId: "acc", Label: "destination_workload_namespace"}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, []string{}, vals)
	assert.Equal(t, int32(0), calls)
}
