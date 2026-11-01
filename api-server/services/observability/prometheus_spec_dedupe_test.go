package observability

import (
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Series that more than one exporter publishes. OpenCost re-exports the request and
// allocatable series next to kube-state-metrics (measured on the dev cluster: 961 vs
// 963 request series, 138 allocatable series each), and a cluster may run more than
// one kube-state-metrics. A query that adds or counts them without first collapsing
// to one series per container or node reports every value once per exporter.
var promMultiExporterSpecMetrics = map[string]bool{
	"kube_pod_container_resource_requests": true,
	"kube_pod_container_resource_limits":   true,
	"kube_node_status_allocatable":         true,
	"kube_node_status_capacity":            true,
}

// Labels that name the thing a spec series describes. A copy from another exporter
// carries the same values for these and different values for its own scrape labels
// (on dev: job, instance and service all differ between kube-state-metrics and
// OpenCost), so grouping by these alone is what collapses the copies.
var promSpecTargetLabels = map[string]bool{
	"namespace": true, "pod": true, "container": true, "node": true,
	"resource": true, "unit": true, "uid": true, "cluster": true,
}

// promCollapsesExporters reports whether a max/min grouping merges exporter copies.
//
// Only a `by` whose every label identifies the target does. `without (job, instance)`
// is deliberately not trusted: the copies also differ in labels the query cannot
// name ahead of time (`service` on dev), and whatever is not listed survives.
func promCollapsesExporters(a *parser.AggregateExpr) bool {
	if a.Without {
		return false
	}
	for _, label := range a.Grouping {
		if !promSpecTargetLabels[label] {
			return false
		}
	}
	return true
}

// promUncollapsedSpecSelectors returns every multi-exporter selector in expr that
// reaches its consumer, or an adding aggregation, without passing through a max/min
// that drops the exporter labels.
//
// max and min are the only aggregations that are safe over duplicates: a copy of a
// value leaves them unchanged. sum and count double, and avg reweights. A max that
// keeps an exporter label (`job`) inherits whatever the aggregation above it decided,
// which is what lets the OOM-killed join key on job and collapse it afterwards.
func promUncollapsedSpecSelectors(t *testing.T, expr string) []string {
	t.Helper()
	root, err := promQLParser.ParseExpr(stripClusterPlaceholder(expr))
	require.NoErrorf(t, err, "invalid PromQL:\n%s", expr)

	var bad []string
	var walk func(node parser.Node, collapsed bool)
	walk = func(node parser.Node, collapsed bool) {
		switch n := node.(type) {
		case *parser.AggregateExpr:
			switch {
			case n.Op != parser.MAX && n.Op != parser.MIN:
				collapsed = false
			case promCollapsesExporters(n):
				collapsed = true
			}
		case *parser.VectorSelector:
			if promMultiExporterSpecMetrics[n.Name] && !collapsed {
				bad = append(bad, n.String())
			}
		}
		for _, child := range parser.Children(node) {
			walk(child, collapsed)
		}
	}
	walk(root, false)
	return bad
}

// Every key either builder answers, read from the builder's own source so a new key
// is covered the moment its case is written.
func TestPrometheusSpecKeysCountEachContainerOnce(t *testing.T) {
	builders := map[string]struct {
		build func(RequestMetadata, []string) map[string]string
		meta  RequestMetadata
	}{
		"buildPrometheusWorkloadQueries": {
			build: buildPrometheusWorkloadQueries,
			meta: RequestMetadata{
				Kind: "deployment", Namespace: "shop", Name: "api", ContainerName: "api",
				PVCName: "data", NodeName: "node-a", NodeIP: "10.0.0.5", InternalIP: "10.0.0.5",
			},
		},
		"buildPrometheusNodeQueries": {
			build: buildPrometheusNodeQueries,
			meta: RequestMetadata{
				Kind: "node", NodeName: "node-a", NodeIP: "10.0.0.5", InternalIP: "10.0.0.5",
			},
		},
	}

	for name, b := range builders {
		t.Run(name, func(t *testing.T) {
			keys := promBuilderKeys(t, name)
			meta := b.meta
			meta.RangeWindow, meta.Step, meta.RateWindow = "14d", "75s", "75s"
			queries := b.build(meta, keys)
			for _, key := range keys {
				expr, built := queries[key]
				if !built {
					continue
				}
				assert.Emptyf(t, promUncollapsedSpecSelectors(t, expr),
					"%s adds up request/limit/allocatable series without collapsing exporter copies:\n%s", key, expr)
			}
		})
	}
}

func TestOvercommitQueriesCountEachNodeAndContainerOnce(t *testing.T) {
	for _, a := range []*overcommitAction{{resource: "cpu"}, {resource: "memory"}} {
		for key, expr := range a.queries() {
			assert.Emptyf(t, promUncollapsedSpecSelectors(t, expr),
				"%s/%s adds up series without collapsing exporter copies:\n%s", a.resource, key, expr)
		}
	}
}

// The check itself, against the query shapes that caused the bug and the ones that
// are allowed.
func TestPromUncollapsedSpecSelectors(t *testing.T) {
	for _, tc := range []struct {
		expr string
		bad  bool
	}{
		{`sum(kube_pod_container_resource_requests{resource="cpu"})`, true},
		{`count(kube_node_status_allocatable{resource="cpu"})`, true},
		{`kube_pod_container_resource_requests{resource="cpu"}`, true},
		{`sum(max by (namespace, pod, job) (kube_pod_container_resource_requests{resource="cpu"}))`, true},
		{`max(sum by (pod) (kube_pod_container_resource_requests{resource="cpu"}))`, true},
		{`sum(max by (namespace, pod, container, service) (kube_pod_container_resource_requests{resource="cpu"}))`, true},
		{`sum(max without (job, instance) (kube_pod_container_resource_requests{resource="cpu"}))`, true},
		{`sum(max by (namespace, pod, container) (kube_pod_container_resource_requests{resource="cpu"}))`, false},
		{`count(max by (node) (kube_node_status_allocatable{resource="cpu"}))`, false},
		{`max by (namespace, container, pod) (max by (namespace, container, pod, job) (kube_pod_container_resource_limits{}) * on (namespace, container, pod, job) group_left(reason) max by (namespace, container, pod, job, reason) (kube_pod_container_status_last_terminated_reason{}))`, false},
		{`sum(container_memory_working_set_bytes{pod="a"})`, false},
	} {
		got := promUncollapsedSpecSelectors(t, tc.expr)
		if tc.bad {
			assert.NotEmptyf(t, got, "should flag: %s", tc.expr)
		} else {
			assert.Emptyf(t, got, "should allow: %s", tc.expr)
		}
	}
}
