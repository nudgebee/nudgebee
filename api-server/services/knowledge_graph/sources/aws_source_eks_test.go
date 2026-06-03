package sources

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestExtractClusterMetadata_RoleArn pins that the cluster's IAM role ARN
// hoists from meta["RoleArn"] into properties["role_arn"] — used by
// createEKSEdges to wire the RUNS_AS → ServiceIdentity edge. See #30679.
func TestExtractClusterMetadata_RoleArn(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	props := map[string]interface{}{}
	meta := map[string]interface{}{
		"RoleArn": "arn:aws:iam::123456789012:role/nudgebee-eks-role",
		"Status":  "ACTIVE",
		"Version": "1.33",
	}

	src.extractClusterMetadata(props, meta)

	if got := props["role_arn"]; got != "arn:aws:iam::123456789012:role/nudgebee-eks-role" {
		t.Errorf("role_arn = %v, want the cluster role ARN", got)
	}
}

// TestExtractClusterMetadata_NoRoleArn — defensive: missing RoleArn must not
// leave a "role_arn" key behind (otherwise downstream lookups misfire).
func TestExtractClusterMetadata_NoRoleArn(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	props := map[string]interface{}{}
	src.extractClusterMetadata(props, map[string]interface{}{"Status": "ACTIVE"})

	if _, ok := props["role_arn"]; ok {
		t.Errorf("role_arn should be absent when meta has no RoleArn")
	}
}

// TestCreateEKSEdges_FullTopology pins the post-fix edge shape for a healthy
// EKS cluster: HOSTED_ON to VPC + each Subnet + each SecurityGroup (incl.
// the cluster SG), and RUNS_AS to the IAM role's ServiceIdentity. Without
// this fix the function emitted 0 edges because getMetadataMap returned
// no meta — verified against the prod KG audit on 2026-05-19.
func TestCreateEKSEdges_FullTopology(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	// Seed the per-build meta cache with one EKS row.
	clusterMeta := map[string]interface{}{
		"RoleArn": "arn:aws:iam::123456789012:role/nudgebee-eks-role",
		"ResourcesVpcConfig": map[string]interface{}{
			"VpcId":                  "vpc-00459f012dc59d416",
			"EndpointPublicAccess":   true,
			"EndpointPrivateAccess":  true,
			"SubnetIds":              []interface{}{"subnet-1", "subnet-2", "subnet-3"},
			"SecurityGroupIds":       []interface{}{"sg-shared-1", "sg-shared-2"},
			"ClusterSecurityGroupId": "sg-cluster",
		},
	}
	metaBytes, _ := json.Marshal(clusterMeta)
	src.metaByType = map[string][]CloudResourceRow{}
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{
		"cluster": {"nudgebee": {ResourceID: "nudgebee", Type: "cluster", Meta: metaBytes}},
	}

	// EKS node referencing the cached meta by resource_id + carrying role_arn
	// the way extractClusterMetadata would have populated it.
	eks := &core.DbNode{
		NodeType: core.NodeTypeManagedCluster,
		Properties: map[string]interface{}{
			"resource_id": "nudgebee",
			"name":        "nudgebee",
			"role_arn":    "arn:aws:iam::123456789012:role/nudgebee-eks-role",
		},
	}
	vpc := &core.DbNode{NodeType: core.NodeTypeVPC, Properties: map[string]interface{}{"resource_id": "vpc-00459f012dc59d416"}}
	subnet1 := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-1"}}
	subnet2 := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-2"}}
	subnet3 := &core.DbNode{NodeType: core.NodeTypeSubnet, Properties: map[string]interface{}{"resource_id": "subnet-3"}}
	sg1 := &core.DbNode{NodeType: core.NodeTypeSecurityGroup, Properties: map[string]interface{}{"resource_id": "sg-shared-1"}}
	sg2 := &core.DbNode{NodeType: core.NodeTypeSecurityGroup, Properties: map[string]interface{}{"resource_id": "sg-shared-2"}}
	sgCluster := &core.DbNode{NodeType: core.NodeTypeSecurityGroup, Properties: map[string]interface{}{"resource_id": "sg-cluster"}}
	role := &core.DbNode{NodeType: core.NodeTypeServiceIdentity, Properties: map[string]interface{}{"arn": "arn:aws:iam::123456789012:role/nudgebee-eks-role"}}
	lookup := newNodeLookup([]*core.DbNode{eks, vpc, subnet1, subnet2, subnet3, sg1, sg2, sgCluster, role})

	edges := src.createEKSEdges([]*core.DbNode{eks}, lookup, &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"})

	// Expect: 1 HOSTED_ON→VPC + 3 HOSTED_ON→Subnet + 3 HOSTED_ON→SG + 1 RUNS_AS = 8 edges
	if len(edges) != 8 {
		t.Fatalf("expected 8 edges, got %d", len(edges))
	}

	counts := map[string]int{}
	for _, e := range edges {
		counts[string(e.RelationshipType)]++
	}
	if counts["HOSTED_ON"] != 7 {
		t.Errorf("HOSTED_ON edges = %d, want 7", counts["HOSTED_ON"])
	}
	if counts["RUNS_AS"] != 1 {
		t.Errorf("RUNS_AS edges = %d, want 1", counts["RUNS_AS"])
	}
}

// TestCreateEKSEdges_NoMetaCache — regression guard: if the per-build cache
// has no row for this cluster (eg the cache wasn't populated), the function
// must not panic and must emit 0 edges.
func TestCreateEKSEdges_NoMetaCache(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{}

	eks := &core.DbNode{
		NodeType:   core.NodeTypeManagedCluster,
		Properties: map[string]interface{}{"resource_id": "nudgebee"},
	}
	lookup := newNodeLookup([]*core.DbNode{eks})
	edges := src.createEKSEdges([]*core.DbNode{eks}, lookup, &core.SourceBuildRequest{})
	if len(edges) != 0 {
		t.Errorf("expected 0 edges when cache is empty, got %d", len(edges))
	}
}

// TestCreateEKSEdges_RunsAsOnlyWhenRoleFound — if role_arn is set but the
// matching ServiceIdentity node isn't in the lookup, we must not emit a
// dangling RUNS_AS edge.
func TestCreateEKSEdges_RunsAsOnlyWhenRoleFound(t *testing.T) {
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	clusterMeta := map[string]interface{}{
		"ResourcesVpcConfig": map[string]interface{}{},
	}
	metaBytes, _ := json.Marshal(clusterMeta)
	src.metaByTypeAndID = map[string]map[string]CloudResourceRow{
		"cluster": {"c": {ResourceID: "c", Type: "cluster", Meta: metaBytes}},
	}

	eks := &core.DbNode{
		NodeType: core.NodeTypeManagedCluster,
		Properties: map[string]interface{}{
			"resource_id": "c",
			"role_arn":    "arn:aws:iam::1:role/unmatched",
		},
	}
	// No ServiceIdentity node with that ARN in lookup.
	lookup := newNodeLookup([]*core.DbNode{eks})
	edges := src.createEKSEdges([]*core.DbNode{eks}, lookup, &core.SourceBuildRequest{})

	for _, e := range edges {
		if e.RelationshipType == core.RelationshipRunsAs {
			t.Errorf("must not emit RUNS_AS when ServiceIdentity is absent from lookup")
		}
	}
}
