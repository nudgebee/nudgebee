package event

import "testing"

// Alert-sourced events get subject_node from the alert's `instance` label,
// which for anything scraped off kube-state-metrics is the KSM pod's scrape
// address rather than a node. Measured on the test env: 104 of 124
// KubePodCrashLooping events in 36h stored "10.64.21.224:8080" here, and the
// noisy-neighbours card rendered for none of them — every node-scoped query
// built from that value matched nothing (issue #37669).
//
// A node name is a Kubernetes object name, so it is a DNS-1123 subdomain and
// cannot contain a colon. That makes the check exact rather than a heuristic,
// which is why a bare address is left alone: some clusters really do name nodes
// by IP, and there is no way to tell such a name from a port-less scrape target.
func TestNormalizeSubjectNode(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{"kube-state-metrics scrape address is dropped", "10.64.21.224:8080", ""},
		{"node-exporter scrape address is dropped", "10.64.12.84:9100", ""},
		{"host:port with a DNS host is dropped", "worker-01:10250", ""},
		{"GKE node name is unchanged", "gke-example-cluster-spot-pool-02132c6e-z2nc", "gke-example-cluster-spot-pool-02132c6e-z2nc"},
		{"EC2 private DNS node name is unchanged", "ip-10-0-1-23.ec2.internal", "ip-10-0-1-23.ec2.internal"},
		{"short node name is unchanged", "worker-01", "worker-01"},
		{"bare address is left alone — could be an IP-named node", "10.64.21.224", "10.64.21.224"},
		{"empty passes through", "", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSubjectNode(tc.input); got != tc.want {
				t.Errorf("normalizeSubjectNode(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
