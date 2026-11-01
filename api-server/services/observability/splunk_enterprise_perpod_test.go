package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The per-container keys vertical rightsizing reads. Every one must be either served or
// refused with a reason -- never fall through to "not supported for this provider",
// which reads as a gap nobody has looked at.
var splunkRightsizingKeys = []string{
	"container_cpu_usage_by_pod",
	"container_memory_working_set_by_pod",
	"container_memory_max_usage_by_pod",
	"container_oomkilled_limit_by_pod",
	"container_cpu_request_by_pod",
	"container_cpu_limit_by_pod",
	"container_memory_request_by_pod",
	"container_memory_limit_by_pod",
}

func TestSplunkPerPodKeysAreAllDecided(t *testing.T) {
	for _, key := range splunkRightsizingKeys {
		spec, served := splunkUtilisationKeys[UtilisationKey(key)]
		_, refused := splunkUtilisationRefusals[UtilisationKey(key)]
		assert.True(t, served != refused, "%s must be exactly one of served or refused", key)
		if served {
			assert.True(t, spec.PerPod, "%s must return one series per pod and container", key)
		}
	}
}

// One series per namespace/pod/container, one point per step. Unlike the workload keys
// the series are NOT summed together -- the key name promises a series per container.
func TestBuildSplunkPerPodQueryShape(t *testing.T) {
	const dims = "k8s.namespace.name, k8s.pod.name, k8s.container.name"
	series := `| mstats avg("container.cpu.usage") AS nb_value WHERE index="k8s_metrics" span=75s BY ` + dims +
		` | addinfo | where _time + 75 <= info_max_time | fields _time k8s.namespace.name k8s.pod.name k8s.container.name nb_value`

	q, err := buildSplunkPerPodQuery("container.cpu.usage", "k8s_metrics", 75, nil, ParsedAggregate{}, false)
	require.NoError(t, err)
	assert.Equal(t, series, q)

	for _, tc := range []struct {
		name    string
		agg     ParsedAggregate
		instant bool
		stage   string
	}{
		{"instant read", ParsedAggregate{}, true, "latest(nb_value)"},
		{"p95", ParsedAggregate{Name: "quantile", Quantile: 0.95}, true, "exactperc95(nb_value)"},
		{"p99.9", ParsedAggregate{Name: "quantile", Quantile: 0.999}, true, "exactperc99.9(nb_value)"},
		{"count", ParsedAggregate{Name: AggregateCount}, true, "count(nb_value)"},
		{"max", ParsedAggregate{Name: AggregateMax}, true, "max(nb_value)"},
		{"avg", ParsedAggregate{Name: AggregateAvg}, true, "avg(nb_value)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := buildSplunkPerPodQuery("container.cpu.usage", "k8s_metrics", 75, nil, tc.agg, tc.instant)
			require.NoError(t, err)
			assert.Equal(t, series+" | stats "+tc.stage+" AS nb_value BY "+dims, q)
			require.NoError(t, validateSplunkEnterpriseMetricQuery(q, "k8s_metrics"))
		})
	}
}

// `count` is "step-wide intervals with data" (metrics_aggregate_count.go). Bucketing at
// the caller's step and counting the buckets IS that definition, so no conversion is
// applied -- a conversion on top would double-count.
func TestSplunkPerPodCountIsStepIntervals(t *testing.T) {
	q, err := buildSplunkPerPodQuery("container.cpu.usage", "idx", 75, nil, ParsedAggregate{Name: AggregateCount}, true)
	require.NoError(t, err)
	assert.Contains(t, q, "span=75s")
	assert.Contains(t, q, "| stats count(nb_value) AS nb_value BY")
}

// Node-scoped requests (Kind=node) narrow by the node's own dimension; before this they
// were answered cluster-wide.
func TestSplunkUtilisationScopeNarrowsToANode(t *testing.T) {
	scope := splunkUtilisationScope(RequestMetadata{Kind: "node", NodeName: "gke-node-1"})
	require.Len(t, scope, 1)
	assert.Equal(t, LabelMatcher{Label: "k8s.node.name", Operator: "_eq", Value: "gke-node-1"}, scope[0])

	assert.Nil(t, splunkUtilisationScope(RequestMetadata{Kind: "node"}), "no node name, nothing to narrow on")
}

// Rightsizing passes the workload's exact pod list. It must replace the name prefix,
// which also matches a sibling workload ("services-server" vs "services-server-worker"),
// and an empty list is a real answer that must match nothing.
func TestSplunkUtilisationScopeUsesTheSuppliedPodList(t *testing.T) {
	base := RequestMetadata{Namespace: "demo", Name: "checkout", Kind: "deployment"}

	listed := base
	listed.PodNames, listed.PodNamesSupplied = []string{"checkout-a", "checkout-b"}, true
	q, err := buildSplunkPerPodQuery("container.cpu.usage", "idx", 60, splunkUtilisationScope(listed), ParsedAggregate{}, false)
	require.NoError(t, err)
	assert.Contains(t, q, `k8s.pod.name IN ("checkout-a", "checkout-b")`)
	assert.NotContains(t, q, `checkout-*`, "the list replaces the prefix")

	empty := base
	empty.PodNames, empty.PodNamesSupplied = []string{}, true
	q, err = buildSplunkPerPodQuery("container.cpu.usage", "idx", 60, splunkUtilisationScope(empty), ParsedAggregate{}, false)
	require.NoError(t, err)
	assert.Contains(t, q, `k8s.pod.name="__no_pods_resolved__"`)

	// A pod list with no workload name is still scope.
	bare := RequestMetadata{Namespace: "demo", PodNames: []string{"x-1"}, PodNamesSupplied: true}
	assert.Contains(t, splunkUtilisationScope(bare), LabelMatcher{Label: "k8s.pod.name", Operator: "_in", Value: "x-1"})

	// Values are escaped like any other matcher.
	m, err := buildSplunkMetricMatcher(LabelMatcher{Label: "k8s.pod.name", Operator: "_in", Value: `a,b"c`})
	require.NoError(t, err)
	assert.Equal(t, `k8s.pod.name IN ("a", "b\"c")`, m)
}

