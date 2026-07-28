package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAlertNameFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{name: "nb_alert_name preferred", labels: map[string]string{"nb_alert_name": "KubeSchedulerDown", "alertname": "incident"}, want: "KubeSchedulerDown"},
		{name: "falls back to alertname", labels: map[string]string{"alertname": "KubeMemoryOvercommit"}, want: "KubeMemoryOvercommit"},
		{name: "empty nb_alert_name falls back", labels: map[string]string{"nb_alert_name": "", "alertname": "KubeCPUOvercommit"}, want: "KubeCPUOvercommit"},
		{name: "none present", labels: map[string]string{"severity": "warning"}, want: ""},
		{name: "nil labels", labels: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, alertNameFromLabels(tt.labels))
		})
	}
}

// TestClusterScopedAlertNamesFloor asserts the curated fallback set includes the
// well-known subjectless cluster/control-plane alerts and excludes alerts that
// carry a resolvable subject.
func TestClusterScopedAlertNamesFloor(t *testing.T) {
	for _, a := range []string{
		"KubeMemoryOvercommit", "KubeCPUOvercommit", "KubeSchedulerDown",
		"KubeControllerManagerDown", "KubeAPIDown", "KubeVersionMismatch",
		"KubeletDown", "Watchdog",
	} {
		assert.True(t, clusterScopedAlertNames[a], "expected %s in cluster floor", a)
	}
	for _, a := range []string{
		"KubePodCrashLooping", "KubeNodeNotReady", "TargetDown",
		"KubeAggregatedAPIDown", "AlertmanagerClusterDown",
		"KubePersistentVolumeFillingUp", "HighErrorCriticalLogs",
	} {
		assert.False(t, clusterScopedAlertNames[a], "did not expect %s in cluster floor", a)
	}
}

func TestClassifyPromExprClusterScoped(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		wantCyes   bool // isCluster
		wantParsed bool
	}{
		{
			name:       "KubeMemoryOvercommit (by cluster)",
			expr:       `sum(namespace_memory:kube_pod_container_resource_requests:sum{job="kube-state-metrics"}) by (cluster) - (sum(kube_node_status_allocatable{resource="memory",job="kube-state-metrics"}) by (cluster) - max(kube_node_status_allocatable{resource="memory",job="kube-state-metrics"}) by (cluster)) > 0`,
			wantCyes:   true,
			wantParsed: true,
		},
		{
			// Recording-rule metric name contains "namespace" as __name__, not a label.
			name:       "KubeCPUOvercommit (recording-rule name trap)",
			expr:       `sum(namespace_cpu:kube_pod_container_resource_requests:sum{}) by (cluster) - (sum(kube_node_status_allocatable{resource="cpu"}) by (cluster) - max(kube_node_status_allocatable{resource="cpu"}) by (cluster)) > 0`,
			wantCyes:   true,
			wantParsed: true,
		},
		{
			name:       "KubeSchedulerDown (absent)",
			expr:       `absent(up{job="kube-scheduler"} == 1)`,
			wantCyes:   true,
			wantParsed: true,
		},
		{
			name:       "KubeNodeNotReady (on cluster,node)",
			expr:       `kube_node_status_condition{job="kube-state-metrics",condition="Ready",status="true"} == 0 and on (cluster, node) kube_node_spec_unschedulable{job="kube-state-metrics"} == 0`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			name:       "KubeletTooManyPods (by cluster,node)",
			expr:       `count by (cluster, node) (kube_pod_status_phase{job="kube-state-metrics",phase="Running"} == 1) > 100`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			name:       "CPUThrottlingHigh (by cluster,container,pod,namespace)",
			expr:       `sum(increase(container_cpu_cfs_throttled_periods_total{container!=""}[5m])) by (cluster, container, pod, namespace) / sum(increase(container_cpu_cfs_periods_total{}[5m])) by (cluster, container, pod, namespace) > 0.25`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			name:       "KubePersistentVolumeFillingUp (namespace matcher)",
			expr:       `kubelet_volume_stats_available_bytes{job="kubelet", namespace=~".*"} / kubelet_volume_stats_capacity_bytes{job="kubelet", namespace=~".*"} < 0.15`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			name:       "KubePodCrashLooping (namespace matcher)",
			expr:       `max_over_time(kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff", job="kube-state-metrics", namespace=~".*"}[5m]) >= 1`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			// Bare per-instance alert: carries a natural `instance` label not in any
			// matcher, and has no cluster aggregation signal — must NOT be skipped.
			name:       "bare per-instance up==0 (no cluster signal)",
			expr:       `up{job="my-app"} == 0`,
			wantCyes:   false,
			wantParsed: true,
		},
		{
			name:       "bare global sum (by empty) is a cluster signal",
			expr:       `sum(kube_pod_status_phase{job="kube-state-metrics",phase="Pending"}) by () > 500`,
			wantCyes:   true,
			wantParsed: true,
		},
		{
			name:       "not valid PromQL (e.g. datadog expr)",
			expr:       `avg(last_5m):avg:system.cpu.user{*} > 90`,
			wantCyes:   false,
			wantParsed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isCluster, parsed := classifyPromExprClusterScoped(tt.expr)
			assert.Equal(t, tt.wantParsed, parsed, "parsed")
			assert.Equal(t, tt.wantCyes, isCluster, "isCluster")
		})
	}
}
