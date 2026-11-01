package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The query reduces each series within a bucket before adding series together. The
// previous `mstats sum(...) span=` added every SAMPLE in the bucket, inflating every figure
// by step / collection interval (4x at a 60s step against a 15s collector, 240x at 3600s).
// It also drops the still-filling final bucket, which otherwise made `latest` jump between
// a partial and a full figure.
func TestBuildSplunkUtilisationQueryShape(t *testing.T) {
	scalar, err := buildSplunkUtilisationQuery("k8s.node.allocatable_cpu", "nb_metrics", 60, "latest", nil)
	require.NoError(t, err)
	assert.Equal(t,
		`| mstats avg("k8s.node.allocatable_cpu") AS series_value WHERE index="nb_metrics" span=60s BY k8s.node.name`+
			` | stats sum(series_value) AS nb_value BY _time`+
			` | addinfo | where _time + 60 <= info_max_time | fields _time nb_value`+
			` | stats latest(nb_value) AS nb_value`,
		scalar)

	// A trend key wants the spanned series itself, so no reducing stage.
	trend, err := buildSplunkUtilisationQuery("k8s.node.cpu.usage", "nb_metrics", 300, "", nil)
	require.NoError(t, err)
	assert.Equal(t,
		`| mstats avg("k8s.node.cpu.usage") AS series_value WHERE index="nb_metrics" span=300s BY k8s.node.name`+
			` | stats sum(series_value) AS nb_value BY _time`+
			` | addinfo | where _time + 300 <= info_max_time | fields _time nb_value`,
		trend)
}

// A series is one node, one pod, or one container depending on the family. Grouping by
// too little re-introduces the sample sum; grouping by too much is harmless.
func TestSplunkUtilisationSeriesDims(t *testing.T) {
	for _, tc := range []struct {
		family string
		want   []string
	}{
		{"k8s.node.cpu.usage", []string{"k8s.node.name"}},
		{"K8s.Node.Allocatable_CPU", []string{"k8s.node.name"}},
		{"kube.node.allocatable_cpu", []string{"k8s.node.name"}},
		{"k8s.pod.cpu.usage", []string{"k8s.namespace.name", "k8s.pod.name"}},
		{"container.memory.usage", []string{"k8s.namespace.name", "k8s.pod.name", "k8s.container.name"}},
		{"k8s.container.cpu_limit", []string{"k8s.namespace.name", "k8s.pod.name", "k8s.container.name"}},
	} {
		assert.Equal(t, tc.want, splunkUtilisationSeriesDims(tc.family), tc.family)
	}
}

// Every family any role can resolve to must map to series dimensions; a family with no
// grouping would silently fall back to summing samples.
func TestSplunkUtilisationEveryCandidateHasSeriesDims(t *testing.T) {
	for role, families := range SplunkUtilisationCandidates {
		for _, f := range families {
			assert.NotEmpty(t, splunkUtilisationSeriesDims(f), "%s candidate %s", role, f)
		}
	}
}

// Without span, mstats aggregates across time as well as across series -- a 3-node,
// 181-sample window returned 15204 instead of the cluster's 84 cores. Every generated
// query must carry it.
func TestSplunkUtilisationQueriesAlwaysSpan(t *testing.T) {
	for key, spec := range splunkUtilisationKeys {
		q, err := buildSplunkUtilisationQuery("some.metric", "idx", 60, spec.Reducer, nil)
		require.NoError(t, err)
		assert.Contains(t, q, "span=60s", "%s must bucket by span", key)
		assert.Contains(t, q, `index="idx"`, "%s must carry the quoted index literal", key)
		assert.Contains(t, q, "AS "+splunkEnterpriseValueColumn, "%s must alias the value column", key)
	}
}

