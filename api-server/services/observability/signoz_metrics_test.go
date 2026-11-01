package observability

import (
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignozRenderPromQL(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		matchers []LabelMatcher
		labels   map[string]string
		want     string
	}{
		{"bare OTel metric is quoted", "k8s.pod.cpu.usage", nil, nil, `{"k8s.pod.cpu.usage"}`},
		{"classic metric stays bare", "up", nil, nil, "up"},
		{"division is not a metric name", "errors_total/requests_total", nil, nil, "errors_total/requests_total"},
		{"subtraction is not a metric name", "up-down", nil, nil, "up-down"},
		{"cluster placeholder dropped", `sum({__CLUSTER__ "k8s.pod.cpu.usage"})`, nil, nil, `sum({ "k8s.pod.cpu.usage"})`},
		{
			"dotted matcher labels are quoted, classic ones bare",
			"k8s.pod.memory.working_set",
			[]LabelMatcher{{Label: "k8s.namespace.name", Operator: "_eq", Value: "shop"}, {Label: "job", Operator: "_neq", Value: "x"}},
			nil,
			`{"k8s.pod.memory.working_set",job!="x","k8s.namespace.name"="shop"}`,
		},
		{
			"legacy labels map",
			`sum({"container.cpu.usage"})`,
			nil,
			map[string]string{"k8s.pod.name": `a"b`},
			`sum({"container.cpu.usage","k8s.pod.name"="a\"b"})`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := signozRenderPromQL(tc.raw, tc.matchers, tc.labels)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// People in the Code tab and agents used to classic PromQL write dotted names bare.
func TestSignozQuoteDottedNames(t *testing.T) {
	cases := map[string]string{
		`sum(rate(k8s.pod.cpu.usage[5m]))`:                           `sum(rate({"k8s.pod.cpu.usage"}[5m]))`,
		`k8s.pod.cpu.usage{k8s.namespace.name="demo"}`:               `{"k8s.pod.cpu.usage", "k8s.namespace.name"="demo"}`,
		`k8s.pod.cpu.usage {}`:                                       `{"k8s.pod.cpu.usage"}`,
		`sum by (k8s.pod.name) (k8s.pod.memory.working_set)`:         `sum by ("k8s.pod.name") ({"k8s.pod.memory.working_set"})`,
		`max without (k8s.node.uid, host.name) (k8s.node.cpu.usage)`: `max without ("k8s.node.uid", "host.name") ({"k8s.node.cpu.usage"})`,
		`{"k8s.pod.cpu.usage", "k8s.pod.name"=~"a.b.*"}`:             `{"k8s.pod.cpu.usage", "k8s.pod.name"=~"a.b.*"}`,
		`up{job="k8s.pod.name"} == 1.5`:                              `up{job="k8s.pod.name"} == 1.5`,
		`rate(http_requests_total[1h:5m]) > 0.5e3`:                   `rate(http_requests_total[1h:5m]) > 0.5e3`,
		`errors_total/requests_total`:                                `errors_total/requests_total`,
	}
	for in, want := range cases {
		assert.Equal(t, want, signozQuoteDottedNames(in), in)
	}
}

func TestSignozRenderPromQL_RejectsLabelNamesThatEscapeTheQuote(t *testing.T) {
	for _, bad := range []string{`a"b`, `a\b`, "a{b", "", "a\nb"} {
		_, err := signozRenderPromQL("x", []LabelMatcher{{Label: bad, Operator: "_eq", Value: "v"}}, nil)
		assert.Error(t, err, "label %q", bad)
	}
}

// The body is a v0.145 /api/v4/query_range answer for a promql graph query.
func TestSignozPromResults(t *testing.T) {
	payload := []byte(`{"status":"success","data":{"resultType":"","result":[{"queryName":"A","series":[
		{"labels":{"k8s.deployment.name":"kafka"},"labelsArray":null,"values":[{"timestamp":1792061612000,"value":"675479552"},{"timestamp":1792061672000,"value":"NaN"},{"timestamp":1792061732000,"value":"675483648"}]},
		{"labels":{},"labelsArray":null,"values":[{"timestamp":1792061672000,"value":"16347136"}]}]}]}}`)
	got, err := signozPromResults(payload)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, map[string]string{"k8s.deployment.name": "kafka"}, got[0].Metric)
	assert.Equal(t, []int64{1792061612, 1792061732}, got[0].Timestamps, "milliseconds become seconds; NaN dropped")
	assert.Equal(t, []float64{675479552, 675483648}, got[0].Values)
	assert.NotNil(t, got[1].Metric)
}

func TestSignozPromResults_NoSeriesAndErrors(t *testing.T) {
	got, err := signozPromResults([]byte(`{"status":"success","data":{"resultType":"","result":[{"queryName":"A"}]}}`))
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = signozPromResults([]byte(`{"status":"error","data":{"Query-A":"1:5: parse error: unclosed left parenthesis"},"errorType":"internal","error":"error in prom queries"}`))
	assert.EqualError(t, err, "SigNoz rejected the query: 1:5: parse error: unclosed left parenthesis")

	_, err = signozPromResults([]byte(`{"status":"error","error":"bad"}`))
	assert.EqualError(t, err, "SigNoz rejected the query: bad")
}

func TestSignozRunKeySafely_PanicFailsOnlyThatKey(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	got := signozRunKeySafely(ctx, "cpu_usage", func() QueryResult { panic("boom") })
	assert.Equal(t, "cpu_usage", got.QueryKey)
	require.NotNil(t, got.Error)
	assert.Contains(t, *got.Error, "boom")

	ok := signozRunKeySafely(ctx, "memory_usage", func() QueryResult { return QueryResult{QueryKey: "memory_usage"} })
	assert.Nil(t, ok.Error)
}

func TestSignozLatestSamples(t *testing.T) {
	got := signozLatestSamples([]Result{{Timestamps: []int64{1, 2, 3}, Values: []float64{1, 2, 3}}, {}})
	assert.Equal(t, []int64{3}, got[0].Timestamps)
	assert.Equal(t, []float64{3}, got[0].Values)
	assert.Empty(t, got[1].Values)
}

func TestSignozRangeParams(t *testing.T) {
	start, end, step := signozRangeParams(FetchMetricsRequest{StartTime: 1_000_000, EndTime: 1_000_000 + 3_600_000})
	assert.Equal(t, int64(1_000_000), start)
	assert.Equal(t, int64(4_600_000), end)
	assert.Equal(t, 36, step, "about 100 points over the window")

	start, end, step = signozRangeParams(FetchMetricsRequest{EndTime: 10_000_000, Instant: true})
	assert.Equal(t, 60, step)
	assert.Equal(t, int64(10_000_000-60_000), start, "an instant query with no window evaluates the last step")
	assert.Equal(t, int64(10_000_000), end)
}

func TestServesOnlyAsExplicitDefault(t *testing.T) {
	assert.True(t, servesOnlyAsExplicitDefault("signoz", "metrics"), "a logs-only SigNoz must not be picked for metrics by elimination")
	assert.False(t, servesOnlyAsExplicitDefault("signoz", "logs"))
	assert.False(t, servesOnlyAsExplicitDefault("cubeapm", "metrics"))
	assert.True(t, isSupportedTelemetryProvider("signoz", "user", "metrics"), "still selectable explicitly")
}

func TestBuildSignozUtilisationQueries_Workload(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "checkout", Kind: "deployment"}
	keys := []string{
		string(KeyCPUUsage), string(KeyMemoryUsage), string(KeyCPUUsageByPod), string(KeyPodCount),
		string(KeyContainerMemoryWorkingSetByPod), string(KeyCPURequest), string(KeyMemoryLimit),
		string(KeyReplicaDefined), string(KeyReplicaReady), string(KeyCPUReal), string(KeyHTTPLatencyP99),
	}
	q := buildSignozUtilisationQueries(meta, keys)

	scope := `"k8s.namespace.name"="shop", "k8s.deployment.name"="checkout"`
	assert.Equal(t, `sum({"container.cpu.usage", `+scope+`})`, q[string(KeyCPUUsage)], "a gauge in cores: no rate()")
	assert.Equal(t, `sum({"container.memory.working_set", `+scope+`})`, q[string(KeyMemoryUsage)])
	assert.Equal(t, `sum by ("k8s.namespace.name", "k8s.pod.name") ({"container.cpu.usage", `+scope+`})`, q[string(KeyCPUUsageByPod)])
	assert.Equal(t, `count(count by ("k8s.pod.name") ({"container.memory.working_set", `+scope+`}))`, q[string(KeyPodCount)])
	assert.Equal(t, `max by ("k8s.namespace.name", "k8s.pod.name", "k8s.container.name") ({"container.memory.working_set", `+scope+`})`, q[string(KeyContainerMemoryWorkingSetByPod)])
	assert.Equal(t, `sum(max by ("k8s.namespace.name", "k8s.pod.name", "k8s.container.name") ({"k8s.container.cpu_request", `+scope+`}))`, q[string(KeyCPURequest)])
	assert.Equal(t, `sum(max by ("k8s.namespace.name", "k8s.pod.name", "k8s.container.name") ({"k8s.container.memory_limit", `+scope+`}))`, q[string(KeyMemoryLimit)])
	assert.Equal(t, `max({"k8s.deployment.desired", "k8s.namespace.name"="shop", "k8s.deployment.name"="checkout"})`, q[string(KeyReplicaDefined)])
	assert.Equal(t, `max({"k8s.deployment.available", "k8s.namespace.name"="shop", "k8s.deployment.name"="checkout"})`, q[string(KeyReplicaReady)])
	assert.NotContains(t, q, string(KeyCPUReal), "a cluster key is not answered at workload scope")
	assert.NotContains(t, q, string(KeyHTTPLatencyP99), "keys SigNoz's collector has no metric for are left to the not-available note")
}

func TestBuildSignozUtilisationQueries_ScopeVariants(t *testing.T) {
	q := buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "web-0", Kind: "pod", ContainerName: "app"}, []string{string(KeyCPUUsage)})
	assert.Equal(t, `sum({"container.cpu.usage", "k8s.namespace.name"="shop", "k8s.pod.name"="web-0", "k8s.container.name"="app"})`, q[string(KeyCPUUsage)])

	q = buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "x", PodNamesSupplied: true}, []string{string(KeyCPUUsage)})
	assert.Contains(t, q[string(KeyCPUUsage)], `"k8s.pod.name"="__no_pods_resolved__"`, "a resolved empty pod list matches nothing")

	q = buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "api", Kind: "rollout"}, []string{string(KeyCPUUsage)})
	assert.Contains(t, q[string(KeyCPUUsage)], `"k8s.pod.name"=~"api-.*"`, "a kind the collector does not label falls back to the pod-name prefix")

	q = buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "db", Kind: "statefulset"}, []string{string(KeyCPUUsage)})
	assert.Contains(t, q[string(KeyCPUUsage)], `"k8s.statefulset.name"="db"`)

	q = buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "db", Kind: "statefulset"}, []string{string(KeyReplicaReady)})
	assert.Equal(t, `max({"k8s.statefulset.ready_pods", "k8s.namespace.name"="shop", "k8s.statefulset.name"="db"})`, q[string(KeyReplicaReady)])
}

