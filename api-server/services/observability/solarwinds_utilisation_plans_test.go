package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The conformance suite asked cpu_usage_line for a namespace on one node (kind
// "deployment", no workload name) and got 3 series: the groupBy named the deployment
// attribute although no deployment was named, so the namespace split per deployment.
func TestSolarWindsGroupBy_KindWithoutNameDoesNotFanOut(t *testing.T) {
	assert.Equal(t, "k8s.namespace.name", buildSolarWindsGroupBy(RequestMetadata{Kind: "deployment", Namespace: "shop"}))
	assert.Equal(t, "k8s.namespace.name,k8s.deployment.name",
		buildSolarWindsGroupBy(RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}))

	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", NodeName: "node-1"}
	batches := swWorkloadBatches(meta, buildSolarWindsWorkloadQueries(meta, []string{"cpu_usage_line", "memory_usage_line"}))
	require.Len(t, batches, 1)
	assert.Equal(t, "k8s.namespace.name", batches[0].request["groupBy"])
	assert.Equal(t, "k8s.node.name: [node-1] k8s.namespace.name: [shop]", batches[0].request["filter"])
}

var swWorkloadMeta = RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}

const swWorkloadFilter = "k8s.namespace.name: [shop] k8s.deployment.name: [web]"

func TestSolarWindsWorkloadPlans_PerContainerKeys(t *testing.T) {
	want := map[string]string{
		"container_cpu_usage_by_pod":          "k8s.container.cpu.usage.seconds.rate",
		"container_memory_working_set_by_pod": "k8s.container_memory_working_set_bytes",
		"container_memory_max_usage_by_pod":   "k8s.container_memory_max_usage_bytes",
		"container_cpu_request_by_pod":        "k8s.container.spec.cpu.requests",
		"container_cpu_limit_by_pod":          "k8s.container.spec.cpu.limit",
		"container_memory_request_by_pod":     "k8s.container.spec.memory.requests",
		"container_memory_limit_by_pod":       "k8s.container.spec.memory.limit",
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	plans := buildSolarWindsWorkloadPlans(swWorkloadMeta, keys)
	require.Len(t, plans, len(want))
	for key, metric := range want {
		p := plans[key]
		assert.Equal(t, metric, p.metric, key)
		assert.True(t, p.perContainer, key)
		agg := "AVG"
		if strings.HasPrefix(metric, "k8s.container.spec.") {
			agg = "MAX" // constant per container; immune to the end marker
		}
		assert.Equal(t, map[string]any{
			"aggregateBy": agg,
			"groupBy":     "k8s.namespace.name,k8s.pod.name,k8s.container.name",
			"filter":      swWorkloadFilter,
		}, p.request, key)
	}
	// Usage in one call, specs in another.
	assert.Len(t, swPlanBatches(plans), 2)
}

func TestSolarWindsWorkloadPlans_PerContainerHonoursContainerAndPodNames(t *testing.T) {
	meta := swWorkloadMeta
	meta.ContainerName = "app"
	meta.PodNames, meta.PodNamesSupplied = []string{"web-1"}, true
	p := buildSolarWindsWorkloadPlans(meta, []string{"container_cpu_usage_by_pod"})["container_cpu_usage_by_pod"]
	assert.Equal(t, swWorkloadFilter+" k8s.pod.name: [web-1] k8s.container.name: [app]", p.request["filter"])
}

func TestSolarWindsWorkloadPlans_PerPodAndBusiestPod(t *testing.T) {
	plans := buildSolarWindsWorkloadPlans(swWorkloadMeta,
		[]string{"cpu_usage_by_pod", "memory_usage_by_pod", "cpu_usage_max_pod", "memory_usage_max_pod"})

	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate", plans["cpu_usage_by_pod"].metric)
	assert.Equal(t, "k8s.pod.memory.working_set", plans["memory_usage_by_pod"].metric)
	for _, k := range []string{"cpu_usage_by_pod", "memory_usage_by_pod"} {
		assert.Equal(t, map[string]any{"aggregateBy": "AVG", "groupBy": "k8s.namespace.name,k8s.pod.name", "filter": swWorkloadFilter},
			plans[k].request, k)
		assert.False(t, plans[k].perContainer, "pod metrics carry no container")
	}

	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate", plans["cpu_usage_max_pod"].metric)
	assert.Equal(t, "k8s.pod.memory.working_set", plans["memory_usage_max_pod"].metric)
	for _, k := range []string{"cpu_usage_max_pod", "memory_usage_max_pod"} {
		assert.Equal(t, map[string]any{
			"aggregateBy": "MAX", "preGroupBy": "k8s.pod.name", "preGroupByMethod": "AVG",
			"groupBy": "k8s.namespace.name,k8s.deployment.name", "filter": swWorkloadFilter,
		}, plans[k].request, k)
	}
}

func TestSolarWindsWorkloadPlans_TimeWeightedTotals(t *testing.T) {
	plans := buildSolarWindsWorkloadPlans(swWorkloadMeta,
		[]string{"pod_count", "disk_read_bytes", "disk_write_bytes", "network_receive_packet", "network_transmit_packets"})

	sum := func(filter string) map[string]any {
		return map[string]any{
			"aggregateBy": "SUM", "preGroupBy": "k8s.pod.name", "preGroupByMethod": "AVG",
			"groupBy": "k8s.namespace.name,k8s.deployment.name", "filter": filter,
		}
	}
	assert.Equal(t, "k8s.kube_pod_status_phase", plans["pod_count"].metric)
	assert.Equal(t, sum(swWorkloadFilter+" phase:Running"), plans["pod_count"].request)

	for key, metric := range map[string]string{
		"disk_read_bytes":          "k8s.pod.fs.reads.bytes.rate",
		"disk_write_bytes":         "k8s.pod.fs.writes.bytes.rate",
		"network_receive_packet":   "k8s.pod.network.bytes_received",
		"network_transmit_packets": "k8s.pod.network.bytes_transmitted",
	} {
		assert.Equal(t, metric, plans[key].metric, key)
		assert.Equal(t, sum(swWorkloadFilter), plans[key].request, key)
	}
	assert.True(t, plans["network_transmit_packets"].negate, "transmit is reported negative, as on every provider")
	assert.False(t, plans["network_receive_packet"].negate)
}

func TestSolarWindsWorkloadPlans_Replicas(t *testing.T) {
	cases := []struct {
		kind, attr, defined, ready string
	}{
		{"Deployment", "k8s.deployment.name", "k8s.kube_deployment_spec_replicas", "k8s.kube_deployment_status_replicas_ready"},
		{"statefulset", "k8s.statefulset.name", "k8s.kube_statefulset_replicas", "k8s.kube_statefulset_status_replicas_ready"},
		{"daemonset", "k8s.daemonset.name", "k8s.kube_daemonset_status_desired_number_scheduled", "k8s.kube_daemonset_status_number_ready"},
		// A kind SolarWinds does not tag is read as a Deployment, as on Prometheus.
		{"workload", "k8s.deployment.name", "k8s.kube_deployment_spec_replicas", "k8s.kube_deployment_status_replicas_ready"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			meta := RequestMetadata{Kind: tc.kind, Namespace: "shop", Name: "web"}
			plans := buildSolarWindsWorkloadPlans(meta, []string{"replica_defined", "replica_ready"})
			assert.Equal(t, tc.defined, plans["replica_defined"].metric)
			assert.Equal(t, tc.ready, plans["replica_ready"].metric)
			assert.Equal(t, map[string]any{
				"aggregateBy": "MAX",
				"filter":      "k8s.namespace.name: [shop] " + tc.attr + ": [web]",
				"groupBy":     "k8s.namespace.name," + tc.attr,
			}, plans["replica_ready"].request)
		})
	}
	assert.Empty(t, buildSolarWindsWorkloadPlans(RequestMetadata{Kind: "pod", Namespace: "shop", Name: "web-1"}, []string{"replica_defined"}),
		"a pod has no replica count")
	assert.Empty(t, buildSolarWindsWorkloadPlans(RequestMetadata{Kind: "deployment", Namespace: "shop"}, []string{"replica_defined"}),
		"no workload named, no controller to read")
}

