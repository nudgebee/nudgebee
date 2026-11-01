package observability

import (
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

func TestOpenObserveMetricSource_FetchMetricsQuery(t *testing.T) {
	// Start a local HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/test-org/prometheus/api/v1/query_range", r.URL.Path)
		assert.Equal(t, "Basic dXNlcjpwYXNz", r.Header.Get("Authorization"))

		err := r.ParseForm()
		require.NoError(t, err)

		query := r.Form.Get("query")
		// The caller's own matcher survives, and every query leaving this source also
		// excludes OTel "no recorded value" markers (openobserve_stale_markers.go).
		assert.Contains(t, query, `job="prometheus"`)
		assert.Contains(t, query, `flag!="DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK"`)

		respBody := `{
			"status": "success",
			"data": {
				"resultType": "matrix",
				"result": [
					{
						"metric": {
							"__name__": "up",
							"job": "prometheus"
						},
						"values": [
							[1699999999.000, "1"]
						]
					}
				]
			}
		}`

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs, func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
		return integrations.OpenObserveConfig{URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass"}, nil
	})
	out, err := (&OpenObserveMetricSource{}).FetchMetricsQuery(mockRequestContext(), FetchMetricsRequest{AccountId: "test", Queries: map[string]string{"up": `up{job="prometheus"}`}})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	require.Nil(t, out.Results[0].Error)
	require.Len(t, out.Results[0].Payload, 1)
	// OSS metric responses use epoch milliseconds (#733); EE moved them to seconds.
	assert.Equal(t, []int64{1699999999000}, out.Results[0].Payload[0].Timestamps)
}

func TestOpenObserveMetricSource_GetSupportedOperators(t *testing.T) {
	s := &OpenObserveMetricSource{}
	ops := s.GetSupportedOperators()
	assert.Contains(t, ops, "_eq")
	assert.Contains(t, ops, "_neq")
	assert.Contains(t, ops, "_regex")
}

func TestOpenObserveMetricQueryRangeParamsConvertsMillisecondsToSeconds(t *testing.T) {
	start, end, step := openObserveMetricQueryRangeParams(FetchMetricsRequest{
		StartTime: 1700000000000,
		EndTime:   1700003600000,
	})

	assert.Equal(t, "1700000000", start)
	assert.Equal(t, "1700003600", end)
	assert.Equal(t, 36, step)
}

// The Prometheus metadata endpoints accept start/end alongside match[], and every
// FetchMetric*Request already carries the window — it was never sent. Without it
// OpenObserve picks its own range, which is why a match[]-filtered lookup came back empty
// while the same endpoint without match[] returned the full set.
func TestOpenObserveMetricMetadataQuery(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)

	q := openObserveMetricMetadataQuery(1700000000000, 1700003600000, nil, now)
	parsed, err := neturl.ParseQuery(strings.TrimPrefix(q, "?"))
	require.NoError(t, err)
	assert.Equal(t, "1700000000", parsed.Get("start"))
	assert.Equal(t, "1700003600", parsed.Get("end"))
	assert.Empty(t, parsed["match[]"])

	// An absent window must not become 0→0, which selects nothing.
	q = openObserveMetricMetadataQuery(0, 0, []string{`{__name__="up"}`}, now)
	parsed, err = neturl.ParseQuery(strings.TrimPrefix(q, "?"))
	require.NoError(t, err)
	assert.Equal(t, "1784718000", parsed.Get("start"))
	assert.Equal(t, "1784721600", parsed.Get("end"))
	assert.Equal(t, []string{`{__name__="up"}`}, parsed["match[]"])
}

// A metric name is interpolated into a PromQL matcher literal; a quote in it would
// otherwise break out of `{__name__="..."}`.
func TestEscapePromQLLabelValue(t *testing.T) {
	assert.Equal(t, "up", escapePromQLLabelValue("up"))
	assert.Equal(t, `a\"b`, escapePromQLLabelValue(`a"b`))
	assert.Equal(t, "a\\\\b", escapePromQLLabelValue("a\\b"))
}

