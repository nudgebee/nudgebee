package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Expressions below are the real kubernetes-mixin rules as they are stored in
// event_rules on dev, copied verbatim. A hand-simplified expression would not
// exercise the shapes this classifier has to survive: `without` aggregations
// that drop every node dimension (NodeCPUHighUsage), binary comparisons with no
// `on (…)` clause (NodeSystemSaturation), and `group_left` joins that carry a
// node label across (KubeletTooManyPods).
func TestClassifyPromExprNodeScoped(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		wantIsNode bool
		wantParsed bool
	}{
		{
			name:       "NodeSystemSaturation - node-exporter job, no sub-node dimension",
			expr:       "node_load1{job=\"node-exporter\"}\n/ count without (cpu, mode) (node_cpu_seconds_total{job=\"node-exporter\", mode=\"idle\"}) > 2",
			wantIsNode: true,
			wantParsed: true,
		},
		{
			name:       "NodeCPUHighUsage - names no node dimension at all, job carries the scope",
			expr:       "sum without(mode) (avg without (cpu) (rate(node_cpu_seconds_total{job=\"node-exporter\", mode!~\"idle|iowait\"}[2m]))) * 100 > 90",
			wantIsNode: true,
			wantParsed: true,
		},
		{
			name:       "NodeMemoryMajorPagesFaults - bare node-exporter rate",
			expr:       "rate(node_vmstat_pgmajfault{job=\"node-exporter\"}[5m]) > 500",
			wantIsNode: true,
			wantParsed: true,
		},
		{
			name:       "NodeFilesystemAlmostOutOfSpace - device matchers are not sub-node dimensions",
			expr:       "(\n  node_filesystem_avail_bytes{job=\"node-exporter\",fstype!=\"\",mountpoint!=\"\"} / node_filesystem_size_bytes{job=\"node-exporter\",fstype!=\"\",mountpoint!=\"\"} * 100 < 3\nand\n  node_filesystem_readonly{job=\"node-exporter\",fstype!=\"\",mountpoint!=\"\"} == 0\n)",
			wantIsNode: true,
			wantParsed: true,
		},
		{
			name:       "KubePodCrashLooping - namespace matcher makes it sub-node",
			expr:       "max_over_time(kube_pod_container_status_waiting_reason{reason=\"CrashLoopBackOff\", job=\"kube-state-metrics\", namespace=~\".*\"}[5m]) >= 1",
			wantIsNode: false,
			wantParsed: true,
		},
		{
			name:       "kube-state-metrics pod rule grouped by pod is not node-scoped",
			expr:       "sum by (namespace, pod) (kube_pod_container_status_restarts_total{job=\"kube-state-metrics\"}) > 5",
			wantIsNode: false,
			wantParsed: true,
		},
		{
			name:       "explicit node grouping on a non-exporter job still signals node",
			expr:       "count by (node) (kube_node_status_condition{status=\"true\",condition=\"Ready\"}) > 0",
			wantIsNode: true,
			wantParsed: true,
		},
		{
			name:       "negated exporter job is not a node signal",
			expr:       "rate(some_metric{job!=\"node-exporter\"}[5m]) > 1",
			wantIsNode: false,
			wantParsed: true,
		},
		{
			name:       "unparseable expression reports parsed=false",
			expr:       "this is not promql {{{",
			wantIsNode: false,
			wantParsed: false,
		},
		{
			name:       "empty expression reports parsed=false",
			expr:       "",
			wantIsNode: false,
			wantParsed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isNode, parsed := classifyPromExprNodeScoped(tt.expr)
			assert.Equal(t, tt.wantParsed, parsed, "parsed")
			assert.Equal(t, tt.wantIsNode, isNode, "isNode")
		})
	}
}

// TestClassifyPromExprNodeScopedClusterRuleLimitation pins a known false
// positive so nobody reuses this classifier as a general scope test. The
// KubeCPUOvercommit rule is cluster-scoped, but it counts control-plane nodes
// with `max by (cluster, node)`, so a node dimension is present and this
// function reports true.
//
// Harmless where it is used: ResolveNodeSubject only consults the classifier
// after the subject has already resolved to a monitoring exporter, and a
// cluster-scoped rule never produces one — KubeCPUOvercommit's subject is the
// alert name itself.
func TestClassifyPromExprNodeScopedClusterRuleLimitation(t *testing.T) {
	expr := "(\n  (\n    sum by (cluster) (namespace_cpu:kube_pod_container_resource_requests:sum{})\n    -\n    sum by (cluster) (kube_node_status_allocatable{job=\"kube-state-metrics\",resource=\"cpu\"}) > 0\n  )\n  and\n  count by (cluster) (max by (cluster, node) (kube_node_role{job=\"kube-state-metrics\", role=\"control-plane\"})) < 3\n)"
	isNode, parsed := classifyPromExprNodeScoped(expr)
	assert.True(t, parsed)
	assert.True(t, isNode, "documented false positive — see the comment above before changing this")
}