func TestSolarWindsWorkloadPlans_SinglePodKeys(t *testing.T) {
	// The investigate card sends the event's pod as the workload name, kind "workload".
	meta := RequestMetadata{Kind: "workload", Namespace: "shop", Name: "web-7d9f-x2"}
	plans := buildSolarWindsWorkloadPlans(meta, []string{"cpu_usage_pod", "cpu_request_pod", "cpu_limit_pod"})
	podFilter := "k8s.namespace.name: [shop] k8s.pod.name: [web-7d9f-x2]"

	assert.Equal(t, "k8s.pod.cpu.usage.seconds.rate", plans["cpu_usage_pod"].metric)
	assert.Equal(t, map[string]any{
		"aggregateBy": "SUM", "preGroupBy": "k8s.pod.name", "preGroupByMethod": "AVG",
		"groupBy": "k8s.namespace.name,k8s.pod.name", "filter": podFilter,
	}, plans["cpu_usage_pod"].request)

	assert.Equal(t, "k8s.container.spec.cpu.requests", plans["cpu_request_pod"].metric)
	assert.Equal(t, "k8s.container.spec.cpu.limit", plans["cpu_limit_pod"].metric)
	assert.Empty(t, buildSolarWindsWorkloadPlans(swWorkloadMeta, []string{"cpu_usage_pod", "cpu_request_pod", "cpu_limit_pod"}),
		"a controller kind says the name is not a pod: unanswered, with a note, rather than empty")
	assert.NotEmpty(t, buildSolarWindsWorkloadPlans(RequestMetadata{Kind: "pod", Namespace: "shop", Name: "web-1"}, []string{"cpu_usage_pod"}))
	for _, k := range []string{"cpu_request_pod", "cpu_limit_pod"} {
		assert.True(t, plans[k].perContainer)
		assert.True(t, plans[k].dropEndMarker)
		assert.Equal(t, map[string]any{
			"aggregateBy": "MAX", "groupBy": "k8s.namespace.name,k8s.pod.name,k8s.container.name", "filter": podFilter,
		}, plans[k].request, k)
	}
}

