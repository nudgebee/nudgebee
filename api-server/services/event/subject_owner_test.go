package event

import "testing"

func TestOwnerFromLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		wantName string
		wantKind string
	}{
		{
			name:     "k8s_deployment_name (prometheus snake_case)",
			labels:   map[string]string{"k8s_deployment_name": "checkout"},
			wantName: "checkout",
			wantKind: "Deployment",
		},
		{
			name:     "k8s.deployment.name (OTel dotted)",
			labels:   map[string]string{"k8s.deployment.name": "payment"},
			wantName: "payment",
			wantKind: "Deployment",
		},
		{
			name:     "deployment (generic)",
			labels:   map[string]string{"deployment": "frontend"},
			wantName: "frontend",
			wantKind: "Deployment",
		},
		{
			name:     "kube_deployment (Datadog tag)",
			labels:   map[string]string{"kube_deployment": "ad"},
			wantName: "ad",
			wantKind: "Deployment",
		},
		{
			name:     "k8s_statefulset_name",
			labels:   map[string]string{"k8s_statefulset_name": "clickhouse"},
			wantName: "clickhouse",
			wantKind: "StatefulSet",
		},
		{
			name:     "k8s_daemonset_name",
			labels:   map[string]string{"k8s_daemonset_name": "node-agent"},
			wantName: "node-agent",
			wantKind: "DaemonSet",
		},
		{
			name: "Deployment precedence over StatefulSet when both present",
			labels: map[string]string{
				"k8s_deployment_name":  "checkout",
				"k8s_statefulset_name": "clickhouse",
			},
			wantName: "checkout",
			wantKind: "Deployment",
		},
		{
			name:     "empty labels returns empty",
			labels:   map[string]string{},
			wantName: "",
			wantKind: "",
		},
		{
			name:     "nil labels returns empty",
			labels:   nil,
			wantName: "",
			wantKind: "",
		},
		{
			name:     "unrelated labels returns empty",
			labels:   map[string]string{"alertname": "Foo", "severity": "critical"},
			wantName: "",
			wantKind: "",
		},
		{
			name:     "empty value treated as missing",
			labels:   map[string]string{"k8s_deployment_name": "", "deployment": "fallback"},
			wantName: "fallback",
			wantKind: "Deployment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotKind := ownerFromLabels(tc.labels)
			if gotName != tc.wantName || gotKind != tc.wantKind {
				t.Errorf("ownerFromLabels(%v) = (%q, %q); want (%q, %q)",
					tc.labels, gotName, gotKind, tc.wantName, tc.wantKind)
			}
		})
	}
}

func TestIsNonWorkloadDatastoreSubject(t *testing.T) {
	tests := []struct {
		subjectType string
		want        bool
	}{
		{"database", true},
		{"cloudsql_database", true},
		{"Database", true},          // case-insensitive
		{"CloudSQL_Database", true}, // case-insensitive
		{"", false},
		{"pod", false},
		{"deployment", false},
		{"statefulset", false}, // a postgres StatefulSet workload must still resolve
		{"instance", false},    // generic host fallback must still resolve
	}
	for _, tc := range tests {
		t.Run(tc.subjectType, func(t *testing.T) {
			if got := isNonWorkloadDatastoreSubject(tc.subjectType); got != tc.want {
				t.Errorf("isNonWorkloadDatastoreSubject(%q) = %v; want %v", tc.subjectType, got, tc.want)
			}
		})
	}
}