// OpenObserve returns HTTP 400 for two conditions that are ordinary empty results, not
// failures. Verified live: the __name__ list contains families such as a summary's base name
// (acquire_shards_latency) that have no stream of their own, and a label valid on one metric
// (k8s_namespace_name) may not exist on another (kube_pod_info). Surfacing either as an error
// puts a toast in front of the user for a dropdown that simply has nothing to offer.
func TestIsOpenObserveEmptyResultError(t *testing.T) {
	streamMissing := `{"code":20002,"message":"Search stream not found: acquire_shards_latency","trace_id":"x"}`
	fieldMissing := `{"code":20004,"message":"Search field not found: Schema error: No field named k8s_namespace_name"}`

	assert.True(t, isOpenObserveEmptyResultError(streamMissing))
	assert.True(t, isOpenObserveEmptyResultError(fieldMissing))

	// Real failures must still propagate.
	assert.False(t, isOpenObserveEmptyResultError(`{"code":20008,"message":"Search SQL execute error"}`))
	assert.False(t, isOpenObserveEmptyResultError(`{"code":401,"message":"Unauthorized"}`))
	assert.False(t, isOpenObserveEmptyResultError(""))
}

// A metric stream's schema mixes real labels with the sample's own columns. Confirmed
// against a live `up` stream: 38 fields, of which __hash__ (internal series key), __name__
// (the metric already selected), _timestamp, value and start_time are not dimensions.
func TestOpenObserveMetricNonLabelColumns(t *testing.T) {
	for _, col := range []string{"__hash__", "__name__", "_timestamp", "value", "start_time"} {
		_, skip := openObserveMetricNonLabelColumns[col]
		assert.True(t, skip, "%q must not be offered as a label", col)
	}
	for _, col := range []string{"k8s_namespace_name", "k8s_pod_name", "service_name", "node", "pod"} {
		_, skip := openObserveMetricNonLabelColumns[col]
		assert.False(t, skip, "%q is a real label and must be offered", col)
	}
}

// The utilisation query builders emit a __CLUSTER__ placeholder that relay-server
// substitutes on the agent path and PrometheusSaasMetricSource expands itself. This
// path does neither, so the source has to expand it before the query leaves — an
// unexpanded token reaches OpenObserve verbatim and fails to parse.
func TestOpenObserveMetricSourceExpandsClusterPlaceholder(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		received = r.Form.Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs, func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
		return integrations.OpenObserveConfig{URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass"}, nil
	})

	_, err := (&OpenObserveMetricSource{}).FetchMetricsQuery(mockRequestContext(), FetchMetricsRequest{
		AccountId: "test",
		Queries:   map[string]string{"cpu_real": `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m]))`},
	})
	require.NoError(t, err)
	assert.NotContains(t, received, "__CLUSTER__", "placeholder must not reach OpenObserve")
	assert.Contains(t, received, `mode!="idle"`, "the rest of the selector must survive expansion")
}

// Keyword discovery is filtered in Go, not with a match[] selector, because
// OpenObserve's shim resolves match[] to a metric NAME only: a `{__name__=~".*x.*"}`
// selector returns an empty list and a selector with no literal name is a 400. Sending
// one is what made metrics_list come back empty for every keyword.
func TestOpenObserveFetchMetricListFiltersClientSide(t *testing.T) {
	var gotQuery neturl.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/test-org/prometheus/api/v1/label/__name__/values", r.URL.Path)
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[
			"container_memory_usage_bytes",
			"container_cpu_usage_seconds_total",
			"redis_memory_used_bytes",
			"Redis_Connected_Clients",
			"up"
		]}`))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs, func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
		return integrations.OpenObserveConfig{URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass"}, nil
	})

	out, err := (&OpenObserveMetricSource{}).FetchMetricList(mockRequestContext(), FetchMetricsListRequest{
		AccountId: "test",
		Metric:    "redis",
	})
	require.NoError(t, err)

	// The catalog is requested unfiltered — the only form the endpoint answers.
	assert.Empty(t, gotQuery["match[]"], "no match[] selector may be sent")

	names := make([]string, 0, len(out))
	for _, m := range out {
		names = append(names, m.Metric)
	}
	// Case-insensitive substring: callers pass a technology keyword, not an exact name.
	assert.ElementsMatch(t, []string{"redis_memory_used_bytes", "Redis_Connected_Clients"}, names)
}

func TestOpenObserveFetchMetricListEmptyKeywordReturnsCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["up","redis_memory_used_bytes"]}`))
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetOpenObserveConfigs, func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
		return integrations.OpenObserveConfig{URL: server.URL, OrgID: "test-org", Username: "user", Password: "pass"}, nil
	})

	out, err := (&OpenObserveMetricSource{}).FetchMetricList(mockRequestContext(), FetchMetricsListRequest{AccountId: "test"})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

