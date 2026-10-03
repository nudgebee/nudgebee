package observability

import (
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requestRecorder collects the requests the source made. FetchMetricSeries probes
// its candidates concurrently, so the handler runs on several goroutines at once
// and the slice must be guarded — without the lock this is a data race that only
// surfaces under `go test -race`.
type requestRecorder struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (r *requestRecorder) add(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

// all returns a copy so callers can index it without holding the lock.
func (r *requestRecorder) all() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.requests...)
}

// first returns the single request the caller expects, failing the test if the
// source made none.
func (r *requestRecorder) first(t *testing.T) *http.Request {
	t.Helper()
	got := r.all()
	require.NotEmpty(t, got, "expected the source to issue a request")
	return got[0]
}

// newPrometheusSaasServer stands up a fake Prometheus and points the source's
// config loader at it for the duration of the test. Every request the source
// makes is recorded so param construction can be asserted.
func newPrometheusSaasServer(t *testing.T, handler http.HandlerFunc) *requestRecorder {
	t.Helper()

	recorder := &requestRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.add(r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	original := loadPrometheusUserConfig
	loadPrometheusUserConfig = func(*security.RequestContext, string) (integrations.PrometheusUserConfig, error) {
		return integrations.PrometheusUserConfig{URL: server.URL, AuthType: integrations.PrometheusAuthNone}, nil
	}
	t.Cleanup(func() { loadPrometheusUserConfig = original })

	return recorder
}

func testRequestContext() *security.RequestContext {
	return &security.RequestContext{}
}

// ----- range and instant queries -------------------------------------------

func TestPrometheusSaas_FetchMetricsQuery_Range(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{"resultType":"matrix","result":[
				{"metric":{"__name__":"up","job":"api"},"values":[[1700000000,"1"],[1700000060,"0.5"]]}
			]}
		}`))
	})

	out, err := (&PrometheusSaasMetricSource{}).FetchMetricsQuery(testRequestContext(), FetchMetricsRequest{
		AccountId:    "acc",
		Queries:      map[string]string{"panel": "up"},
		StartTime:    1700000000000,
		EndTime:      1700000060000,
		StepInterval: 30,
	})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)

	result := out.Results[0]
	assert.Equal(t, "panel", result.QueryKey)
	assert.Nil(t, result.Error)
	require.Len(t, result.Payload, 1)
	assert.Equal(t, map[string]string{"__name__": "up", "job": "api"}, result.Payload[0].Metric)
	// Timestamps stay in unix SECONDS — the same units the agent-relayed source
	// returns, which is what the charts render.
	assert.Equal(t, []int64{1700000000, 1700000060}, result.Payload[0].Timestamps)
	assert.Equal(t, []float64{1, 0.5}, result.Payload[0].Values)

	require.Len(t, recorder.all(), 1)
	got := recorder.first(t)
	assert.Equal(t, "/api/v1/query_range", got.URL.Path)
	assert.Equal(t, "up", got.URL.Query().Get("query"))
	assert.Equal(t, "1700000000", got.URL.Query().Get("start"))
	assert.Equal(t, "1700000060", got.URL.Query().Get("end"))
	assert.Equal(t, "30", got.URL.Query().Get("step"))
}

func TestPrometheusSaas_FetchMetricsQuery_Instant(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{"resultType":"vector","result":[
				{"metric":{"__name__":"up"},"value":[1700000060,"1"]}
			]}
		}`))
	})

	out, err := (&PrometheusSaasMetricSource{}).FetchMetricsQuery(testRequestContext(), FetchMetricsRequest{
		AccountId: "acc",
		Queries:   map[string]string{"panel": "up"},
		StartTime: 1700000000000,
		EndTime:   1700000060000,
		Instant:   true,
	})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	require.Len(t, out.Results[0].Payload, 1)
	assert.Equal(t, []int64{1700000060}, out.Results[0].Payload[0].Timestamps)
	assert.Equal(t, []float64{1}, out.Results[0].Payload[0].Values)

	got := recorder.first(t)
	assert.Equal(t, "/api/v1/query", got.URL.Path)
	// The instant sample the UI wants is the one at the END of the window.
	assert.Equal(t, "1700000060", got.URL.Query().Get("time"))
}

