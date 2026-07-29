package traces

import "testing"

// applicationKindFor is the fix for a K8s Node (host-network / kubelet-level
// traffic, e.g. a Prometheus node-metrics scrape) being misclassified as a
// generic "Service" application — see the doc comment on the sentinel
// constant in service_map.go.
func TestApplicationKindFor(t *testing.T) {
	tests := []struct {
		namespace string
		want      string
	}{
		{namespace: "node", want: "Node"},
		{namespace: "default", want: "Service"},
		{namespace: "", want: "Service"},
		{namespace: "kube-system", want: "Service"},
	}
	for _, tt := range tests {
		if got := applicationKindFor(tt.namespace); got != tt.want {
			t.Errorf("applicationKindFor(%q) = %q, want %q", tt.namespace, got, tt.want)
		}
	}
}
