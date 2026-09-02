package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Prometheus utilisation query builders (buildPrometheusNodeQueries /
// buildPrometheusWorkloadQueries) — the workload/node paths FetchMetricUtilisation
// dispatches to for prometheus / chronosphere / victoria_metrics.
//
// Style mirrors solarwinds_queries_test.go / service_promql_test.go: table-driven
// with hardcoded expected PromQL (not re-derived via fmt.Sprintf, so a change to a
// query template is actually caught). The __CLUSTER__ token is a placeholder the
// metric source substitutes later, so it appears verbatim in expectations.
// ============================================================================

// ----------------------------------------------------------------------------
// buildPrometheusNodeQueries
// ----------------------------------------------------------------------------

func TestBuildPrometheusNodeQueries(t *testing.T) {
	// meta exercises all three node identity fields the builder substitutes.
	meta := RequestMetadata{
		Kind:       "node",
		InternalIP: "10.0.0.1",
		NodeName:   "node-1",
		NodeIP:     "192.168.1.1",
	}

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_usage uses internal IP and node name",
			metric:   "cpu_usage",
			expected: `sum(irate(node_cpu_seconds_total{mode!="idle", instance=~"10.0.0.1.*"}[5m])) OR sum(irate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"node-1.*"}[5m]))`,
		},
		{
			name:     "memory_usage uses internal IP and node name",
			metric:   "memory_usage",
			expected: `sum(node_memory_Active_bytes{instance=~"10.0.0.1.*"}) or sum(node_resources_memory_total_bytes{instance=~"node-1.*"} - node_resources_memory_available_bytes{instance=~"node-1.*"})`,
		},
		{
			// The node-agent fallback (node_resources_cpu_usage_seconds_total) must match on
			// instance=~node-name, not node=~ — that metric carries an `instance` label, not `node`,
			// so a node=~ filter returns empty and the node table renders CPU as 0%.
			name:     "cpu_usage_line node-agent fallback matches on instance not node",
			metric:   "cpu_usage_line",
			expected: `sum by (instance) (rate(node_cpu_seconds_total{mode!="idle", instance=~"10.0.0.1|node-1"}[5m])) or (sum by (node) (rate(node_cpu_seconds_total{mode!="idle", node=~"node-1"}[5m]))) or (sum by (instance) (rate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"node-1"}[5m])))`,
		},
		{
			name:     "memory_usage_line tries instance clauses before node clauses",
			metric:   "memory_usage_line",
			expected: `(avg(node_memory_MemTotal_bytes{instance=~"10.0.0.1|node-1"} - node_memory_MemAvailable_bytes{instance=~"10.0.0.1|node-1"}) by (instance)) or (avg(node_resources_memory_total_bytes{instance=~"node-1"} - node_resources_memory_available_bytes{instance=~"node-1"}) by (instance)) or (avg(node_memory_MemTotal_bytes{node=~"node-1"} - node_memory_MemAvailable_bytes{node=~"node-1"}) by (node)) or (avg(node_resources_memory_total_bytes{node=~"node-1"} - node_resources_memory_available_bytes{node=~"node-1"}) by (node))`,
		},
		{
			name:     "cpu_request scoped by node name",
			metric:   "cpu_request",
			expected: `sum(kube_pod_container_resource_requests{resource="cpu", node=~"node-1.*"})`,
		},
		{
			name:     "memory_request scoped by node name",
			metric:   "memory_request",
			expected: `sum(kube_pod_container_resource_requests{resource="memory", node=~"node-1.*"})`,
		},
		{
			name:     "cpu_limit scoped by node name",
			metric:   "cpu_limit",
			expected: `sum(kube_pod_container_resource_limits{resource="cpu", node=~"node-1.*"})`,
		},
		{
			name:     "memory_limit scoped by node name",
			metric:   "memory_limit",
			expected: `sum(kube_pod_container_resource_limits{resource="memory", node=~"node-1.*"})`,
		},
		{
			name:     "disk_total falls back across internal IP, node name and node IP",
			metric:   "disk_total",
			expected: `sum(node_filesystem_size_bytes{mountpoint="/", instance=~"10.0.0.1.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"node-1.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"192.168.1.1.*"})`,
		},
		{
			// Virtual block devices (loop/ram/dm-*) sit on top of the physical disk, so
			// summing them in would double-count every byte read.
			name:     "disk_read_bytes falls back across internal IP, node name and node IP",
			metric:   "disk_read_bytes",
			expected: `sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"10.0.0.1.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"node-1.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"192.168.1.1.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`,
		},
		{
			// node-exporter spells the write counter "written", not "write".
			name:     "disk_write_bytes falls back across internal IP, node name and node IP",
			metric:   "disk_write_bytes",
			expected: `sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"10.0.0.1.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"node-1.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"192.168.1.1.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`,
		},
		{
			name:     "node_az is a static karpenter query with no substitution",
			metric:   "node_az",
			expected: `count(karpenter_nodes_total_pod_requests{ __CLUSTER__ provisioner_name="",resource_type="pods"}) by (zone)`,
		},
		{
			name:     "node_pool_pod_trend is a static karpenter query",
			metric:   "node_pool_pod_trend",
			expected: `sum by (nodepool)(karpenter_pods_state{__CLUSTER__})`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrometheusNodeQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildPrometheusNodeQueries_EscapesPromQLSpecialChars(t *testing.T) {
	// Node identity fields flow from request input through escapePromQLString before
	// interpolation, so a quote can't break out of the PromQL string literal.
	meta := RequestMetadata{Kind: "node", InternalIP: `10.0.0.1`, NodeName: `node"evil`, NodeIP: `192.168.1.1`}
	got := buildPrometheusNodeQueries(meta, []string{"cpu_usage_line", "disk_total"})
	assert.Contains(t, got["cpu_usage_line"], `node\"evil`)
	assert.NotContains(t, got["cpu_usage_line"], `node"evil`)
	assert.Contains(t, got["disk_total"], `node\"evil`)
}

func TestBuildPrometheusNodeQueries_MultipleMetrics(t *testing.T) {
	meta := RequestMetadata{Kind: "node", InternalIP: "10.0.0.1", NodeName: "node-1", NodeIP: "192.168.1.1"}

	got := buildPrometheusNodeQueries(meta, []string{"cpu_usage", "memory_usage", "cpu_request"})

	assert.Len(t, got, 3)
	assert.Contains(t, got, "cpu_usage")
	assert.Contains(t, got, "memory_usage")
	assert.Contains(t, got, "cpu_request")
}

func TestBuildPrometheusNodeQueries_UnknownMetricSkipped(t *testing.T) {
	meta := RequestMetadata{Kind: "node", NodeName: "node-1"}

	// A mix of a known and an unknown key: only the known one is emitted, and the
	// builder never returns nil (it always allocates the map).
	got := buildPrometheusNodeQueries(meta, []string{"cpu_request", "not_a_real_metric"})

	assert.NotNil(t, got)
	assert.Len(t, got, 1)
	assert.Contains(t, got, "cpu_request")
	assert.NotContains(t, got, "not_a_real_metric")
}

func TestBuildPrometheusNodeQueries_EmptyMetrics(t *testing.T) {
	got := buildPrometheusNodeQueries(RequestMetadata{Kind: "node", NodeName: "node-1"}, nil)

	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// ----------------------------------------------------------------------------
// buildPrometheusWorkloadQueries
//
// Exact-match pins the resource-metric PromQL; the kind- and container-dependent
// filter behaviour is asserted via fragments so the intent stays readable.
// ----------------------------------------------------------------------------

func deploymentMeta() RequestMetadata {
	return RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}
}

func TestBuildPrometheusWorkloadQueries_DeploymentResourceMetrics(t *testing.T) {
	meta := deploymentMeta()

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_usage rate over container_cpu_usage_seconds_total",
			metric:   "cpu_usage",
			expected: `sum(rate(container_cpu_usage_seconds_total{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",}[5m]))`,
		},
		{
			name:     "memory_usage over working set bytes",
			metric:   "memory_usage",
			expected: `sum(container_memory_working_set_bytes{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",})`,
		},
		{
			name:     "cpu_request from kube-state-metrics requests",
			metric:   "cpu_request",
			expected: `sum(kube_pod_container_resource_requests{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",resource="cpu"})`,
		},
		{
			name:     "cpu_limit from kube-state-metrics limits",
			metric:   "cpu_limit",
			expected: `sum(kube_pod_container_resource_limits{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",resource="cpu"})`,
		},
		{
			name:     "memory_limit from kube-state-metrics limits",
			metric:   "memory_limit",
			expected: `sum(kube_pod_container_resource_limits{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",resource="memory"})`,
		},
		{
			// Preferred form excludes the pod-level rollup (container!=""); the `or`
			// fallback drops that constraint for runtimes that only publish the rollup.
			// `or` is not addition — the fallback is evaluated only when the left side
			// returns no series, so the two can never be double-counted.
			name:     "disk_read_bytes prefers per-container series with pod-level fallback",
			metric:   "disk_read_bytes",
			expected: `(sum(rate(container_fs_reads_bytes_total{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",}[5m]))) or (sum(rate(container_fs_reads_bytes_total{__CLUSTER__  namespace="shop", pod=~"web-.*",job!=""}[5m])))`,
		},
		{
			name:     "disk_write_bytes prefers per-container series with pod-level fallback",
			metric:   "disk_write_bytes",
			expected: `(sum(rate(container_fs_writes_bytes_total{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",}[5m]))) or (sum(rate(container_fs_writes_bytes_total{__CLUSTER__  namespace="shop", pod=~"web-.*",job!=""}[5m])))`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrometheusWorkloadQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildPrometheusWorkloadQueries_DeploymentUsesRegexPodMatcher(t *testing.T) {
	// Non-pod kinds match all pods owned by the workload via a regex prefix.
	got := buildPrometheusWorkloadQueries(deploymentMeta(), []string{"cpu_usage"})
	assert.Contains(t, got["cpu_usage"], `pod=~"web-.*"`)
	assert.NotContains(t, got["cpu_usage"], `pod="web"`)
}

func TestBuildPrometheusWorkloadQueries_PodKindUsesExactPodMatcher(t *testing.T) {
	// Kind "pod" targets a single pod by exact name, not a regex prefix.
	meta := RequestMetadata{Kind: "pod", Namespace: "shop", Name: "web-abc-123"}
	got := buildPrometheusWorkloadQueries(meta, []string{"cpu_usage"})
	assert.Contains(t, got["cpu_usage"], `pod="web-abc-123"`)
	assert.NotContains(t, got["cpu_usage"], `pod=~`)
}

func TestBuildPrometheusWorkloadQueries_ContainerNarrowsFilter(t *testing.T) {
	// When a container is specified the filter narrows to container="<name>"
	// instead of the catch-all container!="".
	meta := deploymentMeta()
	meta.ContainerName = "sidecar"
	got := buildPrometheusWorkloadQueries(meta, []string{"cpu_usage"})
	assert.Contains(t, got["cpu_usage"], `container="sidecar"`)
	assert.NotContains(t, got["cpu_usage"], `container!=""`)
}

func TestBuildPrometheusWorkloadQueries_EscapesPromQLSpecialChars(t *testing.T) {
	// Workload/namespace values flow through escapePromQLString before interpolation.
	meta := RequestMetadata{Kind: "deployment", Namespace: `ns"x`, Name: `web"y`}
	got := buildPrometheusWorkloadQueries(meta, []string{"cpu_usage"})
	assert.Contains(t, got["cpu_usage"], `namespace="ns\"x"`)
	assert.Contains(t, got["cpu_usage"], `pod=~"web\"y-.*"`)
}

func TestBuildPrometheusWorkloadQueries_MultipleAndUnknown(t *testing.T) {
	got := buildPrometheusWorkloadQueries(deploymentMeta(), []string{"cpu_usage", "memory_usage", "not_a_real_metric"})

	assert.NotNil(t, got)
	assert.Len(t, got, 2)
	assert.Contains(t, got, "cpu_usage")
	assert.Contains(t, got, "memory_usage")
	assert.NotContains(t, got, "not_a_real_metric")
}

func TestBuildPrometheusWorkloadQueries_EmptyMetrics(t *testing.T) {
	got := buildPrometheusWorkloadQueries(deploymentMeta(), nil)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// ----------------------------------------------------------------------------
// Cluster-level aggregations (cpu_real / p50_cpu / p90_cpu / max_usage_cpu /
// max_usage_mem / cpu_usage_trend). These are windowed by the picker range and the
// CPU percentile/peak queries must sum-across-series BEFORE aggregating over time,
// so the result stays bounded by physical capacity (no >100%).
// ----------------------------------------------------------------------------

func TestBuildPrometheusWorkloadQueries_ClusterAggregationsWindowed(t *testing.T) {
	// Windows as promAggWindow would produce for a 24h picker range.
	meta := RequestMetadata{RangeWindow: "86400s", Step: "288s", RateWindow: "300s"}

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_real averages over the picker range",
			metric:   "cpu_real",
			expected: `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[86400s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[86400s]))`,
		},
		{
			name:     "p90_cpu sums first then quantile_over_time (bounded)",
			metric:   "p90_cpu",
			expected: `quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "p50_cpu sums first then quantile_over_time (bounded)",
			metric:   "p50_cpu",
			expected: `quantile_over_time(0.50, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or quantile_over_time(0.50, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "max_usage_cpu sums first then max_over_time (bounded)",
			metric:   "max_usage_cpu",
			expected: `max_over_time(sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or max_over_time(sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "max_usage_mem windows the subquery by the picker range",
			metric:   "max_usage_mem",
			expected: `max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[86400s:288s])`,
		},
		{
			name:     "cpu_usage_trend is the instantaneous usage expression (short rate window)",
			metric:   "cpu_usage_trend",
			expected: `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrometheusWorkloadQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildPrometheusWorkloadQueries_ClusterAggregationsFallbackTo24h(t *testing.T) {
	// Direct-constructed metadata (as unit tests / non-picker callers use) has no
	// window fields; the builder falls back to a 24h window at 5m resolution.
	got := buildPrometheusWorkloadQueries(RequestMetadata{}, []string{"cpu_real", "p90_cpu"})
	assert.Equal(t, `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h]))`, got["cpu_real"])
	assert.Equal(t, `quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m]))[24h:5m]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[5m]))[24h:5m])`, got["p90_cpu"])
}

func TestPromAggWindow(t *testing.T) {
	const ms = int64(1000)

	cases := []struct {
		name                        string
		startMs, endMs              int64
		wantRange, wantStep, wantRt string
	}{
		{"zero range falls back to 24h", 0, 0, "86400s", "288s", "300s"},
		{"1h range clamps step to 1m, rate to 5m", 0, 3600 * ms, "3600s", "60s", "300s"},
		{"24h range", 0, 24 * 3600 * ms, "86400s", "288s", "300s"},
		{"7d range clamps step and rate to 30m", 0, 7 * 24 * 3600 * ms, "604800s", "1800s", "1800s"},
		{"inverted range falls back to 24h", 5000, 1000, "86400s", "288s", "300s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRange, gotStep, gotRate := promAggWindow(tc.startMs, tc.endMs)
			assert.Equal(t, tc.wantRange, gotRange)
			assert.Equal(t, tc.wantStep, gotStep)
			assert.Equal(t, tc.wantRt, gotRate)
		})
	}
}