// The Validate-Query button sends `instant` in the request bag rather than the
// typed field; the agent source honours it and so must this one.
func TestPrometheusSaas_FetchMetricsQuery_InstantViaRequestOverride(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	})

	_, err := (&PrometheusSaasMetricSource{}).FetchMetricsQuery(testRequestContext(), FetchMetricsRequest{
		AccountId: "acc",
		Queries:   map[string]string{"panel": "up"},
		EndTime:   1700000060000,
		Request:   map[string]any{"instant": true},
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/query", recorder.first(t).URL.Path)
}

func TestPrometheusSaas_FetchMetricsQuery_AppliesMatchersAndAggregator(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	})

	out, err := (&PrometheusSaasMetricSource{}).FetchMetricsQuery(testRequestContext(), FetchMetricsRequest{
		AccountId:  "acc",
		Queries:    map[string]string{"panel": "up"},
		Labels:     map[string]string{"namespace": "prod"},
		QueryItems: map[string]QueryItem{"panel": {Metric: "up", AggregateOperator: "sum"}},
		StartTime:  1700000000000,
		EndTime:    1700000060000,
	})
	require.NoError(t, err)

	sent := recorder.first(t).URL.Query().Get("query")
	assert.Contains(t, sent, `namespace="prod"`)
	assert.True(t, len(sent) > 4 && sent[:4] == "sum(", "expected the aggregator wrap, got %q", sent)
	assert.Equal(t, sent, out.Results[0].Query, "the executed query is reported back")
}

// A dashboard sends a batch of queries and renders each panel independently, so
// one failure must not discard the others' results.
func TestPrometheusSaas_FetchMetricsQuery_ErrorIsolatedPerQuery(t *testing.T) {
	newPrometheusSaasServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"2"]]}]}
		}`))
	})

	out, err := (&PrometheusSaasMetricSource{}).FetchMetricsQuery(testRequestContext(), FetchMetricsRequest{
		AccountId: "acc",
		Queries:   map[string]string{"good": "up", "bad": "bad"},
		StartTime: 1700000000000,
		EndTime:   1700000060000,
	})
	require.NoError(t, err)
	require.Len(t, out.Results, 2)

	byKey := map[string]QueryResult{}
	for _, result := range out.Results {
		byKey[result.QueryKey] = result
	}
	require.NotNil(t, byKey["bad"].Error)
	assert.Contains(t, *byKey["bad"].Error, "parse error")
	assert.Nil(t, byKey["good"].Error)
	assert.Len(t, byKey["good"].Payload, 1)
}

// ----- catalogue lookups ----------------------------------------------------

func TestPrometheusSaas_FetchMetricList(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["up","node_cpu_seconds_total"]}`))
	})

	metrics, err := (&PrometheusSaasMetricSource{}).FetchMetricList(testRequestContext(), FetchMetricsListRequest{
		AccountId: "acc",
		Metric:    "cpu",
		StartTime: 1700000000000,
		EndTime:   1700000060000,
	})
	require.NoError(t, err)
	assert.Equal(t, []OutputMetrics{
		{Metric: "up", Attributes: map[string]any{}},
		{Metric: "node_cpu_seconds_total", Attributes: map[string]any{}},
	}, metrics)

	got := recorder.first(t)
	assert.Equal(t, "/api/v1/label/__name__/values", got.URL.Path)
	assert.Equal(t, `{__name__=~".*cpu.*"}`, got.URL.Query().Get("match[]"))
}