func TestBuildSignozUtilisationQueries_ClusterAndNode(t *testing.T) {
	q := buildSignozUtilisationQueries(RequestMetadata{RangeWindow: "1h", Step: "5m"}, []string{
		string(KeyCPUReal), string(KeyCPUTotal), string(KeyMemReal), string(KeyMemTotal), string(KeyP90CPU), string(KeyMaxUsageMem), string(KeyP90Mem),
	})
	assert.Equal(t, `avg_over_time(sum({"k8s.node.cpu.usage"})[1h:5m])`, q[string(KeyCPUReal)], "the window's average, like a rate over the range")
	assert.Equal(t, `quantile(0.9, sum by ("k8s.node.name") ({"k8s.node.memory.working_set"}))`, q[string(KeyP90Mem)], "across nodes, as on Prometheus")
	assert.Equal(t, `sum({"k8s.node.allocatable_cpu"})`, q[string(KeyCPUTotal)])
	assert.Equal(t, `sum({"k8s.node.memory.working_set"})`, q[string(KeyMemReal)])
	assert.Equal(t, `sum({"k8s.node.allocatable_memory"})`, q[string(KeyMemTotal)])
	assert.Equal(t, `quantile_over_time(0.9, sum({"k8s.node.cpu.usage"})[1h:5m])`, q[string(KeyP90CPU)])
	assert.Equal(t, `max_over_time(sum({"k8s.node.memory.working_set"})[1h:5m])`, q[string(KeyMaxUsageMem)])

	n := buildSignozUtilisationQueries(RequestMetadata{Kind: "node", NodeName: "node-a"}, []string{
		string(KeyCPUUsage), string(KeyMemTotal), string(KeyCPURequest), string(KeyContainerCPUUsageByPod),
	})
	assert.Equal(t, `sum({"k8s.node.cpu.usage", "k8s.node.name"="node-a"})`, n[string(KeyCPUUsage)])
	assert.Equal(t, `sum({"k8s.node.allocatable_memory", "k8s.node.name"="node-a"})`, n[string(KeyMemTotal)])
	assert.Equal(t, `sum(max by ("k8s.namespace.name", "k8s.pod.name", "k8s.container.name") ({"k8s.container.cpu_request", "k8s.node.name"="node-a"}))`, n[string(KeyCPURequest)])
	assert.Equal(t, `max by ("k8s.namespace.name", "k8s.pod.name", "k8s.container.name") ({"container.cpu.usage", "k8s.node.name"="node-a"})`, n[string(KeyContainerCPUUsageByPod)])

	assert.Empty(t, buildSignozUtilisationQueries(RequestMetadata{Kind: "node"}, []string{string(KeyCPUUsage)}), "no node name, no query")
}

