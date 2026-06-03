package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

// makeK8sNode builds a Node-typed graph node as K8sSource emits them
// (source="k8s", with cluster + internal_ip set). Shared by tests in this
// file and tests in pod_ip_resolver_test.go that exercise the chain helper.
func makeK8sNode(name, cluster, internalIP string) *core.DbNode {
	return &core.DbNode{
		NodeType: core.NodeTypeNode,
		Source:   "k8s",
		Properties: map[string]interface{}{
			"name":        name,
			"cluster":     cluster,
			"internal_ip": internalIP,
		},
	}
}

func TestK8sNodeIPResolver_SameClusterHit(t *testing.T) {
	r := NewK8sNodeIPResolver([]*core.DbNode{
		makeK8sNode("ip-172-31-8-2.ec2.internal", "k8s-prod", "172.31.8.2"),
		makeK8sNode("ip-172-31-32-204.ec2.internal", "k8s-prod", "172.31.32.204"),
	})

	got, ok := r.Resolve("k8s-prod", "172.31.8.2")
	if !ok {
		t.Fatalf("expected same-cluster hit")
	}
	if got.Properties["name"] != "ip-172-31-8-2.ec2.internal" {
		t.Errorf("expected ip-172-31-8-2 node, got %v", got.Properties["name"])
	}
}

func TestK8sNodeIPResolver_GlobalUniqueFallback(t *testing.T) {
	// Caller cluster unknown (e.g., bypass branch in eBPF source). One Node
	// globally with this IP — safe to resolve.
	r := NewK8sNodeIPResolver([]*core.DbNode{
		makeK8sNode("ip-172-31-8-2.ec2.internal", "k8s-prod", "172.31.8.2"),
	})

	got, reason, ok := ResolveIPToK8sNode("172.31.8.2", "", r)
	if !ok {
		t.Fatalf("expected global-unique hit")
	}
	if reason != IPResolutionReasonGlobalUnique {
		t.Errorf("expected reason=global_unique, got %q", reason)
	}
	if got.Properties["name"] != "ip-172-31-8-2.ec2.internal" {
		t.Errorf("expected node match, got %v", got.Properties["name"])
	}
}

func TestK8sNodeIPResolver_CrossClusterAmbiguityRefused(t *testing.T) {
	// Same internal IP in two clusters (e.g., overlapping VPC CIDRs in a
	// multi-cluster tenant). Without caller-cluster context, refuse to guess.
	r := NewK8sNodeIPResolver([]*core.DbNode{
		makeK8sNode("ip-a", "cluster-a", "172.31.8.2"),
		makeK8sNode("ip-b", "cluster-b", "172.31.8.2"),
	})

	if _, ok := r.Resolve("", "172.31.8.2"); ok {
		t.Errorf("ambiguous IP without caller cluster should refuse to resolve")
	}
	// But scoped lookups still work.
	if got, ok := r.Resolve("cluster-a", "172.31.8.2"); !ok || got.Properties["name"] != "ip-a" {
		t.Errorf("caller in cluster-a should resolve to ip-a, got %v ok=%v", got, ok)
	}
}

func TestK8sNodeIPResolver_NilSafe(t *testing.T) {
	var r *K8sNodeIPResolver
	if _, ok := r.Resolve("any", "172.31.8.2"); ok {
		t.Errorf("nil resolver should return ok=false")
	}
	if _, _, ok := ResolveIPToK8sNode("172.31.8.2", "any", r); ok {
		t.Errorf("ResolveIPToK8sNode with nil resolver should return ok=false")
	}
}

func TestResolveIPToK8sNode_StripsPort(t *testing.T) {
	r := NewK8sNodeIPResolver([]*core.DbNode{
		makeK8sNode("ip-172-31-8-2.ec2.internal", "k8s-prod", "172.31.8.2"),
	})

	got, _, ok := ResolveIPToK8sNode("172.31.8.2:10250", "k8s-prod", r)
	if !ok || got.Properties["name"] != "ip-172-31-8-2.ec2.internal" {
		t.Errorf("port-suffixed IP should resolve, got %v ok=%v", got, ok)
	}
}

func TestResolveIPToK8sNode_RejectsSpecialIPs(t *testing.T) {
	r := NewK8sNodeIPResolver([]*core.DbNode{
		// Pollute the resolver with special IPs that WOULD match if the
		// skip list weren't enforced. Verifies the entry point's filter
		// runs before delegating to Resolve.
		makeK8sNode("ip-loopback", "k8s-prod", "127.0.0.1"),
		makeK8sNode("ip-metadata", "k8s-prod", "169.254.169.254"),
	})

	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "::1", "0.0.0.0"} {
		if _, _, ok := ResolveIPToK8sNode(ip, "k8s-prod", r); ok {
			t.Errorf("special IP %q must not resolve", ip)
		}
	}
}

func TestNewK8sNodeIPResolver_SkipsNonK8sSourceNodes(t *testing.T) {
	// ebpf flow source emits "Node"-typed entries that aren't real K8s Nodes
	// (e.g., host-network pods labelled `kind: node`). Only k8s-source Nodes
	// should be indexed.
	r := NewK8sNodeIPResolver([]*core.DbNode{
		{
			NodeType: core.NodeTypeNode,
			Source:   "ebpf", // not k8s
			Properties: map[string]interface{}{
				"name":        "aws-node-5655f",
				"cluster":     "k8s-prod",
				"internal_ip": "172.31.81.38",
			},
		},
		makeK8sNode("ip-172-31-81-38.ec2.internal", "k8s-prod", "172.31.81.38"),
	})

	got, ok := r.Resolve("k8s-prod", "172.31.81.38")
	if !ok || got.Properties["name"] != "ip-172-31-81-38.ec2.internal" {
		t.Errorf("expected k8s-source Node to win, got %v ok=%v", got, ok)
	}
}

func TestNewK8sNodeIPResolver_SkipsNodesWithoutInternalIP(t *testing.T) {
	// Defensive: a Node without internal_ip (corrupt data) shouldn't blow
	// up — silently skipped.
	r := NewK8sNodeIPResolver([]*core.DbNode{
		{
			NodeType: core.NodeTypeNode,
			Source:   "k8s",
			Properties: map[string]interface{}{
				"name":    "missing-ip-node",
				"cluster": "k8s-prod",
				// no internal_ip
			},
		},
	})

	if _, ok := r.Resolve("k8s-prod", "172.31.8.2"); ok {
		t.Errorf("resolver should be empty when no Node has internal_ip")
	}
}