func TestPrometheusSaas_FetchMetricsLabels(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["job","namespace"]}`))
	})

	labels, err := (&PrometheusSaasMetricSource{}).FetchMetricsLabels(testRequestContext(), FetchMetricLabelsRequest{
		AccountId:  "acc",
		MetricName: "up",
		StartTime:  1700000000000,
		EndTime:    1700000060000,
	})
	require.NoError(t, err)
	assert.Equal(t, []OutputMetricLabels{
		{Label: "job", Attributes: map[string]any{}},
		{Label: "namespace", Attributes: map[string]any{}},
	}, labels)

	got := recorder.first(t)
	assert.Equal(t, "/api/v1/labels", got.URL.Path)
	assert.Equal(t, `{__name__="up"}`, got.URL.Query().Get("match[]"))
}

func TestPrometheusSaas_FetchMetricLabelValues(t *testing.T) {
	recorder := newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["api","web"]}`))
	})

	values, err := (&PrometheusSaasMetricSource{}).FetchMetricLabelValues(testRequestContext(), FetchMetricsLabelValueRequest{
		AccountId: "acc",
		Label:     "job",
		StartTime: 1700000000000,
		EndTime:   1700000060000,
		Request:   map[string]any{"metric": "up"},
	})
	require.NoError(t, err)
	assert.Equal(t, []OutputMetricsLabelValues{
		{Value: "api", Attributes: map[string]any{}},
		{Value: "web", Attributes: map[string]any{}},
	}, values)

	got := recorder.first(t)
	assert.Equal(t, "/api/v1/label/job/values", got.URL.Path)
	assert.Equal(t, `{__name__="up"}`, got.URL.Query().Get("match[]"))
}

func TestPrometheusSaas_FetchMetricLabelValues_RequiresLabel(t *testing.T) {
	newPrometheusSaasServer(t, func(http.ResponseWriter, *http.Request) {})
	_, err := (&PrometheusSaasMetricSource{}).FetchMetricLabelValues(testRequestContext(), FetchMetricsLabelValueRequest{AccountId: "acc"})
	require.Error(t, err)
}

// A `status: error` body carries HTTP 200 on some engines, so the envelope must
// be inspected rather than trusting the status code alone.
func TestPrometheusSaas_SurfacesErrorEnvelope(t *testing.T) {
	newPrometheusSaasServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid selector"}`))
	})

	_, err := (&PrometheusSaasMetricSource{}).FetchMetricList(testRequestContext(), FetchMetricsListRequest{AccountId: "acc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid selector")
}

// ----- series discovery -----------------------------------------------------