func TestBuildSignozUtilisationQueries_AggregateSkipsSelfWindowedKeys(t *testing.T) {
	q := buildSignozUtilisationQueries(RequestMetadata{RangeWindow: "1h", Step: "5m", Aggregate: ParsedAggregate{Name: "max"}},
		[]string{string(KeyCPUReal), string(KeyP90Mem), string(KeyMaxUsageMem)})
	assert.Equal(t, `avg_over_time(sum({"k8s.node.cpu.usage"})[1h:5m])`, q[string(KeyCPUReal)])
	assert.Equal(t, `max_over_time(sum({"k8s.node.memory.working_set"})[1h:5m])`, q[string(KeyMaxUsageMem)])
	assert.Equal(t, `max_over_time((quantile(0.9, sum by ("k8s.node.name") ({"k8s.node.memory.working_set"})))[1h:5m])`, q[string(KeyP90Mem)],
		"an instantaneous key takes the aggregate, as on Prometheus")
}

func TestBuildSignozUtilisationQueries_AppliesRequestAggregate(t *testing.T) {
	q := buildSignozUtilisationQueries(RequestMetadata{Namespace: "shop", Name: "web", RangeWindow: "1h", Step: "5m",
		Aggregate: ParsedAggregate{Name: "max"}}, []string{string(KeyMemoryUsage)})
	assert.Equal(t, `max_over_time((sum({"container.memory.working_set", "k8s.namespace.name"="shop", "k8s.pod.name"=~"web-.*"}))[1h:5m])`, q[string(KeyMemoryUsage)])
}

func TestCanonicaliseUtilisationLabels_Signoz(t *testing.T) {
	results := []QueryResult{{QueryKey: string(KeyContainerCPUUsageByPod), Payload: []Result{{Metric: map[string]string{
		"k8s.namespace.name": "shop", "k8s.pod.name": "web-1", "k8s.container.name": "app", "k8s.deployment.name": "web",
	}}}}}
	canonicaliseUtilisationLabels(results, "signoz")
	m := results[0].Payload[0].Metric
	assert.Equal(t, "shop", m[CanonicalNamespace])
	assert.Equal(t, "web-1", m[CanonicalPod])
	assert.Equal(t, "app", m[CanonicalContainer])
	assert.Equal(t, "web", m[CanonicalWorkload])
}