// Every generated query has to survive the integration's own validator, which only allows
// a generating command first, forbids the write commands, and insists on the quoted index.
func TestSplunkUtilisationQueriesPassTheMetricValidator(t *testing.T) {
	for key, spec := range splunkUtilisationKeys {
		q, err := buildSplunkUtilisationQuery("k8s.node.cpu.usage", "nb_metrics", 60, spec.Reducer, nil)
		require.NoError(t, err)
		require.NoError(t, validateSplunkEnterpriseMetricQuery(q, "nb_metrics"), "key %s produced %s", key, q)
	}
}

func TestResolveSplunkUtilisationFamilies(t *testing.T) {
	available := []OutputMetrics{
		{Metric: "k8s.node.allocatable_cpu"},
		{Metric: "k8s.node.cpu.usage"},
		{Metric: "k8s.node.allocatable_memory"},
		{Metric: "k8s.node.memory.usage"},
		{Metric: "k8s.container.cpu_request"},
		{Metric: "k8s.container.cpu_limit"},
		{Metric: "k8s.container.memory_request"},
		{Metric: "k8s.container.memory_limit"},
		{Metric: "some.unrelated.metric"},
	}
	got := resolveSplunkUtilisationFamilies(available)

	assert.Equal(t, "k8s.node.allocatable_cpu", got[seRoleCPUCapacity])
	assert.Equal(t, "k8s.node.cpu.usage", got[seRoleCPUUsage])
	assert.Equal(t, "k8s.node.allocatable_memory", got[seRoleMemCapacity])
	assert.Equal(t, "k8s.node.memory.usage", got[seRoleMemUsage])
	assert.Equal(t, "k8s.container.cpu_limit", got[seRoleCPULimit])
	assert.Equal(t, "k8s.container.memory_limit", got[seRoleMemLimit])
}

// A collector that publishes none of the known spellings must leave the role unresolved,
// so the caller can report which names were looked for instead of rendering a zero.
func TestResolveSplunkUtilisationFamiliesLeavesUnknownRolesUnset(t *testing.T) {
	got := resolveSplunkUtilisationFamilies([]OutputMetrics{{Metric: "totally.different.name"}})
	_, ok := got[seRoleCPUCapacity]
	assert.False(t, ok, "an absent family must not resolve")
}

// Splunk preserves whatever case the writer used and collectors are inconsistent, so
// matching is case-insensitive -- but the name sent back to SPL must be the one the index
// actually reports, not the lower-cased probe.
func TestResolveSplunkUtilisationFamiliesIsCaseInsensitive(t *testing.T) {
	got := resolveSplunkUtilisationFamilies([]OutputMetrics{{Metric: "K8s.Node.Allocatable_CPU"}})
	assert.Equal(t, "K8s.Node.Allocatable_CPU", got[seRoleCPUCapacity])
}

// meta.Step is a duration string; parsing only the "<n>s" form would make a later switch
// to minutes coarsen every percentile without any error.
func TestSplunkUtilisationStepParsesAnyDuration(t *testing.T) {
	req := GetUtilisationTrendRequest{}
	for _, tc := range []struct {
		step string
		want int
	}{
		{"60s", 60},
		{"1800s", 1800},
		{"5m", 300},
		{"1h", 3600},
		{"90", 90},
		// A sub-second step parses and is > 0, but truncates to 0 seconds. Emitting
		// span=0s would fail the search, so it must fall through to the default.
		{"500ms", 60},
		{"999ms", 60},
	} {
		assert.Equal(t, tc.want, splunkUtilisationStep(RequestMetadata{Step: tc.step}, req), "step %q", tc.step)
	}
	assert.Equal(t, 45, splunkUtilisationStep(RequestMetadata{Step: "nonsense"}, GetUtilisationTrendRequest{StepInterval: 45}))
	assert.Equal(t, 60, splunkUtilisationStep(RequestMetadata{}, req))
}

