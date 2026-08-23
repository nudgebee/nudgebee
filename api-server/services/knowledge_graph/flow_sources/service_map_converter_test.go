package flow_sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
)

// convertK8sMetadataToGraph builds Cluster/Namespace/Node infra nodes directly
// from trace-derived K8s metadata, bypassing core.NewNode's default-to-NodeType
// SpecificType fallback (see nb-34880) — same bug class as ConvertServiceMapToGraph,
// caught by review on the fix for that function.
func TestConvertK8sMetadataToGraph_PopulatesSpecificType(t *testing.T) {
	metadata := &traces.K8sInfrastructureMetadata{
		Clusters: map[string]*traces.K8sClusterInfo{
			"c1": {Name: "cluster-a", Environment: "prod"},
		},
		Namespaces: map[string]*traces.K8sNamespaceInfo{
			"n1": {Name: "default", Cluster: "cluster-a", Environment: "prod"},
		},
		Nodes: map[string]*traces.K8sNodeInfo{
			"w1": {Name: "worker-1", Cluster: "cluster-a", Environment: "prod"},
		},
	}

	nodes, _ := convertK8sMetadataToGraph(metadata, "acct-1", "tenant-1", "traces")

	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes (cluster, namespace, node), got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.SpecificType == "" {
			t.Errorf("node %q (NodeType=%s) has blank SpecificType", n.ID, n.NodeType)
		}
		if n.SpecificType != string(n.NodeType) {
			t.Errorf("node %q SpecificType = %q, want %q", n.ID, n.SpecificType, n.NodeType)
		}
	}

	wantTypes := map[core.NodeType]bool{
		core.NodeTypeCluster:   false,
		core.NodeTypeNamespace: false,
		core.NodeTypeNode:      false,
	}
	for _, n := range nodes {
		if _, ok := wantTypes[n.NodeType]; ok {
			wantTypes[n.NodeType] = true
		}
	}
	for nt, seen := range wantTypes {
		if !seen {
			t.Errorf("expected a node of type %s", nt)
		}
	}
}