// openObserveFetchForTest points the source at a stub server and asks one key, with or
// without Instant. Shared so the two endpoint tests differ only in the flag.
func openObserveFetchForTest(t *testing.T, serverURL string, instant bool) OutputMetricQuery {
	t.Helper()
	patches := gomonkey.NewPatches()
	t.Cleanup(patches.Reset)
	patches.ApplyFunc(integrations.GetOpenObserveConfigs, func(_ *security.RequestContext, _ string) (integrations.OpenObserveConfig, error) {
		return integrations.OpenObserveConfig{URL: serverURL, OrgID: "test-org", Username: "user", Password: "pass"}, nil
	})
	out, err := (&OpenObserveMetricSource{}).FetchMetricsQuery(mockRequestContext(), FetchMetricsRequest{
		AccountId: "test",
		Queries:   map[string]string{"container_cpu_usage_by_pod": `max_over_time(container_cpu_usage[1d:75s])`},
		StartTime: 1700000000000,
		EndTime:   1700086400000,
		Instant:   instant,
	})
	require.NoError(t, err)
	return out
}

// An Aggregate promises ONE point per series. Roughly thirty UI call sites read
// payload[0], so a key that answers with a whole series plots an arbitrary sample as the
// workload, with no error anywhere.
//
// The MECHANISM changed once it was clear OpenObserve does not evaluate the builders'
// `*_over_time` wrapper at all when the inner expression collapses to a single series --
// it answers with the value at the evaluation instant, so max, min and avg all returned
// the same number (see openobserve_local_aggregate.go). Pinning that to the instant
// endpoint produced one point, but not the statistic asked for. So the wrapper is now
// unwrapped, its inner expression fetched as a range, and the statistic computed here.
// The endpoint is therefore query_range even for an aggregate; what this test guards is
// the contract, which is unchanged: exactly one point per series.
func TestOpenObserveAggregateAnswersOnePointPerSeries(t *testing.T) {
	var gotPath, gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotPath, gotForm = r.URL.Path, r.Form.Encode()
		w.WriteHeader(http.StatusOK)
		// The instant shape: `value`, not `values`.
		_, _ = w.Write([]byte(`{"data":{"result":[{"metric":{"pod":"web-1"},"value":[1700000000,"0.25"]}]}}`))
	}))
	defer srv.Close()

	res := openObserveFetchForTest(t, srv.URL, true)
	assert.Contains(t, gotPath, "query_range",
		"the inner expression is fetched as a range and reduced here")
	assert.Contains(t, gotForm, "step=")
	// The wrapper must NOT reach OpenObserve: it is the thing the engine ignores, and
	// sending it is what made max, min and avg agree.
	assert.NotContains(t, gotForm, "over_time", "the wrapper is reduced locally, not sent")

	require.Len(t, res.Results, 1)
	require.Len(t, res.Results[0].Payload, 1, "one series")
	assert.Equal(t, []float64{0.25}, res.Results[0].Payload[0].Values,
		"collapsed to one point; the instant `value` shape must still decode")
	// And the caller still sees which statistic it asked for, not the unwrapped query.
	assert.Contains(t, res.Results[0].Query, "max_over_time")
}

// Without an aggregate nothing changes: a chart still wants the series.
func TestOpenObserveRangeStillUsesQueryRange(t *testing.T) {
	var gotPath, gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotPath, gotForm = r.URL.Path, r.Form.Encode()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"result":[{"metric":{"pod":"web-1"},"values":[[1700000000,"0.25"],[1700000075,"0.30"]]}]}}`))
	}))
	defer srv.Close()

	res := openObserveFetchForTest(t, srv.URL, false)
	assert.Contains(t, gotPath, "query_range")
	assert.Contains(t, gotForm, "step=")
	require.Len(t, res.Results, 1)
	assert.Equal(t, []float64{0.25, 0.30}, res.Results[0].Payload[0].Values)
}
