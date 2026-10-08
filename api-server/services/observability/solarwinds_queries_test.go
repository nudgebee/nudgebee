package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// swBuildMultiValueFilter
// ============================================================================

func TestSWBuildMultiValueFilter(t *testing.T) {
	cases := []struct {
		name     string
		attr     string
		input    string
		expected string
	}{
		{
			name:     "single exact value",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1",
			expected: "k8s.node.name: [gke-cluster-node1]",
		},
		{
			// Prometheus-style ".*" suffix is stripped to recover the exact SWO node name.
			// SWO measurements filter does not support wildcards inside [...].
			name:     "single value with prometheus trailing wildcard",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1.*",
			expected: "k8s.node.name: [gke-cluster-node1]",
		},
		{
			name:     "multiple pipe-separated values each with trailing wildcard",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1.*|gke-cluster-node2.*|gke-cluster-node3.*",
			expected: "k8s.node.name: [gke-cluster-node1,gke-cluster-node2,gke-cluster-node3]",
		},
		{
			// The exact node names from the API request, as sent by the UI.
			name:     "real GKE node names pipe-separated with prometheus wildcards",
			attr:     "k8s.node.name",
			input:    "gke-test-cluster-dev-runner-node-pool-v2-8f4d877c-lt65.*|gke-test-cluster-dev-runner-node-pool-v2-8f4d877c-5nvc.*",
			expected: "k8s.node.name: [gke-test-cluster-dev-runner-node-pool-v2-8f4d877c-lt65,gke-test-cluster-dev-runner-node-pool-v2-8f4d877c-5nvc]",
		},
		{
			// Mid-string "*" (not a trailing ".*") falls back to space-joined "attr:val*" format
			// because SWO only supports glob wildcards outside brackets.
			name:     "glob wildcard mid-string uses attr:val format",
			attr:     "k8s.node.name",
			input:    "gke-cluster-*-node",
			expected: "k8s.node.name:gke-cluster-*-node",
		},
		{
			name:     "empty string returns empty",
			attr:     "k8s.node.name",
			input:    "",
			expected: "",
		},
		{
			name:     "blank tokens dropped",
			attr:     "k8s.node.name",
			input:    "node1||node2|",
			expected: "k8s.node.name: [node1,node2]",
		},
		{
			name:     "whitespace trimmed around tokens",
			attr:     "k8s.node.name",
			input:    " node1 | node2 ",
			expected: "k8s.node.name: [node1,node2]",
		},
		{
			name:     "no wildcard in exact name",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1|gke-cluster-node2",
			expected: "k8s.node.name: [gke-cluster-node1,gke-cluster-node2]",
		},
		{
			name:     "mixed wildcards fall back to attr:val format",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1*|gke-cluster-node2*",
			expected: "k8s.node.name:gke-cluster-node1* k8s.node.name:gke-cluster-node2*",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, swBuildMultiValueFilter(tc.attr, tc.input))
		})
	}
}

// ============================================================================
// buildSolarWindsFilter
// ============================================================================

func TestBuildSolarWindsFilter(t *testing.T) {
	t.Run("node kind with pipe-separated node names strips prometheus wildcards", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-cluster-lt65.*|gke-cluster-5nvc.*",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-cluster-lt65,gke-cluster-5nvc]", got)
	})

	t.Run("node kind with single node name", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-cluster-lt65",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-cluster-lt65]", got)
	})

	t.Run("workload kind with namespace and deployment", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:      "deployment",
			Namespace: "nudgebee",
			Name:      "api-server",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.namespace.name: [nudgebee] k8s.deployment.name: [api-server]", got)
	})

	t.Run("cluster level returns empty filter", func(t *testing.T) {
		meta := RequestMetadata{}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "", got)
	})

	t.Run("node name with prometheus trailing wildcard stripped to exact value", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-test-cluster-dev-c4-amd-spot-v2-02132c6e-spkf.*",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-test-cluster-dev-c4-amd-spot-v2-02132c6e-spkf]", got)
	})
}