func TestSolarWindsPVCPlans(t *testing.T) {
	keys := []string{"pvc_used_by_claim", "pvc_capacity_by_claim", "pvc_usage", "pvc_requests"}

	ns := buildSolarWindsPVCPlans(RequestMetadata{Namespace: "shop"}, keys)
	assert.Equal(t, "k8s.kubelet_volume_stats_used_bytes", ns["pvc_used_by_claim"].metric)
	assert.Equal(t, "k8s.kubelet_volume_stats_capacity_bytes", ns["pvc_capacity_by_claim"].metric)
	assert.Equal(t, map[string]any{
		"aggregateBy": "AVG", "groupBy": "k8s.namespace.name,k8s.persistentvolumeclaim.name", "filter": "k8s.namespace.name: [shop]",
	}, ns["pvc_used_by_claim"].request)

	assert.Equal(t, "k8s.kubelet_volume_stats_used_bytes", ns["pvc_usage"].metric)
	assert.Equal(t, "k8s.kube_persistentvolumeclaim_resource_requests_storage_bytes", ns["pvc_requests"].metric)
	assert.Equal(t, map[string]any{
		"aggregateBy": "SUM", "preGroupBy": "k8s.namespace.name,k8s.persistentvolumeclaim.name", "preGroupByMethod": "AVG",
		"groupBy": "k8s.namespace.name", "filter": "k8s.namespace.name: [shop]",
	}, ns["pvc_requests"].request)

	// One claim when named; else the workload's claims by prefix, as Prometheus's "<name>.*".
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.persistentvolumeclaim.name: [data-db-0]",
		swPVCFilter(RequestMetadata{Kind: "pvc", Namespace: "shop", PVCName: "data-db-0"}))
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.persistentvolumeclaim.name:db*",
		swPVCFilter(RequestMetadata{Kind: "statefulset", Namespace: "shop", Name: "db"}))

	// Volume rightsizing asks with no kind and no namespace: every claim in the cluster.
	cluster := buildSolarWindsClusterPlans(keys)
	assert.Equal(t, map[string]any{"aggregateBy": "AVG", "groupBy": "k8s.namespace.name,k8s.persistentvolumeclaim.name"},
		cluster["pvc_capacity_by_claim"].request)
	assert.NotContains(t, cluster["pvc_usage"].request, "groupBy")
}

