package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Datadog utilisation query builders (buildDatadogNodeQueries /
// buildDatadogWorkloadQueries) — the node/workload paths FetchMetricUtilisation
// dispatches to for the datadog provider. Table-driven with hardcoded expected
// Datadog metric query strings so a template change is actually caught.
// ============================================================================

// ----------------------------------------------------------------------------
// buildDatadogNodeQueries
// ----------------------------------------------------------------------------

func TestBuildDatadogNodeQueries(t *testing.T) {
	meta := RequestMetadata{Kind: "node", NodeName: "node-1"}

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_usage scoped by host with host grouping",
			metric:   "cpu_usage",
			expected: "avg:kubernetes.cpu.usage.total{host:node-1} by {host}",
		},
		{
			name:     "memory_usage uses system.mem.used",
			metric:   "memory_usage",
			expected: "avg:system.mem.used{host:node-1} by {host}",
		},
		{
			name:     "cpu_request",
			metric:   "cpu_request",
			expected: "avg:kubernetes.cpu.requests{host:node-1} by {host}",
		},
		{
			name:     "disk_used",
			metric:   "disk_used",
			expected: "avg:system.disk.used{host:node-1} by {host}",
		},
		{
			name:     "pvc_usage is a ratio of disk used over total",
			metric:   "pvc_usage",
			expected: "(avg:system.disk.used{host:node-1} by {host} / avg:system.disk.total{host:node-1} by {host}) * 100",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDatadogNodeQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildDatadogNodeQueries_EmptyNodeReturnsEmpty(t *testing.T) {
	got := buildDatadogNodeQueries(RequestMetadata{Kind: "node"}, []string{"cpu_usage"})
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildDatadogNodeQueries_UnknownMetricSkipped(t *testing.T) {
	got := buildDatadogNodeQueries(RequestMetadata{Kind: "node", NodeName: "node-1"}, []string{"cpu_usage", "not_a_real_metric"})
	assert.Len(t, got, 1)
	assert.Contains(t, got, "cpu_usage")
	assert.NotContains(t, got, "not_a_real_metric")
}

// ----------------------------------------------------------------------------
// buildDatadogWorkloadQueries
// ----------------------------------------------------------------------------

func TestBuildDatadogWorkloadQueries_DeploymentResourceMetrics(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_usage filtered by namespace + deployment tag, grouped by deployment",
			metric:   "cpu_usage",
			expected: "avg:kubernetes.cpu.usage.total{kube_namespace:shop, kube_deployment:web} by {kube_deployment}",
		},
		{
			name:     "memory_usage uses working_set",
			metric:   "memory_usage",
			expected: "avg:kubernetes.memory.working_set{kube_namespace:shop, kube_deployment:web} by {kube_deployment}",
		},
		{
			name:     "cpu_request",
			metric:   "cpu_request",
			expected: "avg:kubernetes.cpu.requests{kube_namespace:shop, kube_deployment:web} by {kube_deployment}",
		},
		{
			name:     "cluster aggregation cpu_real ignores the workload filter",
			metric:   "cpu_real",
			expected: "sum:kubernetes.cpu.usage.total{*}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDatadogWorkloadQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildDatadogWorkloadQueries_KindSelectsTagKey(t *testing.T) {
	// Each kind maps to its own Datadog tag key for both the filter and the groupBy.
	cases := []struct {
		kind   string
		tagKey string
	}{
		{"deployment", "kube_deployment"},
		{"statefulset", "kube_stateful_set"},
		{"daemonset", "kube_daemon_set"},
		{"pod", "pod_name"},
		{"", "kube_deployment"}, // default falls back to deployment
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			meta := RequestMetadata{Kind: tc.kind, Namespace: "shop", Name: "web"}
			got := buildDatadogWorkloadQueries(meta, []string{"cpu_usage"})
			assert.Contains(t, got["cpu_usage"], tc.tagKey+":web")
			assert.Contains(t, got["cpu_usage"], "by {"+tc.tagKey+"}")
		})
	}
}

func TestBuildDatadogWorkloadQueries_ContainerNarrowsFilter(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web", ContainerName: "sidecar"}
	got := buildDatadogWorkloadQueries(meta, []string{"cpu_usage"})
	assert.Contains(t, got["cpu_usage"], "kube_container_name:sidecar")
}

func TestBuildDatadogWorkloadQueries_NoWorkloadContextReturnsEmpty(t *testing.T) {
	// No namespace/name -> filterStr empty -> builder returns before emitting anything.
	got := buildDatadogWorkloadQueries(RequestMetadata{Kind: "deployment"}, []string{"cpu_usage"})
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildDatadogWorkloadQueries_UnknownMetricSkipped(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}
	got := buildDatadogWorkloadQueries(meta, []string{"cpu_usage", "not_a_real_metric"})
	assert.Len(t, got, 1)
	assert.Contains(t, got, "cpu_usage")
	assert.NotContains(t, got, "not_a_real_metric")
}
