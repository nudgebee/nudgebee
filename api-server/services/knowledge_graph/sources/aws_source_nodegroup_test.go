package sources

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestDetermineNodeType_EKSNodeGroup pins (nodegroup, AmazonEKS) →
// ComputeInstancePool. Without this mapping, EKS NodeGroups land as
// generic CloudResource and the BELONGS_TO/HOSTED_ON edges never fire.
// See #30680.
func TestDetermineNodeType_EKSNodeGroup(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	got := src.determineNodeType("nodegroup", "AmazonEKS")
	if got != core.NodeTypeComputeInstancePool {
		t.Errorf("determineNodeType(nodegroup, AmazonEKS) = %v, want ComputeInstancePool", got)
	}
}

// TestCreateEKSNodeGroupEdges pins the post-fix edge shape for a NodeGroup:
// BELONGS_TO the parent EKS cluster and HOSTED_ON each Subnet the group spans.
// Non-EKS ComputeInstancePool rows (eg from GCP) must be skipped.
func TestCreateEKSNodeGroupEdges(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	// Seed cache with a NodeGroup row whose meta carries ClusterName + Subnets.
	ngMeta := map[string]interface{}{
		"ClusterName": "nudgebee",
		"Subnets":     []interface{}{"subnet-a", "subnet-b", "subnet-c"},
	}
	metaBytes, _ := json.Marshal(ngMeta)
	src.metaByType = map[string][]CloudResourceRow{}
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{
		"nodegroup": {"db-node-group-ri-m5-xlarge-v2": {ResourceID: "db-node-group-ri-m5-xlarge-v2", Type: "nodegroup", Meta: metaBytes}},
	}

	ng := &core.DbNode{
		NodeType: core.NodeTypeComputeInstancePool,
		Properties: map[string]interface{}{
			"resource_id":  "db-node-group-ri-m5-xlarge-v2",
			"service_name": "AmazonEKS",
		},
	}
	cluster := &core.DbNode{
		NodeType:   core.NodeTypeManagedCluster,
		Properties: map[string]interface{}{"resource_id": "nudgebee", "service_name": "AmazonEKS"},
	}
	subnetA := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-a"}}
	subnetB := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-b"}}
	subnetC := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-c"}}
	lookup := newNodeLookup([]*core.DbNode{ng, cluster, subnetA, subnetB, subnetC})

	edges := src.createEKSNodeGroupEdges([]*core.DbNode{ng}, lookup, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	// Expect: 1 BELONGS_TO + 3 HOSTED_ON = 4 edges.
	if len(edges) != 4 {
		t.Fatalf("expected 4 edges, got %d", len(edges))
	}

	counts := map[string]int{}
	for _, e := range edges {
		counts[string(e.RelationshipType)]++
	}
	if counts["BELONGS_TO"] != 1 {
		t.Errorf("BELONGS_TO edges = %d, want 1", counts["BELONGS_TO"])
	}
	if counts["HOSTED_ON"] != 3 {
		t.Errorf("HOSTED_ON edges = %d, want 3", counts["HOSTED_ON"])
	}
}

// TestCreateEKSNodeGroupEdges_SkipsNonEKS asserts that ComputeInstancePool
// nodes from non-EKS sources (eg GKE NodePool) are not touched by this
// AWS-specific builder.
func TestCreateEKSNodeGroupEdges_SkipsNonEKS(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{}

	gke := &core.DbNode{
		NodeType: core.NodeTypeComputeInstancePool,
		Properties: map[string]interface{}{
			"resource_id":  "some-gke-pool",
			"service_name": "google-compute-engine",
		},
	}
	lookup := newNodeLookup([]*core.DbNode{gke})
	edges := src.createEKSNodeGroupEdges([]*core.DbNode{gke}, lookup, &core.SourceBuildRequest{})
	if len(edges) != 0 {
		t.Errorf("non-EKS ComputeInstancePool should produce 0 AWS-side edges, got %d", len(edges))
	}
}

// TestCreateEKSNodeGroupEdges_NoMetaCache — regression guard for the empty
// cache case (eg first build before the collector has run).
func TestCreateEKSNodeGroupEdges_NoMetaCache(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{}

	ng := &core.DbNode{
		NodeType: core.NodeTypeComputeInstancePool,
		Properties: map[string]interface{}{
			"resource_id":  "ng",
			"service_name": "AmazonEKS",
		},
	}
	lookup := newNodeLookup([]*core.DbNode{ng})
	edges := src.createEKSNodeGroupEdges([]*core.DbNode{ng}, lookup, &core.SourceBuildRequest{})
	if len(edges) != 0 {
		t.Errorf("expected 0 edges when cache is empty, got %d", len(edges))
	}
}