func TestSolarWindsNodePlans(t *testing.T) {
	meta := RequestMetadata{Kind: "node", NodeName: "node-1.*"}
	plans := buildSolarWindsNodePlans(meta, []string{
		"cpu_request", "memory_request", "cpu_limit", "memory_limit", "container_memory_working_set_by_pod", "active_nodes", "cpu_usage",
	})
	for key, metric := range map[string]string{
		"cpu_request":    "k8s.container.spec.cpu.requests",
		"memory_request": "k8s.container.spec.memory.requests",
		"cpu_limit":      "k8s.container.spec.cpu.limit",
		"memory_limit":   "k8s.container.spec.memory.limit",
	} {
		assert.Equal(t, metric, plans[key].metric, key)
		assert.Equal(t, map[string]any{
			"aggregateBy": "SUM", "preGroupBy": "k8s.pod.name,k8s.container.name", "preGroupByMethod": "AVG",
			"groupBy": "k8s.node.name", "filter": "k8s.node.name: [node-1]",
		}, plans[key].request, key)
	}
	assert.Equal(t, map[string]any{
		"aggregateBy": "AVG", "groupBy": "k8s.namespace.name,k8s.pod.name,k8s.container.name", "filter": "k8s.node.name: [node-1]",
	}, plans["container_memory_working_set_by_pod"].request)
	assert.Equal(t, "k8s.kube_node_info", plans["active_nodes"].metric)
	assert.NotContains(t, plans, "cpu_usage", "the shared node call answers it")

	// The node trends page asks for active_nodes with kind "node" and no node: every node.
	all := buildSolarWindsNodePlans(RequestMetadata{Kind: "node"}, []string{"cpu_request", "active_nodes"})
	assert.NotContains(t, all, "cpu_request", "no node, no scope")
	assert.Equal(t, map[string]any{"aggregateBy": "AVG", "groupBy": "k8s.node.name"}, all["active_nodes"].request)
}

func TestSolarWindsClusterTrendsAreTheNodeTotals(t *testing.T) {
	got := buildSolarWindsClusterSumQueries([]string{"cpu_usage_trend", "mem_usage_trend"})
	assert.Equal(t, map[string]string{
		"cpu_usage_trend": "k8s.node.cpu.usage.seconds.rate",
		"mem_usage_trend": "k8s.node.memory.working_set",
	}, got)
	// As on Prometheus, active_nodes is a node-scope key: one series per node is its
	// shape, which is not a cluster tile.
	assert.Empty(t, buildSolarWindsClusterPlans([]string{"active_nodes"}))
}

// Keys SWO has no equivalent for build nothing, so they are answered with a note.
func TestSolarWindsPlans_UnanswerableKeysBuildNothing(t *testing.T) {
	keys := []string{
		"workload_http_request_rate", "workload_http_request_count", "workload_http_error_count",
		"workload_http_error_rate", "workload_http_latency_mean", "workload_http_latency_p99",
		"http_status", "http_throughput", "http_error_rate", "cpu_throttling", "network_usage",
		"container_oomkilled_limit_by_pod", "disk_total", "no_of_pods",
		"network_receive_bytes_increase", "network_transmit_bytes_increase",
	}
	assert.Empty(t, buildSolarWindsWorkloadPlans(swWorkloadMeta, keys))
	assert.Empty(t, buildSolarWindsNodePlans(RequestMetadata{Kind: "node", NodeName: "n1"}, keys))
	assert.Empty(t, buildSolarWindsClusterPlans(keys))
	assert.Empty(t, buildSolarWindsClusterSumQueries(keys))
}

