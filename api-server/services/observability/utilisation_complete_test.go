package observability

import (
	"errors"
	"testing"
	"time"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resultFor(t *testing.T, results []QueryResult, key string) QueryResult {
	t.Helper()
	var found []QueryResult
	for _, r := range results {
		if r.QueryKey == key {
			found = append(found, r)
		}
	}
	require.Len(t, found, 1, "exactly one entry for %q", key)
	return found[0]
}

func TestCompleteUtilisationResults_KeepsAnExistingEntryAsItIs(t *testing.T) {
	own := QueryResult{QueryKey: "container_memory_max_usage_by_pod", Payload: []Result{}, Note: "Datadog reports no cgroup high-water mark."}
	got := completeUtilisationResults([]QueryResult{own}, []string{"container_memory_max_usage_by_pod"}, nil, "datadog", esScopeWorkload)

	require.Len(t, got, 1)
	assert.Equal(t, own, got[0])
}

func TestCompleteUtilisationResults_RenderedButAbsentIsNoDataNotUnsupported(t *testing.T) {
	got := completeUtilisationResults(nil, []string{"cpu_usage"}, map[string]string{"cpu_usage": "sum(rate(x[5m]))"}, "prometheus", esScopeWorkload)

	r := resultFor(t, got, "cpu_usage")
	assert.Empty(t, r.Note, "the provider can answer this key; it had no data")
	assert.Equal(t, "sum(rate(x[5m]))", r.Query, "the query that ran is kept so the empty answer can be checked")
	assert.NotNil(t, r.Payload, "an empty payload marshals as [], not null")
	assert.Empty(t, r.Payload)
}

func TestCompleteUtilisationResults_UnrenderedKeyIsNotedWithProviderAndScope(t *testing.T) {
	got := completeUtilisationResults(nil, []string{"p90_cpu"}, map[string]string{}, "datadog", esScopeNode)

	r := resultFor(t, got, "p90_cpu")
	assert.Equal(t, `datadog does not answer utilisation key "p90_cpu" at node scope`, r.Note)
	assert.Empty(t, r.Query)
	assert.NotNil(t, r.Payload)
	assert.Empty(t, r.Payload)
}

func TestCompleteUtilisationResults_NilRenderedMapNotesEveryAbsentKey(t *testing.T) {
	present := QueryResult{QueryKey: "cpu_usage", Payload: []Result{{Values: []float64{1}}}}
	got := completeUtilisationResults([]QueryResult{present}, []string{"cpu_usage", "jvm_memory_metric_count"}, nil, "ES", esScopeWorkload)

	require.Len(t, got, 2)
	assert.Equal(t, present, resultFor(t, got, "cpu_usage"))
	assert.Contains(t, resultFor(t, got, "jvm_memory_metric_count").Note, "ES does not answer")
}

func TestCompleteUtilisationResults_KeyRequestedTwiceGetsOneEntry(t *testing.T) {
	got := completeUtilisationResults(nil, []string{"p90_cpu", "p90_cpu", ""}, nil, "solarwinds", esScopeWorkload)

	require.Len(t, got, 1, "duplicates collapse and an empty key asks for nothing")
	assert.Equal(t, "p90_cpu", got[0].QueryKey)
}

func TestCompleteUtilisationResults_EntryWithoutAKeyAnswersNothing(t *testing.T) {
	// An unfilled slot in Elasticsearch's pre-sized results must not count as an answer.
	got := completeUtilisationResults([]QueryResult{{}}, []string{"cpu_usage"}, nil, "ES", esScopeWorkload)

	require.Len(t, got, 2)
	assert.Contains(t, resultFor(t, got, "cpu_usage").Note, "does not answer")
}

// omittingSource drops the keys in omit, as the relay does for keys that returned nothing.
type omittingSource struct {
	MetricSource
	omit    map[string]bool
	queries map[string]string
}

func (o *omittingSource) FetchMetricsQuery(_ *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	o.queries = req.Queries
	out := OutputMetricQuery{}
	for key, query := range req.Queries {
		if o.omit[key] {
			continue
		}
		out.Results = append(out.Results, QueryResult{QueryKey: key, Query: query, Payload: []Result{{Values: []float64{0.5}}}})
	}
	return out, nil
}

type erroringSource struct{ MetricSource }

func (erroringSource) FetchMetricsQuery(*security.RequestContext, FetchMetricsRequest) (OutputMetricQuery, error) {
	return OutputMetricQuery{}, errors.New("relay: unable to execute relay query")
}

func nodeUtilisationRequest(metrics ...any) GetUtilisationTrendRequest {
	end := time.Now().UTC()
	return GetUtilisationTrendRequest{
		AccountId: "acc",
		StartTime: end.Add(-time.Hour).UnixMilli(),
		EndTime:   end.UnixMilli(),
		Request: map[string]any{
			"kind":      "node",
			"node_name": "node-a",
			"metrics":   metrics,
		},
	}
}

func TestFetchMetricUtilisation_KeyTheBuilderCannotRenderIsNoted(t *testing.T) {
	stubUtilisationProvider(t, "prometheus", "agent")
	source := &fakeMetricSource{}
	stubMetricSource(t, source)

	out, err := FetchMetricUtilisation(testRequestContext(), nodeUtilisationRequest("cpu_usage", "container_top_cpu_usage"))
	require.NoError(t, err)

	assert.NotContains(t, source.queries, "container_top_cpu_usage", "precondition: the node builder renders no query for it")
	require.Len(t, out.Results, 2)
	assert.Empty(t, resultFor(t, out.Results, "cpu_usage").Note)
	assert.Equal(t, `prometheus does not answer utilisation key "container_top_cpu_usage" at node scope`,
		resultFor(t, out.Results, "container_top_cpu_usage").Note)
}

func TestFetchMetricUtilisation_RenderedKeyTheSourceDroppedIsEmptyNotNoted(t *testing.T) {
	stubUtilisationProvider(t, "prometheus", "agent")
	source := &omittingSource{omit: map[string]bool{"memory_usage": true}}
	stubMetricSource(t, source)

	out, err := FetchMetricUtilisation(testRequestContext(), workloadUtilisationRequest("cpu_usage", "memory_usage"))
	require.NoError(t, err)

	require.Len(t, out.Results, 2)
	mem := resultFor(t, out.Results, "memory_usage")
	assert.Empty(t, mem.Note, "rendered means the provider can answer it; an absent entry is no data")
	assert.Empty(t, mem.Payload)
	assert.Equal(t, source.queries["memory_usage"], mem.Query)
}

func TestFetchMetricUtilisation_ProviderOwnNoteIsNotDuplicated(t *testing.T) {
	stubUtilisationProvider(t, "datadog", "user")
	stubMetricSource(t, &fakeMetricSource{})

	out, err := FetchMetricUtilisation(testRequestContext(), workloadUtilisationRequest("container_memory_max_usage_by_pod"))
	require.NoError(t, err)

	assert.Equal(t, datadogUnanswerableKeys["container_memory_max_usage_by_pod"],
		resultFor(t, out.Results, "container_memory_max_usage_by_pod").Note)
}

func TestFetchMetricUtilisation_FailedRequestIsNotCompleted(t *testing.T) {
	// A failed request stays failed rather than being filled with Notes.
	stubUtilisationProvider(t, "prometheus", "agent")
	stubMetricSource(t, erroringSource{})

	out, err := FetchMetricUtilisation(testRequestContext(), workloadUtilisationRequest("cpu_usage", "container_top_cpu_usage"))
	require.Error(t, err)
	assert.Empty(t, out.Results)
}

func TestCompleteUtilisationResults_NoProviderSaysSoInsteadOfNamingOne(t *testing.T) {
	got := completeUtilisationResults(nil, []string{"cpu_usage"}, nil, "", esScopeWorkload)

	assert.Equal(t, "no metrics provider resolved for this account", resultFor(t, got, "cpu_usage").Note)
}

func TestFetchMetricUtilisation_NoProviderAnswersEveryKeyWithAReason(t *testing.T) {
	stubUtilisationProvider(t, "", "")

	out, err := FetchMetricUtilisation(testRequestContext(), workloadUtilisationRequest("cpu_usage", "memory_usage"))
	require.NoError(t, err)

	require.Len(t, out.Results, 2)
	for _, key := range []string{"cpu_usage", "memory_usage"} {
		r := resultFor(t, out.Results, key)
		assert.Equal(t, "no metrics provider resolved for this account", r.Note)
		assert.NotNil(t, r.Payload)
		assert.Empty(t, r.Payload)
	}
}

func TestFetchMetricUtilisation_NoProviderMalformedRequestFailsLikeAnyOther(t *testing.T) {
	stubUtilisationProvider(t, "", "")
	req := workloadUtilisationRequest()
	delete(req.Request, "metrics")

	out, err := FetchMetricUtilisation(testRequestContext(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics")
	assert.Empty(t, out.Results)
}