// The card's whole key set must be covered; a gap here renders a blank row.
func TestSplunkUtilisationCoversEveryCardKey(t *testing.T) {
	for _, key := range []string{
		"cpu_total", "cpu_real", "cpu_request", "cpu_limit",
		"mem_total", "mem_real", "memory_request", "memory_limit",
		"p50_cpu", "p90_cpu", "max_usage_cpu",
		"p50_mem", "p90_mem", "max_usage_mem",
	} {
		_, ok := splunkUtilisationKeys[UtilisationKey(key)]
		assert.True(t, ok, "%s is drawn by the Utilization & Health card and must be served", key)
	}
}

// The family name reaching the SPL comes from the index's own catalog, not from the
// candidate list, so it gets the same guard buildSplunkMStatsQuery applies.
func TestSplunkUtilisationRejectsUnsafeFamilyNames(t *testing.T) {
	assert.False(t, isSafeSplunkMetricName(`k8s.node.cpu" | delete`), "a name that could close the quote and chain a command must be rejected")
	assert.True(t, isSafeSplunkMetricName("k8s.node.cpu.usage"))
	assert.True(t, isSafeSplunkMetricName("k8s.node.allocatable_cpu"))
}

// The Trend popup asks for the gauge keys alongside the usage series with instant=false and
// draws a line each. Reducing them to "latest" there collapses three of the four lines to a
// single point, which is what a broken trend chart looks like.
func TestSplunkUtilisationGaugesReturnSeriesOnRangeRequests(t *testing.T) {
	gauge := splunkUtilisationKeys["cpu_total"]
	assert.Equal(t, "latest", splunkUtilisationReducer(gauge, ParsedAggregate{}, true), "an instant read still wants one figure")
	assert.Equal(t, "", splunkUtilisationReducer(gauge, ParsedAggregate{}, false), "a range read wants the series")

	// Everything the Trend popup plots must yield a series on a range request.
	for _, key := range []string{"cpu_total", "cpu_request", "cpu_limit", "mem_total", "memory_request", "memory_limit"} {
		q, err := buildSplunkUtilisationQuery("m", "idx", 60, splunkUtilisationReducer(splunkUtilisationKeys[UtilisationKey(key)], ParsedAggregate{}, false), nil)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(q, "| fields _time nb_value"), "%s must not reduce on a range request: %s", key, q)
	}

	// Window statistics describe the whole range by definition, so they stay reduced.
	for _, key := range []string{"cpu_real", "p50_cpu", "p90_cpu", "max_usage_cpu", "p50_mem", "max_usage_mem"} {
		spec := splunkUtilisationKeys[UtilisationKey(key)]
		assert.Equal(t, spec.Reducer, splunkUtilisationReducer(spec, ParsedAggregate{}, false), "%s stays reduced on a range request", key)
	}
}

// count is the one aggregate no key can encode -- there is no count_cpu -- so a count
// request must override the key's own reducer. It did not, and the answer was the metric's
// own value: cpu_total returned 12.0, the core count, where Prometheus returned 15915.
func TestSplunkUtilisationCountOverridesTheKeyReducer(t *testing.T) {
	count := ParsedAggregate{Name: "count"}

	// A gauge whose reducer is "latest" and a trend key whose reducer is empty both have to
	// answer a count with a count.
	for _, key := range []string{"cpu_total", "mem_total", "cpu_usage_trend", "mem_usage_trend"} {
		spec := splunkUtilisationKeys[UtilisationKey(key)]
		assert.Equal(t, "count", splunkUtilisationReducer(spec, count, false),
			"%s must answer a count request with count, not %q", key, spec.Reducer)

		q, err := buildSplunkUtilisationQuery("m", "idx", 60, splunkUtilisationReducer(spec, count, false), nil)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(q, "| stats count(nb_value) AS nb_value"),
			"%s must collapse to a single count: %s", key, q)
	}

	// Every other aggregate leaves the key's own statistic alone. Overriding it would answer
	// p50_cpu with a mean, under a name that promises a median.
	for _, key := range []string{"cpu_real", "p50_cpu", "p90_cpu", "max_usage_cpu"} {
		spec := splunkUtilisationKeys[UtilisationKey(key)]
		for _, agg := range []string{"avg", "max", "min"} {
			assert.Equal(t, spec.Reducer, splunkUtilisationReducer(spec, ParsedAggregate{Name: agg}, false),
				"%s must keep its own reducer under aggregate=%s", key, agg)
		}
	}
}