func TestSolarWindsApplyPlans(t *testing.T) {
	plans := map[string]swPlan{
		"container_cpu_usage_by_pod": {perContainer: true},
		"network_transmit_packets":   {negate: true},
	}
	results := []QueryResult{
		{QueryKey: "container_cpu_usage_by_pod", Payload: []Result{
			{Metric: map[string]string{"pod": "web-1", "container": "app"}, Values: []float64{0.2}},
			{Metric: map[string]string{"pod": "web-1", "container": ""}, Values: []float64{0.1}},
			{Metric: map[string]string{"pod": "web-1", "container": "POD"}, Values: []float64{0.0}},
		}},
		{QueryKey: "network_transmit_packets", Payload: []Result{{Metric: map[string]string{}, Values: []float64{10, 20}}}},
		{QueryKey: "cpu_usage", Payload: []Result{{Values: []float64{1}}}},
	}
	swApplyPlans(results, plans)

	require.Len(t, results[0].Payload, 1, "series with no container cannot be reported per container")
	assert.Equal(t, "app", results[0].Payload[0].Metric["container"])
	assert.Equal(t, []float64{-10, -20}, results[1].Payload[0].Values)
	assert.Equal(t, []float64{1}, results[2].Payload[0].Values, "keys without a plan are untouched")
}

func TestSolarWindsApplyPlans_DropsTheEndMarker(t *testing.T) {
	plans := map[string]swPlan{"container_cpu_request_by_pod": {dropEndMarker: true}}
	results := []QueryResult{{QueryKey: "container_cpu_request_by_pod", Payload: []Result{
		{Metric: map[string]string{"pod": "web-1"}, Timestamps: []int64{60, 120, 180}, Values: []float64{0.25, 0.25, 0}},
		{Metric: map[string]string{"pod": "web-2"}, Timestamps: []int64{60}, Values: []float64{0}},
		{Metric: map[string]string{"pod": "web-3"}, Timestamps: []int64{60, 120}, Values: []float64{0, 0.25}},
	}}}
	swApplyPlans(results, plans)
	require.Len(t, results[0].Payload, 2, "a series that held only the marker is a pod already gone")
	assert.Equal(t, []float64{0.25, 0.25}, results[0].Payload[0].Values)
	assert.Equal(t, []int64{60, 120}, results[0].Payload[0].Timestamps)
	assert.Equal(t, []float64{0, 0.25}, results[0].Payload[1].Values, "only the LAST reading can be the marker")
}

func TestSolarWindsPlans_EndMarkerOnlyOnGauges(t *testing.T) {
	plans := buildSolarWindsWorkloadPlans(swWorkloadMeta, []string{
		"container_cpu_usage_by_pod", "container_memory_working_set_by_pod", "container_memory_limit_by_pod",
		"cpu_usage_by_pod", "memory_usage_by_pod",
	})
	assert.False(t, plans["container_cpu_usage_by_pod"].dropEndMarker, "a CPU rate of 0 is a reading")
	assert.False(t, plans["cpu_usage_by_pod"].dropEndMarker)
	assert.True(t, plans["container_memory_working_set_by_pod"].dropEndMarker)
	assert.True(t, plans["container_memory_limit_by_pod"].dropEndMarker)
	assert.True(t, plans["memory_usage_by_pod"].dropEndMarker)
}

func TestSolarWindsPlanBatches_IdenticalRequestsShareACall(t *testing.T) {
	plans := buildSolarWindsWorkloadPlans(swWorkloadMeta,
		[]string{"disk_read_bytes", "network_receive_packet", "cpu_usage_by_pod", "memory_usage_by_pod", "replica_ready"})
	batches := swPlanBatches(plans)
	require.Len(t, batches, 3)
	sizes := map[int]int{}
	for _, b := range batches {
		sizes[len(b.queries)]++
	}
	assert.Equal(t, map[int]int{2: 2, 1: 1}, sizes)
}

func TestSolarWindsClaimLabel(t *testing.T) {
	v := 1.0
	qr := swGroupingsToQueryResult([]swMeasurementGrouping{{
		Attributes: []swMeasurementAttribute{
			{Key: "k8s.namespace.name", Value: "shop"},
			{Key: "k8s.persistentvolumeclaim.name", Value: "data-db-0"},
		},
		Measurements: []swBucketMeasurement{{Time: "2026-10-08T00:00:00Z", Value: &v}},
	}}, "pvc_used_by_claim", "k8s.kubelet_volume_stats_used_bytes", false)
	require.Len(t, qr.Payload, 1)
	assert.Equal(t, map[string]string{"namespace": "shop", "persistentvolumeclaim": "data-db-0"}, qr.Payload[0].Metric)
}
