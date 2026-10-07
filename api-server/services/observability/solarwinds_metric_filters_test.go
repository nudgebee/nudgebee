package observability

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Label matchers become SWO filter terms, or the request is refused
// ============================================================================

func TestSWMetricMatchersFilter(t *testing.T) {
	cases := []struct {
		name     string
		matchers []LabelMatcher
		want     string
	}{
		{"eq is exact", []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_eq", Value: "shop"}}, "k8s.namespace.name:shop"},
		{"eq with a trailing wildcard", []LabelMatcher{{Label: "k8s.pod.name", Operator: "_eq", Value: "web-*"}}, "k8s.pod.name:web-*"},
		{"eq value with a space is quoted", []LabelMatcher{{Label: "team", Operator: "_eq", Value: "a b"}}, `team:"a b"`},
		{"in exact values is a bracket list", []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_in", Value: "shop, cart"}}, "k8s.namespace.name:[shop,cart]"},
		{"in with a wildcard is an OR group", []LabelMatcher{{Label: "k8s.pod.name", Operator: "_in", Value: "web-*,api"}}, "(k8s.pod.name:web-* OR k8s.pod.name:api)"},
		{"neq is NOT", []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_neq", Value: "shop"}}, "NOT k8s.namespace.name:shop"},
		{"not_in is one NOT per value", []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_not_in", Value: "shop,cart"}}, "NOT k8s.namespace.name:shop NOT k8s.namespace.name:cart"},
		{"several matchers are ANDed by a space", []LabelMatcher{
			{Label: "k8s.namespace.name", Operator: "_eq", Value: "shop"},
			{Label: "k8s.pod.name", Operator: "_neq", Value: "web-1"},
		}, "k8s.namespace.name:shop NOT k8s.pod.name:web-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := swMetricMatchersFilter(tc.matchers, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSWMetricMatchersFilter_LegacyLabelsAreExact(t *testing.T) {
	got, err := swMetricMatchersFilter(nil, map[string]string{"k8s.pod.name": "web-1", "k8s.namespace.name": "shop"})
	require.NoError(t, err)
	assert.Equal(t, "k8s.namespace.name:shop k8s.pod.name:web-1", got)
}

func TestSWMetricMatchersFilter_RefusesWhatSWOCannotSay(t *testing.T) {
	cases := map[string]LabelMatcher{
		// NOT with a wildcard is accepted by SWO and silently matches everything.
		"negated wildcard":    {Label: "k8s.pod.name", Operator: "_neq", Value: "web-*"},
		"negated list glob":   {Label: "k8s.pod.name", Operator: "_not_in", Value: "a,web-*"},
		"leading wildcard":    {Label: "k8s.pod.name", Operator: "_eq", Value: "*-web"},
		"unsupported regex":   {Label: "k8s.pod.name", Operator: "_regex", Value: "web-.*"},
		"unsupported compare": {Label: "k8s.pod.name", Operator: "_gt", Value: "1"},
		"empty value":         {Label: "k8s.pod.name", Operator: "_eq", Value: " "},
		"empty label":         {Label: "", Operator: "_eq", Value: "x"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := swMetricMatchersFilter([]LabelMatcher{m}, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SolarWinds")
		})
	}
}

// ============================================================================
// Result shape
// ============================================================================

func TestSWGroupingsToQueryResult_NoMatchIsZeroSeries(t *testing.T) {
	// What SWO returns for a filter that matches nothing.
	qr := swGroupingsToQueryResult([]swMeasurementGrouping{{Attributes: []swMeasurementAttribute{}, Measurements: []swBucketMeasurement{}}}, "k", "m", false)
	assert.Empty(t, qr.Payload)
}

func TestSWGroupingsToQueryResult_WorkloadFirstNonEmptyWins(t *testing.T) {
	v := 1.0
	for _, attrs := range [][]swMeasurementAttribute{
		{{Key: "k8s.deployment.name", Value: "web"}, {Key: "k8s.statefulset.name", Value: ""}},
		{{Key: "k8s.statefulset.name", Value: ""}, {Key: "k8s.deployment.name", Value: "web"}},
	} {
		qr := swGroupingsToQueryResult([]swMeasurementGrouping{{Attributes: attrs, Measurements: []swBucketMeasurement{{Time: swTestISO2023, Value: &v}}}}, "k", "m", false)
		require.Len(t, qr.Payload, 1)
		assert.Equal(t, "web", qr.Payload[0].Metric["workload"])
	}
}

// ============================================================================
// Response payload too large
// ============================================================================

func TestFetchAllMeasurements_PayloadTooLargeHalvesPageSize(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var tried []string
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		tried = append(tried, params["pageSize"])
		if params["pageSize"] != "25" {
			return []byte(`{"message":"Response payload too large"}`), http.StatusInternalServerError, nil
		}
		return []byte(`{"groupings":[{"attributes":[],"measurements":[]}],"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
	}

	groupings, _, err := fetchAllMeasurements("tok", swTestBaseURL, "/v1/metrics/m/measurements", swMeasurementQuery{aggregateBy: "AVG"})
	require.NoError(t, err)
	assert.Len(t, groupings, 1)
	assert.Equal(t, []string{"100", "50", "25"}, tried)
}

// A page refused mid-way restarts from the first page at the smaller size: nothing says a
// skipToken from one page size addresses the same position at another.
func TestFetchAllMeasurements_PayloadTooLargeRestartsFromFirstPage(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	type call struct{ size, token string }
	var calls []call
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		calls = append(calls, call{params["pageSize"], params["skipToken"]})
		switch {
		case params["pageSize"] == "100" && params["skipToken"] == "":
			return []byte(`{"groupings":[{"attributes":[{"key":"k8s.pod.name","value":"a"}],"measurements":[]}],"pageInfo":{"nextPage":"https://x/m?skipToken=t100"}}`), http.StatusOK, nil
		case params["pageSize"] == "100":
			return []byte(`{"message":"Response payload too large"}`), http.StatusInternalServerError, nil
		case params["skipToken"] == "":
			return []byte(`{"groupings":[{"attributes":[{"key":"k8s.pod.name","value":"a"}],"measurements":[]}],"pageInfo":{"nextPage":"https://x/m?skipToken=t50"}}`), http.StatusOK, nil
		default:
			return []byte(`{"groupings":[{"attributes":[{"key":"k8s.pod.name","value":"b"}],"measurements":[]}],"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
		}
	}

	groupings, _, err := fetchAllMeasurements("tok", swTestBaseURL, "/v1/metrics/m/measurements", swMeasurementQuery{aggregateBy: "AVG"})
	require.NoError(t, err)
	assert.Equal(t, []call{{"100", ""}, {"100", "t100"}, {"50", ""}, {"50", "t50"}}, calls)
	require.Len(t, groupings, 2, "the first attempt's page is discarded, not kept twice")
	assert.Equal(t, "a", groupings[0].Attributes[0].Value)
	assert.Equal(t, "b", groupings[1].Attributes[0].Value)
}

func TestFetchAllMeasurements_PayloadTooLargeAtFloorSaysWhatToDo(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var tried []string
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		tried = append(tried, params["pageSize"])
		return []byte(`{"message":"Response payload too large"}`), http.StatusInternalServerError, nil
	}

	_, _, err := fetchAllMeasurements("tok", swTestBaseURL, "/v1/metrics/m/measurements", swMeasurementQuery{aggregateBy: "AVG"})
	require.Error(t, err)
	assert.Equal(t, []string{"100", "50", "25", "12", "10"}, tried)
	assert.Contains(t, err.Error(), "narrower window")
}

// ============================================================================
// Label discovery reads the attributes endpoints, for the window asked about
// ============================================================================

func TestSWFetchMetricAttributeNames_UsesAttributesEndpointAndWindow(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var paths []string
	var sent []map[string]string
	solarWindsDoGET = func(_, _, path string, params map[string]string) ([]byte, int, error) {
		paths = append(paths, path)
		cp := map[string]string{}
		for k, v := range params {
			cp[k] = v
		}
		sent = append(sent, cp)
		if params["skipToken"] == "" {
			return []byte(`{"names":["k8s.pod.name","k8s.namespace.name"],"pageInfo":{"nextPage":"https://x/v1/metrics/m/attributes?skipToken=p2"}}`), http.StatusOK, nil
		}
		return []byte(`{"names":["k8s.node.name"],"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
	}

	names, err := swFetchMetricAttributeNames("tok", swTestBaseURL, "k8s.pod.cpu.usage.seconds.rate", "2026-10-07T09:00:00Z", "2026-10-07T10:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, []string{"k8s.pod.name", "k8s.namespace.name", "k8s.node.name"}, names)
	assert.Equal(t, "/v1/metrics/k8s.pod.cpu.usage.seconds.rate/attributes", paths[0])
	assert.Equal(t, "2026-10-07T09:00:00Z", sent[0]["startTime"])
	assert.Equal(t, "2026-10-07T10:00:00Z", sent[0]["endTime"])
	assert.Equal(t, "p2", sent[1]["skipToken"])
}

func TestSWFetchMetricAttributeValues_PassesWindow(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var sentPath string
	var sent map[string]string
	solarWindsDoGET = func(_, _, path string, params map[string]string) ([]byte, int, error) {
		sentPath, sent = path, params
		return []byte(`{"name":"k8s.node.name","values":["n1","","n2"],"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
	}

	values, err := swFetchMetricAttributeValues("tok", swTestBaseURL, "m", "k8s.node.name", "2026-10-07T09:00:00Z", "2026-10-07T10:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, []string{"n1", "n2"}, values)
	assert.Equal(t, "/v1/metrics/m/attributes/k8s.node.name", sentPath)
	assert.Equal(t, "2026-10-07T09:00:00Z", sent["startTime"])
}

// ============================================================================
// The rendered query is SWO's own parameters, and it runs as written
// ============================================================================

func TestSolarWindsGetQuery_RendersRequestParameters(t *testing.T) {
	src := &SolarWindsMetricSource{}
	got, err := src.GetQuery(nil, FetchMetricsRequest{
		Queries:       map[string]string{"a": "k8s.pod.cpu.usage.seconds.rate"},
		LabelMatchers: []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_in", Value: "shop,cart"}},
		Request:       map[string]any{"groupBy": "k8s.pod.name"},
	})
	require.NoError(t, err)
	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate | filter: k8s.namespace.name:[shop,cart] | groupBy: k8s.pod.name | aggregateBy: AVG", got)
}

func TestSolarWindsGetQuery_RefusesUnsupportedMatcher(t *testing.T) {
	src := &SolarWindsMetricSource{}
	_, err := src.GetQuery(nil, FetchMetricsRequest{
		Queries:       map[string]string{"a": "m"},
		LabelMatchers: []LabelMatcher{{Label: "k8s.pod.name", Operator: "_regex", Value: ".*"}},
	})
	assert.Error(t, err)
}

func TestSolarWindsWrapAggregate_IsNotPromQL(t *testing.T) {
	src := &SolarWindsMetricSource{}
	got, err := wrapAggregator(src, "m | filter: k8s.namespace.name:shop | aggregateBy: AVG", "max")
	require.NoError(t, err)
	assert.Equal(t, "m | filter: k8s.namespace.name:shop | aggregateBy: MAX", got)
	assert.False(t, strings.HasPrefix(got, "max("))

	same, err := wrapAggregator(src, "m", "")
	require.NoError(t, err)
	assert.Equal(t, "m", same)

	_, err = wrapAggregator(src, "m", "stddev")
	assert.Error(t, err)
}

func TestSWApplyQueryText_RoundTrip(t *testing.T) {
	base := swMeasurementQuery{aggregateBy: "AVG", filter: "k8s.cluster.name:c1"}
	name, q, err := swApplyQueryText("k8s.pod.memory.working_set | filter: k8s.namespace.name:shop | groupBy: k8s.pod.name | aggregateBy: MAX", base)
	require.NoError(t, err)
	assert.Equal(t, "k8s.pod.memory.working_set", name)
	assert.Equal(t, "k8s.namespace.name:shop", q.filter, "the text says the whole filter, as rendered")
	assert.Equal(t, "k8s.pod.name", q.groupBy)
	assert.Equal(t, "MAX", q.aggregateBy)

	plain, q2, err := swApplyQueryText("k8s.pod.memory.working_set", base)
	require.NoError(t, err)
	assert.Equal(t, "k8s.pod.memory.working_set", plain)
	assert.Equal(t, base, q2, "a bare metric name keeps the request's parameters")

	_, _, err = swApplyQueryText("m | window: 5m", base)
	assert.Error(t, err, "an unknown parameter is refused, not ignored")
}

func TestSWQueryText_RoundTripsAnyValue(t *testing.T) {
	cases := map[string]swMeasurementQuery{
		"pipe inside a quoted value": {filter: `team:"x | y"`, aggregateBy: "AVG"},
		"bare pipes and backslashes": {filter: `a:"x|y\\" b:c\|d`, groupBy: "k8s.pod.name", aggregateBy: "MAX"},
		"pre-grouped total": {filter: "k8s.namespace.name:[shop]", groupBy: "k8s.deployment.name",
			preGroupBy: "k8s.pod.name,k8s.container.name", preGroupByMethod: "AVG", aggregateBy: "SUM"},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			text := swRenderQueryText("k8s.container.spec.cpu.requests", q)
			metric, got, err := swApplyQueryText(text, swMeasurementQuery{aggregateBy: "AVG"})
			require.NoError(t, err, text)
			assert.Equal(t, "k8s.container.spec.cpu.requests", metric)
			assert.Equal(t, q, got, text)
		})
	}
}

func TestSolarWindsGetQuery_ShowsPreGroupBy(t *testing.T) {
	src := &SolarWindsMetricSource{}
	got, err := src.GetQuery(nil, FetchMetricsRequest{
		Queries: map[string]string{"a": "k8s.pod.cpu.usage.seconds.rate"},
		Request: map[string]any{"aggregateBy": "SUM", "preGroupBy": "k8s.pod.name", "preGroupByMethod": "AVG"},
	})
	require.NoError(t, err)
	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate | preGroupBy: k8s.pod.name | preGroupByMethod: AVG | aggregateBy: SUM", got)
}

// ============================================================================
// SUM / COUNT without an entity take it from the metric family
// ============================================================================

func TestSWInferEntity(t *testing.T) {
	cases := []struct {
		metric, agg, want string
	}{
		{"k8s.pod.cpu.usage.seconds.rate", "SUM", "k8s.pod.name"},
		{"k8s.container.spec.cpu.requests", "SUM", "k8s.pod.name,k8s.container.name"},
		{"k8s.node.memory.working_set", "COUNT", "k8s.node.name"},
		{"k8s.pod.cpu.usage.seconds.rate", "AVG", ""},
		{"trace.service.requests", "SUM", ""},
	}
	for _, tc := range cases {
		q := swInferEntity(tc.metric, swMeasurementQuery{aggregateBy: tc.agg})
		assert.Equal(t, tc.want, q.preGroupBy, tc.metric+" "+tc.agg)
		if tc.want != "" {
			assert.Equal(t, "AVG", q.preGroupByMethod)
		}
	}
	named := swInferEntity("k8s.pod.cpu.usage.seconds.rate", swMeasurementQuery{aggregateBy: "SUM", preGroupBy: "k8s.namespace.name", preGroupByMethod: "AVG"})
	assert.Equal(t, "k8s.namespace.name", named.preGroupBy, "the caller's entity wins")
}

func TestSolarWindsWrapAggregate_SumNamesTheEntity(t *testing.T) {
	src := &SolarWindsMetricSource{}
	text, err := src.GetQuery(nil, FetchMetricsRequest{
		Queries:       map[string]string{"a": "k8s.pod.cpu.usage.seconds.rate"},
		LabelMatchers: []LabelMatcher{{Label: "k8s.namespace.name", Operator: "_eq", Value: "shop"}},
	})
	require.NoError(t, err)
	got, err := wrapAggregator(src, text, "sum")
	require.NoError(t, err)
	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate | filter: k8s.namespace.name:shop | preGroupBy: k8s.pod.name | preGroupByMethod: AVG | aggregateBy: SUM", got)

	metric, q, err := swApplyQueryText(got, swMeasurementQuery{aggregateBy: "AVG"})
	require.NoError(t, err)
	assert.True(t, swIsTimeWeightedSum(swInferEntity(metric, q)), "the builder's sum runs as a time-weighted total")

	other, err := wrapAggregator(src, "trace.service.requests", "sum")
	require.NoError(t, err)
	assert.Equal(t, "trace.service.requests | aggregateBy: SUM", other, "any other family is left as sent")
}

func TestSWSumInflationNote(t *testing.T) {
	long := swMeasurementQuery{aggregateBy: "SUM", from: "2026-10-07T00:00:00Z", to: "2026-10-07T06:00:00Z"}
	assert.Contains(t, swSumInflationNote(long), "read high")
	short := long
	short.from = "2026-10-07T05:30:00Z"
	assert.Empty(t, swSumInflationNote(short))
	named := long
	named.preGroupBy, named.preGroupByMethod = "k8s.pod.name", "AVG"
	assert.Empty(t, swSumInflationNote(named))
	avg := long
	avg.aggregateBy = "AVG"
	assert.Empty(t, swSumInflationNote(avg))
}