func TestPrometheusSaas_FetchMetricSeries(t *testing.T) {
	newPrometheusSaasServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Only the pod-prefix candidate matches anything.
		if selector := r.URL.Query().Get("match[]"); selector == `{namespace="prod",pod=~"checkout.*"}` {
			_, _ = w.Write([]byte(`{"status":"success","data":["http_requests_total","up"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	})

	out, err := (&PrometheusSaasMetricSource{}).FetchMetricSeries(testRequestContext(), FetchMetricSeriesRequest{
		AccountId: "acc",
		Namespace: "prod",
		Workload:  "checkout",
		StartTime: 1700000000,
		EndTime:   1700003600,
	})
	require.NoError(t, err)
	require.Len(t, out.Matches, 1)
	assert.Equal(t, "namespace", out.Matches[0].NamespaceLabel)
	assert.Equal(t, "pod", out.Matches[0].WorkloadLabel)
	assert.Equal(t, []string{"http_requests_total", "up"}, out.Matches[0].Families)
	assert.False(t, out.Truncated)
}

// The shared orchestration must keep the agent source's partial-failure policy:
// results survive as long as at least one candidate lookup succeeded.
func TestFetchMetricSeriesCommon_PartialFailure(t *testing.T) {
	req := FetchMetricSeriesRequest{AccountId: "acc", Namespace: "prod", Workload: "checkout"}

	t.Run("one candidate succeeding is enough", func(t *testing.T) {
		out, err := fetchMetricSeriesCommon(testRequestContext(), req, func(selector string, _, _ int64, _ int) ([]string, bool, error) {
			if selector == `{namespace="prod",job="checkout"}` {
				return []string{"up"}, false, nil
			}
			return nil, false, assert.AnError
		})
		require.NoError(t, err)
		require.Len(t, out.Matches, 1)
		assert.Equal(t, "job", out.Matches[0].WorkloadLabel)
	})

	t.Run("every candidate failing surfaces the error", func(t *testing.T) {
		_, err := fetchMetricSeriesCommon(testRequestContext(), req, func(string, int64, int64, int) ([]string, bool, error) {
			return nil, false, assert.AnError
		})
		require.Error(t, err)
	})

	t.Run("truncation is ORed across candidates", func(t *testing.T) {
		out, err := fetchMetricSeriesCommon(testRequestContext(), req, func(selector string, _, _ int64, _ int) ([]string, bool, error) {
			if selector == `{namespace="prod",pod=~"checkout.*"}` {
				return []string{"up"}, true, nil
			}
			return nil, false, nil
		})
		require.NoError(t, err)
		assert.True(t, out.Truncated)
	})
}

// ----- parsing --------------------------------------------------------------

func TestParsePrometheusMatrixOrVector(t *testing.T) {
	t.Run("skips unparseable samples", func(t *testing.T) {
		results := parsePrometheusMatrixOrVector(map[string]any{
			"data": map[string]any{"result": []any{
				map[string]any{
					"metric": map[string]any{"job": "api"},
					"values": []any{
						[]any{float64(1700000000), "1"},
						[]any{float64(1700000060)},        // malformed pair
						[]any{float64(1700000120), "abc"}, // unparseable value
					},
				},
			}},
		})
		require.Len(t, results, 1)
		assert.Equal(t, []int64{1700000000}, results[0].Timestamps)
		assert.Equal(t, []float64{1}, results[0].Values)
	})

	// NaN cannot round-trip through the JSON the API layer emits, so it is
	// normalised to 0 exactly as the agent source does.
	t.Run("NaN becomes zero", func(t *testing.T) {
		results := parsePrometheusMatrixOrVector(map[string]any{
			"data": map[string]any{"result": []any{
				map[string]any{"metric": map[string]any{}, "value": []any{float64(1700000000), "NaN"}},
			}},
		})
		require.Len(t, results, 1)
		assert.Equal(t, []float64{0}, results[0].Values)
	})

	t.Run("empty result", func(t *testing.T) {
		assert.Empty(t, parsePrometheusMatrixOrVector(map[string]any{
			"data": map[string]any{"result": []any{}},
		}))
	})
}

func TestPrometheusSaas_GetSupportedOperators_MatchesAgentSource(t *testing.T) {
	assert.Equal(t,
		(&PrometheusMetricSource{}).GetSupportedOperators(),
		(&PrometheusSaasMetricSource{}).GetSupportedOperators(),
		"the two sources answer the same PromQL, so they must advertise the same operators")
}

func TestPrometheusSaas_GetQuery(t *testing.T) {
	got, err := (&PrometheusSaasMetricSource{}).GetQuery(testRequestContext(), FetchMetricsRequest{
		Queries: map[string]string{"panel": "up"},
		Labels:  map[string]string{"namespace": "prod"},
	})
	require.NoError(t, err)
	assert.Contains(t, got, `namespace="prod"`)
}

// The user-source provider must resolve to the direct client, and the agent one
// must keep resolving to the relayed client.
func TestGetMetricsSource_PrometheusBySource(t *testing.T) {
	userSource, err := getMetricsSource("prometheus", "user")
	require.NoError(t, err)
	assert.IsType(t, &PrometheusSaasMetricSource{}, userSource)

	agentSource, err := getMetricsSource("prometheus", "agent")
	require.NoError(t, err)
	assert.IsType(t, &PrometheusMetricSource{}, agentSource)
}
