package observability

import "testing"

func TestExtractWorkloadFromPodName(t *testing.T) {
	tests := []struct {
		podName  string
		expected string
	}{
		// Empty input
		{"", ""},
		// Single segment — fewer than 3 parts, return as-is
		{"nginx", "nginx"},
		// Two segments — fewer than 3 parts, return as-is
		{"nginx-abc12", "nginx-abc12"},
		// Deployment pattern: workload-replicaset_hash-pod_hash
		{"nginx-deployment-7b7d7f9f9d-abcde", "nginx-deployment"},
		// Multi-segment workload name
		{"my-app-frontend-a1b2c-d3e4f", "my-app-frontend"},
		// Suffixes that don't look like K8s hashes — return as-is
		{"my-app-UPPER-case1", "my-app-UPPER-case1"},
		// Three segments but last two are valid K8s suffixes
		{"redis-abc12-xyz98", "redis"},
		// Pod name with consecutive dashes — FieldsFunc skips empties
		{"app--name-abc12-xyz98", "app-name"},
	}

	for _, tt := range tests {
		t.Run(tt.podName, func(t *testing.T) {
			result := extractWorkloadFromPodName(tt.podName)
			if result != tt.expected {
				t.Errorf("extractWorkloadFromPodName(%q) = %q, expected %q",
					tt.podName, result, tt.expected)
			}
		})
	}
}