// ============================================================================
// buildSolarWindsNodeQueries / buildSolarWindsWorkloadQueries
//
// These return metricKey -> SWO metric name maps; the namespace/workload filter
// and groupBy are built separately (buildSolarWindsRequestParams) and passed as
// FetchMetricsRequest.Request, so here we only assert the metric-name mapping.
// ============================================================================

func TestBuildSolarWindsNodeQueries(t *testing.T) {
	cases := []struct {
		metric   string
		expected string
	}{
		{"cpu_usage", "k8s.node.cpu.usage.seconds.rate"},
		{"cpu_usage_line", "k8s.node.cpu.usage.seconds.rate"},
		{"memory_usage", "k8s.node.memory.working_set"},
		{"memory_usage_line", "k8s.node.memory.working_set"},
		{"cpu_allocatable", "k8s.node.cpu.allocatable"},
		{"memory_allocatable", "k8s.node.memory.allocatable"},
		{"disk_used", "k8s.node.fs.usage"},
	}
	for _, tc := range cases {
		t.Run(tc.metric, func(t *testing.T) {
			got := buildSolarWindsNodeQueries("node-1", []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildSolarWindsNodeQueries_EmptyNodeReturnsEmpty(t *testing.T) {
	got := buildSolarWindsNodeQueries("", []string{"cpu_usage"})
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildSolarWindsNodeQueries_UnsupportedMetricSkipped(t *testing.T) {
	// No per-node pod-request rollup metric exists in SWO, so cpu_request is skipped.
	got := buildSolarWindsNodeQueries("node-1", []string{"cpu_usage", "cpu_request"})
	assert.Len(t, got, 1)
	assert.Contains(t, got, "cpu_usage")
	assert.NotContains(t, got, "cpu_request")
}

func TestBuildSolarWindsWorkloadQueries(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}

	cases := []struct {
		metric   string
		expected string
	}{
		{"cpu_usage", "k8s.pod.cpu.usage.seconds.rate"},
		{"cpu_usage_line", "k8s.pod.cpu.usage.seconds.rate"},
		{"memory_usage", "k8s.pod.memory.working_set"},
		{"memory_usage_line", "k8s.pod.memory.working_set"},
		{"cpu_request", "k8s.container.spec.cpu.requests"},
		{"memory_request", "k8s.container.spec.memory.requests"},
		{"cpu_limit", "k8s.container.spec.cpu.limit"},
		{"memory_limit", "k8s.container.spec.memory.limit"},
	}
	for _, tc := range cases {
		t.Run(tc.metric, func(t *testing.T) {
			got := buildSolarWindsWorkloadQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildSolarWindsWorkloadQueries_EmptyContextReturnsEmpty(t *testing.T) {
	got := buildSolarWindsWorkloadQueries(RequestMetadata{Kind: "deployment"}, []string{"cpu_usage"})
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestBuildSolarWindsWorkloadQueries_UnknownMetricSkipped(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}
	got := buildSolarWindsWorkloadQueries(meta, []string{"cpu_usage", "not_a_real_metric"})
	assert.Len(t, got, 1)
	assert.Contains(t, got, "cpu_usage")
	assert.NotContains(t, got, "not_a_real_metric")
}

// ============================================================================
// SUM across entities pre-groups by the entity (P1, P8)
// ============================================================================

func TestSolarWindsWorkloadBatches_PreGroupByEntityPerKey(t *testing.T) {
	meta := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}
	queries := buildSolarWindsWorkloadQueries(meta, []string{"cpu_usage", "memory_usage", "cpu_request", "memory_limit"})

	batches := swWorkloadBatches(meta, queries)

	byEntity := map[string]map[string]string{}
	for _, b := range batches {
		assert.Equal(t, "SUM", b.request["aggregateBy"])
		assert.Equal(t, "AVG", b.request["preGroupByMethod"])
		assert.Equal(t, "k8s.namespace.name,k8s.deployment.name", b.request["groupBy"])
		assert.Equal(t, "k8s.namespace.name: [shop] k8s.deployment.name: [web]", b.request["filter"])
		byEntity[b.request["preGroupBy"].(string)] = b.queries
	}
	assert.Equal(t, map[string]map[string]string{
		// Usage is reported per pod.
		"k8s.pod.name": {"cpu_usage": "k8s.pod.cpu.usage.seconds.rate", "memory_usage": "k8s.pod.memory.working_set"},
		// Specs are per container: pod alone averages a sidecar pod's containers (0.965 of truth).
		"k8s.pod.name,k8s.container.name": {"cpu_request": "k8s.container.spec.cpu.requests", "memory_limit": "k8s.container.spec.memory.limit"},
	}, byEntity)
}

func TestSolarWindsWorkloadBatches_EmptyQueries(t *testing.T) {
	assert.Empty(t, swWorkloadBatches(RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}, map[string]string{}))
}

func TestBuildSolarWindsRequestParams_PreGroupBy(t *testing.T) {
	got := buildSolarWindsRequestParams(RequestMetadata{}, "", "SUM", "k8s.node.name")
	assert.Equal(t, map[string]any{"aggregateBy": "SUM", "preGroupBy": "k8s.node.name", "preGroupByMethod": "AVG"}, got)

	node := buildSolarWindsRequestParams(RequestMetadata{Kind: "node", NodeName: "n1"}, "k8s.node.name", "AVG", "")
	assert.NotContains(t, node, "preGroupBy", "an AVG over one entity's own series needs no pre-grouping")
}

// ============================================================================
// pod_names
// ============================================================================

func TestBuildSolarWindsFilter_PodNames(t *testing.T) {
	base := RequestMetadata{Kind: "deployment", Namespace: "shop", Name: "web"}

	listed := base
	listed.PodNames, listed.PodNamesSupplied = []string{"web-7d9f", "web-3b2c"}, true
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.deployment.name: [web] k8s.pod.name: [web-7d9f,web-3b2c]", buildSolarWindsFilter(listed))

	none := base
	none.PodNames, none.PodNamesSupplied = []string{}, true
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.deployment.name: [web] k8s.pod.name: [__no_pods_resolved__]", buildSolarWindsFilter(none),
		"a resolved-empty list must match nothing, not the whole workload")

	assert.Equal(t, "k8s.namespace.name: [shop] k8s.deployment.name: [web]", buildSolarWindsFilter(base), "absent list keeps the workload scope")
}

// The UI's utilisation chart sends kind as written in manifests ("Deployment"). A
// case-sensitive match dropped the workload clause, so the chart for one workload
// showed its whole namespace (9.48 cores requested against the workload's 0.2).
func TestBuildSolarWindsFilter_WorkloadKindIsNeverDropped(t *testing.T) {
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.deployment.name: [web]",
		buildSolarWindsFilter(RequestMetadata{Kind: "Deployment", Namespace: "shop", Name: "web"}))
	assert.Equal(t, "k8s.namespace.name: [shop] k8s.statefulset.name: [db]",
		buildSolarWindsFilter(RequestMetadata{Kind: " StatefulSet ", Namespace: "shop", Name: "db"}))
	assert.Equal(t, "k8s.namespace.name: [shop] (k8s.deployment.name: [web] OR k8s.statefulset.name: [web] OR k8s.daemonset.name: [web])",
		buildSolarWindsFilter(RequestMetadata{Namespace: "shop", Name: "web"}),
		"a workload named with no kind is still scoped to that workload")
	assert.Equal(t, "k8s.namespace.name: [shop] (k8s.deployment.name: [web] OR k8s.statefulset.name: [web] OR k8s.daemonset.name: [web])",
		buildSolarWindsFilter(RequestMetadata{Kind: "Rollout", Namespace: "shop", Name: "web"}),
		"a kind SolarWinds does not tag falls back to every kind it does")
}