func TestIsExporterWorkloadName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "bare node-exporter pod", in: "node-exporter-skqnn", want: true},
		{name: "helm-prefixed node-exporter pod", in: "victoria-prometheus-node-exporter-skqnn", want: true},
		{name: "double-prefixed node-exporter pod", in: "nudgebee-prometheus-prometheus-node-exporter-dtkzl", want: true},
		{name: "node-exporter daemonset", in: "victoria-prometheus-node-exporter", want: true},
		{name: "kube-state-metrics", in: "victoria-kube-state-metrics", want: true},
		{name: "case insensitive", in: "Victoria-Prometheus-Node-Exporter", want: true},
		{name: "real workload", in: "checkout-api", want: false},
		{name: "workload that merely mentions node", in: "node-red", want: false},
		{name: "empty", in: "", want: false},
		{name: "whitespace", in: "   ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsExporterWorkloadName(tt.in))
		})
	}
}

// TestNodeFromLabels asserts the `instance` label never leaks through as a node
// name. It is a scrape target (`10.0.0.5:9100`); nodeFromInstanceIP translates
// it against the node inventory instead.
func TestNodeFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{name: "node label wins", labels: map[string]string{"node": "gke-pool-abc", "instance": "10.0.0.5:9100"}, want: "gke-pool-abc"},
		{name: "instance alone is not a node name", labels: map[string]string{"instance": "10.0.0.5:9100"}, want: ""},
		{name: "blank node label falls through", labels: map[string]string{"node": "  "}, want: ""},
		{name: "nil labels", labels: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nodeFromLabels(tt.labels))
		})
	}
}

// TestResolveNodeSubjectGuards pins the order of the early returns: every case
// below must be decided from the input alone, before the alert's rule text is
// fetched. A nil RequestContext is the assertion — the first thing
// ResolveNodeSubject does with sc is read the rule expression from the
// database, so any of these reaching that call would panic here instead of
// silently issuing a query per event.
func TestResolveNodeSubjectGuards(t *testing.T) {
	tests := []struct {
		name      string
		accountId string
		in        NodeSubjectInput
	}{
		{
			name:      "no account",
			accountId: "",
			in:        NodeSubjectInput{AlertName: "NodeSystemSaturation", SubjectName: "victoria-prometheus-node-exporter-skqnn", SubjectType: "pod"},
		},
		{
			name:      "no alert name",
			accountId: "acct",
			in:        NodeSubjectInput{SubjectName: "victoria-prometheus-node-exporter-skqnn", SubjectType: "pod"},
		},
		{
			name:      "subject is already a node",
			accountId: "acct",
			in:        NodeSubjectInput{AlertName: "KubeletTooManyPods", SubjectName: "gke-pool-abc", SubjectType: "node"},
		},
		{
			name:      "subject is a real workload, not an exporter",
			accountId: "acct",
			in:        NodeSubjectInput{AlertName: "NodeSystemSaturation", SubjectName: "checkout-api-7d9f", SubjectType: "pod"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				assert.Equal(t, "", ResolveNodeSubject(nil, tt.accountId, tt.in))
			})
		})
	}
}

// TestIsNodeScopedExporterJob — the `job` label is the primary gate because it
// is on every node alert and costs no query. The kube-state-metrics case is the
// one that must stay false: a KubePodCrashLooping about a node-exporter POD is
// scraped by kube-state-metrics, and its subject is correctly the pod.
func TestIsNodeScopedExporterJob(t *testing.T) {
	tests := []struct {
		name string
		job  string
		want bool
	}{
		{name: "canonical node-exporter job", job: "node-exporter", want: true},
		{name: "underscore spelling", job: "node_exporter", want: true},
		{name: "helm-prefixed job", job: "victoria-prometheus-node-exporter", want: true},
		{name: "case insensitive", job: "Node-Exporter", want: true},
		{name: "kube-state-metrics is not per-node", job: "kube-state-metrics", want: false},
		{name: "kubelet is not per-node for our purposes", job: "kubelet", want: false},
		{name: "application job", job: "checkout-api", want: false},
		{name: "empty", job: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNodeScopedExporterJob(tt.job))
		})
	}
}

// TestIsNodeScopedAlertUsesJobWithoutQuerying asserts the job label alone
// decides, before the rule text is fetched. The nil context is the assertion:
// FetchEventRuleExpr would panic on it, so a node-exporter job reaching the
// database would fail here rather than adding a query per event — and, more
// importantly, would make the fix depend on rule text that is empty for some
// tenants.
func TestIsNodeScopedAlertUsesJobWithoutQuerying(t *testing.T) {
	in := NodeSubjectInput{
		AlertName:   "NodeSystemSaturation",
		SubjectName: "victoria-prometheus-node-exporter-skqnn",
		SubjectType: "pod",
		Labels:      map[string]string{"job": "node-exporter", "instance": "10.128.0.14:9100"},
	}
	assert.NotPanics(t, func() {
		assert.True(t, isNodeScopedAlert(nil, "acct", in))
	})
}
