package k8s

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestCreateContainerImageNodesAndEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &K8sSource{}

	// Two workloads share nginx:1.21 (dedup to one node); one also runs my-app:v2.
	wl1 := &core.DbNode{ID: "wl1", NodeType: core.NodeTypeWorkload, Properties: map[string]interface{}{
		"name": "web", "container_images": []string{"nginx:1.21", "sidecar/logger:latest"},
	}}
	wl2 := &core.DbNode{ID: "wl2", NodeType: core.NodeTypeWorkload, Properties: map[string]interface{}{
		"name": "api", "container_images": []string{"nginx:1.21", "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-app:v2"},
	}}

	nodes, edges := s.createContainerImageNodesAndEdges([]*core.DbNode{wl1, wl2}, req)

	// Distinct images: nginx:1.21, sidecar/logger:latest, my-app:v2 = 3 nodes.
	if len(nodes) != 3 {
		t.Fatalf("expected 3 deduped ContainerImage nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.NodeType != core.NodeTypeContainerImage || n.SpecificType != "ContainerImage" {
			t.Errorf("node %s: type=%s specific=%s, want ContainerImage", n.Properties["name"], n.NodeType, n.SpecificType)
		}
	}

	// Edges: wl1→nginx, wl1→logger, wl2→nginx, wl2→my-app = 4 USES_IMAGE edges.
	if len(edges) != 4 {
		t.Fatalf("expected 4 USES_IMAGE edges, got %d", len(edges))
	}
	for _, e := range edges {
		if e.RelationshipType != core.RelationshipUsesImage {
			t.Errorf("edge rel = %s, want USES_IMAGE", e.RelationshipType)
		}
	}

	// The shared nginx node must be the SAME node ID for both workloads (dedup).
	var nginxID string
	for _, n := range nodes {
		if n.Properties["name"] == "nginx" {
			nginxID = n.ID
		}
	}
	var wl1Nginx, wl2Nginx bool
	for _, e := range edges {
		if e.DestinationNodeID == nginxID && e.SourceNodeID == "wl1" {
			wl1Nginx = true
		}
		if e.DestinationNodeID == nginxID && e.SourceNodeID == "wl2" {
			wl2Nginx = true
		}
	}
	if !wl1Nginx || !wl2Nginx {
		t.Error("both workloads should USES_IMAGE the single deduped nginx node")
	}
}