// A key this provider cannot serve must come back empty WITH A NOTE and no error. 91 call
// sites read Payload and only 62 read Error, so a capability gap on the error field is read
// by most of them as a workload that used nothing.
func TestSplunkUtilisationUnavailableIsANoteNotAnError(t *testing.T) {
	r := splunkUtilisationUnavailable("pvc_used_by_claim", "splunk enterprise: no such metric")
	assert.Equal(t, "pvc_used_by_claim", r.QueryKey)
	assert.Nil(t, r.Error, "a capability gap must not set Error")
	assert.Equal(t, "splunk enterprise: no such metric", r.Note, "and must say why")
	assert.Empty(t, r.Payload, "with no series")

	// A genuine failure keeps the error channel -- a caller must not read it as "no data".
	f := splunkUtilisationError("cpu_total", "boom")
	require.NotNil(t, f.Error)
	assert.Equal(t, "boom", *f.Error)
	assert.Empty(t, f.Note)
}

// Without scope matchers, cpu_request on a single pod's page answered with the whole
// cluster's request total -- plausible-looking and wrong. The Utilization page is
// workload/pod scoped, so every generated query must carry the narrowing.
func TestSplunkUtilisationScopesToTheRequestedTarget(t *testing.T) {
	// Cluster-wide: the card sends no namespace, so nothing narrows.
	assert.Nil(t, splunkUtilisationScope(RequestMetadata{}), "a cluster request must not be narrowed")

	// A workload matches pod name as a PREFIX, because pod names carry a replica hash.
	wl := splunkUtilisationScope(RequestMetadata{Namespace: "prod", Name: "checkout", Kind: "deployment"})
	q, err := buildSplunkUtilisationQuery("k8s.pod.cpu.usage", "idx", 60, "", wl)
	require.NoError(t, err)
	assert.Contains(t, q, `k8s.namespace.name="prod"`)
	assert.Contains(t, q, `k8s.pod.name="checkout-*"`)

	// A pod matches exactly.
	pod := splunkUtilisationScope(RequestMetadata{Namespace: "prod", Name: "checkout-abc123", Kind: "pod"})
	q, err = buildSplunkUtilisationQuery("k8s.pod.cpu.usage", "idx", 60, "", pod)
	require.NoError(t, err)
	assert.Contains(t, q, `k8s.pod.name="checkout-abc123"`)
	assert.NotContains(t, q, "*", "an exact pod match must not wildcard")

	// Container narrows further.
	c := splunkUtilisationScope(RequestMetadata{Namespace: "prod", Name: "checkout", ContainerName: "app"})
	q, err = buildSplunkUtilisationQuery("k8s.pod.cpu.usage", "idx", 60, "", c)
	require.NoError(t, err)
	assert.Contains(t, q, `k8s.container.name="app"`)

	// Series are grouped only to reduce them; the output is summed back to ONE series per
	// bucket. Callers read payload[0], so an ungrouped-by-pod output would chart one pod
	// and drop the rest.
	assert.Contains(t, q, "| stats sum(series_value) AS nb_value BY _time")
	assert.True(t, strings.HasSuffix(q, "| fields _time nb_value"), "only _time and nb_value may reach the row converter: %s", q)
}

// The Utilization page charts these two alongside the request/limit pairs; without them it
// draws limits with no usage line.
func TestSplunkUtilisationServesTheUtilizationPageKeys(t *testing.T) {
	for _, key := range []string{"cpu_usage", "memory_usage", "cpu_limit", "cpu_request", "memory_limit", "memory_request"} {
		_, ok := splunkUtilisationKeys[UtilisationKey(key)]
		assert.True(t, ok, "%s is requested by the Utilization page and must be served", key)
	}
}
