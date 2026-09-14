package observability

import (
	"strings"
	"testing"
)

// ============================================================================
// CubeAPM utilisation wiring.
//
// FetchMetricUtilisation routes cubeapm through the shared buildPrometheus*Queries
// builders (CubeAPM stores whatever its collector writes; on Kubernetes that is the
// cAdvisor / node-exporter / kube-state-metrics families those builders target, and
// its engine speaks PromQL). Those builders emit the relay-only __CLUSTER__ token,
// which only relay-server substitutes — so the direct-API source has to strip it or
// every utilisation query dies as a parse error and the panel renders empty.
//
// These tests pin both halves of that contract.
// ============================================================================

// clusterPanelMetrics is exactly what the "Utilization & Health" card asks for,
// across its three parallel calls plus the Trend popup. Kept verbatim so a
// builder that stops answering one of them fails here rather than in the browser.
// Source: app/src/components/k8s/common/KubernetesMemoryCpuOverView.jsx.
var clusterPanelMetrics = []string{
	// cpu call
	"cpu_real", "cpu_total", "cpu_request", "cpu_limit",
	// memory call
	"mem_real", "mem_total", "memory_limit", "memory_request",
	// percentile call
	"p90_mem", "p90_cpu", "p50_mem", "p50_cpu", "max_usage_mem", "max_usage_cpu",
	// trend popup (range queries)
	"cpu_usage_trend", "mem_usage_trend",
}

func TestStripClusterPlaceholder(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "placeholder alone in selector leaves an empty matcher set",
			in:       `sum(machine_cpu_cores{__CLUSTER__})`,
			expected: `sum(machine_cpu_cores{})`,
		},
		{
			name:     "placeholder followed by a matcher",
			in:       `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m]))`,
			expected: `sum(rate(node_cpu_seconds_total{ mode!="idle"}[5m]))`,
		},
		{
			name:     "workload filter with the builder's trailing comma",
			in:       `sum(container_memory_working_set_bytes{__CLUSTER__ namespace="prod", pod=~"api-.*", container!="",})`,
			expected: `sum(container_memory_working_set_bytes{ namespace="prod", pod=~"api-.*", container!="",})`,
		},
		{
			name:     "every occurrence is removed, not just the first",
			in:       `sum(a{__CLUSTER__}) - sum(b{__CLUSTER__})`,
			expected: `sum(a{}) - sum(b{})`,
		},
		{
			name:     "a query without the token is untouched",
			in:       `sum(rate(container_cpu_usage_seconds_total{namespace="prod"}[5m]))`,
			expected: `sum(rate(container_cpu_usage_seconds_total{namespace="prod"}[5m]))`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripClusterPlaceholder(tc.in); got != tc.expected {
				t.Errorf("stripClusterPlaceholder()\n got: %s\nwant: %s", got, tc.expected)
			}
		})
	}
}

// The builders must answer every key the card requests — an unhandled key is
// silently skipped by the switch, which reads in the UI as a zeroed gauge.
func TestPrometheusBuildersAnswerEveryClusterPanelMetric(t *testing.T) {
	meta := RequestMetadata{
		RangeWindow: "604800s",
		Step:        "1800s",
		RateWindow:  "1800s",
	}

	queries := buildPrometheusWorkloadQueries(meta, clusterPanelMetrics)

	for _, key := range clusterPanelMetrics {
		if q, ok := queries[key]; !ok || strings.TrimSpace(q) == "" {
			t.Errorf("metric %q produced no query; the Utilization & Health card would show 0", key)
		}
	}
}

// The end-to-end contract: whatever the builders hand CubeAPM, what goes on the
// wire carries no placeholder.
func TestCubeAPMRenderQueryStripsPlaceholderForEveryClusterPanelMetric(t *testing.T) {
	meta := RequestMetadata{
		RangeWindow: "604800s",
		Step:        "1800s",
		RateWindow:  "1800s",
	}

	for key, raw := range buildPrometheusWorkloadQueries(meta, clusterPanelMetrics) {
		t.Run(key, func(t *testing.T) {
			if !strings.Contains(raw, clusterPlaceholder) {
				t.Skipf("%s carries no placeholder to strip", key)
			}
			rendered, err := cubeAPMRenderQuery(raw, nil, nil)
			if err != nil {
				t.Fatalf("cubeAPMRenderQuery(%s) failed: %v", key, err)
			}
			if strings.Contains(rendered, clusterPlaceholder) {
				t.Errorf("%s still carries %s after rendering: %s", key, clusterPlaceholder, rendered)
			}
		})
	}
}

// Node-scoped utilisation (the Nodes tab) goes through the other builder, and has
// the same placeholder problem.
func TestCubeAPMRenderQueryStripsPlaceholderForNodeQueries(t *testing.T) {
	meta := RequestMetadata{
		Kind:       "node",
		InternalIP: "10.0.0.1",
		NodeName:   "node-1",
	}

	for key, raw := range buildPrometheusNodeQueries(meta, []string{"cpu_usage", "memory_usage", "cpu_request", "memory_request", "cpu_limit"}) {
		rendered, err := cubeAPMRenderQuery(raw, nil, nil)
		if err != nil {
			t.Fatalf("cubeAPMRenderQuery(%s) failed: %v", key, err)
		}
		if strings.Contains(rendered, clusterPlaceholder) {
			t.Errorf("node metric %s still carries %s: %s", key, clusterPlaceholder, rendered)
		}
	}
}

// The source's own preview path must agree with the execution path — the UI shows
// users the GetQuery output as "the query that ran".
func TestCubeAPMGetQueryStripsClusterPlaceholder(t *testing.T) {
	s := &CubeAPMMetricSource{}

	got, err := s.GetQuery(nil, FetchMetricsRequest{
		Queries: map[string]string{
			"cpu_total": `sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})`,
		},
	})
	if err != nil {
		t.Fatalf("GetQuery failed: %v", err)
	}
	if strings.Contains(got, clusterPlaceholder) {
		t.Errorf("GetQuery leaked %s: %s", clusterPlaceholder, got)
	}
	if want := `sum(machine_cpu_cores{}) or sum(node_resources_cpu_logical_cores{})`; got != want {
		t.Errorf("GetQuery()\n got: %s\nwant: %s", got, want)
	}
}

// Stripping must not disturb label injection: the BUILDER path still layers its
// matchers into the same selector after the token is gone.
func TestCubeAPMRenderQueryKeepsMatcherInjection(t *testing.T) {
	got, err := cubeAPMRenderQuery(
		`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m]))`,
		[]LabelMatcher{{Label: "job", Operator: "_eq", Value: "node-exporter"}},
		nil,
	)
	if err != nil {
		t.Fatalf("cubeAPMRenderQuery failed: %v", err)
	}
	if strings.Contains(got, clusterPlaceholder) {
		t.Errorf("rendered query still carries %s: %s", clusterPlaceholder, got)
	}
	if !strings.Contains(got, `job="node-exporter"`) {
		t.Errorf("matcher was not injected: %s", got)
	}
}