// Splunk rows carry the OTel dimension names; the utilisation layer's contract is the
// canonical ones, so a rightsizing consumer can join usage to requests on
// (namespace, pod, container) whatever the provider.
func TestSplunkLabelsAreCanonicalised(t *testing.T) {
	results := []QueryResult{{Payload: []Result{{Metric: map[string]string{
		"k8s.namespace.name": "demo", "k8s.pod.name": "checkout-1", "k8s.container.name": "app", "k8s.node.name": "n1",
	}}}}}
	canonicaliseUtilisationLabels(results, "splunk_enterprise")
	m := results[0].Payload[0].Metric
	assert.Equal(t, "demo", m[CanonicalNamespace])
	assert.Equal(t, "checkout-1", m[CanonicalPod])
	assert.Equal(t, "app", m[CanonicalContainer])
	assert.Equal(t, "n1", m[CanonicalNode])
}

// Through FetchMetricUtilisation with a fake Splunk: a p95 request for the rightsizing
// keys returns one canonically-labelled series per container, the two keys no Splunk
// layout carries are refused with their reason, and they cost no search.
func TestFetchMetricUtilisationServesSplunkRightsizingKeys(t *testing.T) {
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		spl := r.PostForm.Get("search")
		searches = append(searches, spl)
		var results []map[string]any
		switch {
		case strings.HasPrefix(spl, "| mcatalog values(metric_name)"):
			results = []map[string]any{{"values(metric_name)": []any{"container.cpu.usage", "k8s.container.cpu_request"}}}
		case strings.HasPrefix(spl, "| mstats"):
			results = []map[string]any{
				{"k8s.namespace.name": "demo", "k8s.pod.name": "checkout-a", "k8s.container.name": "app", "nb_value": "0.42"},
				{"k8s.namespace.name": "demo", "k8s.pod.name": "checkout-b", "k8s.container.name": "app", "nb_value": "0.37"},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetSplunkEnterpriseConfig,
		func(_ *security.RequestContext, _ string) (integrations.SplunkEnterpriseConfig, error) {
			return integrations.SplunkEnterpriseConfig{URL: server.URL, AuthType: integrations.SplunkEnterpriseAuthBasic,
				Username: "u", Password: "p", MetricIndex: "k8s_metrics", App: "search"}, nil
		})
	stubUtilisationProvider(t, "splunk_enterprise", "user")
	origSource := metricsSourceForAccount
	metricsSourceForAccount = func(_ *security.RequestContext, _, _, _ string) (MetricSource, error) {
		return &SplunkEnterpriseMetricSource{}, nil
	}
	defer func() { metricsSourceForAccount = origSource }()

	now := time.Now()
	out, err := FetchMetricUtilisation(mockRequestContext(), GetUtilisationTrendRequest{
		AccountId: "acc", StartTime: now.Add(-24 * time.Hour).UnixMilli(), EndTime: now.UnixMilli(),
		Aggregate: "p95",
		Request: map[string]any{
			"kind": "deployment", "workload_namespace": "demo", "workload_name": "checkout",
			"metrics": []any{"container_cpu_usage_by_pod", "container_memory_max_usage_by_pod", "container_oomkilled_limit_by_pod"},
		},
	})
	require.NoError(t, err)

	byKey := map[string]QueryResult{}
	for _, r := range out.Results {
		byKey[r.QueryKey] = r
	}

	usage := byKey["container_cpu_usage_by_pod"]
	require.Nil(t, usage.Error)
	require.Len(t, usage.Payload, 2, "one series per pod and container")
	assert.Equal(t, "checkout-a", usage.Payload[0].Metric[CanonicalPod])
	assert.Equal(t, "app", usage.Payload[0].Metric[CanonicalContainer])
	assert.Equal(t, "demo", usage.Payload[0].Metric[CanonicalNamespace])
	assert.Equal(t, []float64{0.42}, usage.Payload[0].Values)
	assert.Contains(t, usage.Query, "exactperc95(nb_value)")
	assert.Contains(t, usage.Query, `k8s.pod.name="checkout-*"`)

	// A refusal is a capability statement, not a failure: empty with a note, and no error.
	// On the error channel most callers read it as "this workload used nothing".
	for _, key := range []string{"container_memory_max_usage_by_pod", "container_oomkilled_limit_by_pod"} {
		assert.Nil(t, byKey[key].Error, key)
		assert.Contains(t, byKey[key].Note, "no Splunk", key)
		assert.Empty(t, byKey[key].Payload, key)
	}
	assert.Len(t, searches, 2, "one catalog read and one search; refused keys cost none")
}
