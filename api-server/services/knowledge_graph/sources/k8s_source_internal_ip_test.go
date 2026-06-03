package sources

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestCreateNodeFromK8sNode_InternalIP_FromAnnotation pins the preferred
// extraction path: the AWS/EKS `alpha.kubernetes.io/provided-node-ip`
// annotation is the most reliable signal because it's explicitly the
// kubelet's chosen InternalIP. See #30683.
func TestCreateNodeFromK8sNode_InternalIP_FromAnnotation(t *testing.T) {
	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}

	meta := map[string]interface{}{
		"node_info": map[string]interface{}{
			"annotations": map[string]interface{}{
				"alpha.kubernetes.io/provided-node-ip": "172.31.81.38",
			},
			"addresses": []interface{}{
				"172.31.81.38",
				"44.211.89.103",
				"ip-172-31-81-38.ec2.internal",
			},
		},
	}
	metaBytes, _ := json.Marshal(meta)
	row := &K8sNodeRow{Name: "ip-172-31-81-38.ec2.internal", IsActive: true, ClusterName: "k8s-prod", Meta: metaBytes}

	node := src.createNodeFromK8sNode(row, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	if got := node.Properties["internal_ip"]; got != "172.31.81.38" {
		t.Errorf("internal_ip = %v, want 172.31.81.38", got)
	}
}

// TestCreateNodeFromK8sNode_InternalIP_FallbackRFC1918 pins the fallback:
// when no kubelet annotation is present, pick the first RFC1918 IPv4 from
// the flat addresses list (the collector strips the type discriminator).
func TestCreateNodeFromK8sNode_InternalIP_FallbackRFC1918(t *testing.T) {
	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}

	meta := map[string]interface{}{
		"node_info": map[string]interface{}{
			"addresses": []interface{}{
				"44.211.89.103", // public, must skip
				"172.31.8.2",    // first RFC1918, must pick
				"ip-172-31-8-2.ec2.internal",
				"10.0.0.5", // also RFC1918 but later
			},
		},
	}
	metaBytes, _ := json.Marshal(meta)
	row := &K8sNodeRow{Name: "n", IsActive: true, ClusterName: "c", Meta: metaBytes}

	node := src.createNodeFromK8sNode(row, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	if got := node.Properties["internal_ip"]; got != "172.31.8.2" {
		t.Errorf("internal_ip = %v, want 172.31.8.2 (first RFC1918)", got)
	}
}

// TestCreateNodeFromK8sNode_NoInternalIP asserts that if neither the
// annotation nor a private address is present, internal_ip stays unset.
func TestCreateNodeFromK8sNode_NoInternalIP(t *testing.T) {
	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}

	meta := map[string]interface{}{
		"node_info": map[string]interface{}{
			"addresses": []interface{}{
				"44.211.89.103", // public only
				"ec2-44-211-89-103.compute-1.amazonaws.com",
			},
		},
	}
	metaBytes, _ := json.Marshal(meta)
	row := &K8sNodeRow{Name: "n", IsActive: true, ClusterName: "c", Meta: metaBytes}

	node := src.createNodeFromK8sNode(row, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	if _, ok := node.Properties["internal_ip"]; ok {
		t.Errorf("internal_ip should be absent, got %v", node.Properties["internal_ip"])
	}
}

// TestCreateNodeFromK8sNode_NoMeta — empty-meta case (collector hasn't
// populated yet) must not crash.
func TestCreateNodeFromK8sNode_NoMeta(t *testing.T) {
	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}

	row := &K8sNodeRow{Name: "n", IsActive: true, ClusterName: "c", Meta: []byte("{}")}
	node := src.createNodeFromK8sNode(row, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	if _, ok := node.Properties["internal_ip"]; ok {
		t.Errorf("internal_ip should be absent for empty meta")
	}
}

// TestIsRFC1918IPv4 covers the helper directly to guard against future
// edits affecting which IPs we accept as "private".
func TestIsRFC1918IPv4(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"172.15.0.1", false}, // outside 172.16-31
		{"172.32.0.1", false}, // outside 172.16-31
		{"44.211.89.103", false},
		{"::1", false},
		{"not-an-ip", false},
		{"", false},
	} {
		if got := isRFC1918IPv4(tc.ip); got != tc.want {
			t.Errorf("isRFC1918IPv4(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
