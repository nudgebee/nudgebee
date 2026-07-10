package aws

import (
	"fmt"
	"nudgebee/services/knowledge_graph/sources"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// fullEdgeKeys renders edges as sorted "src|dst|rel|k=v,k=v" strings, including
// ALL edge properties — a stricter comparison than edgeKeySet, needed for edges
// that carry extra props (group_name, cluster_name).
func fullEdgeKeys(edges []*core.DbEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		kv := make([]string, 0, len(e.Properties))
		for k, v := range e.Properties {
			kv = append(kv, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(kv)
		out = append(out, fmt.Sprintf("%s|%s|%s|%v", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, kv))
	}
	sort.Strings(out)
	return out
}

// TestAWSEC2Edges_EngineMatchesLegacy proves the engine + escape-hatch path emits
// byte-identical edges to createEC2Edges, including the MapSliceSubKey security
// groups (with group_name) and the hand-written EC2->EKS RUNS_ON edge.
func TestAWSEC2Edges_EngineMatchesLegacy(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	ec2 := rdsTestNode("ec2-node", core.NodeTypeComputeInstance, map[string]interface{}{
		"name":        "web-1",
		"resource_id": "i-web1",
		"vpc_id":      "vpc-1",
		"subnet_id":   "subnet-1",
		"security_groups": []interface{}{
			map[string]interface{}{"GroupId": "sg-1", "GroupName": "web"},
			map[string]interface{}{"GroupId": "sg-2", "GroupName": "ssh"},
		},
		"labels": map[string]interface{}{"aws:eks:cluster-name": "prod-cluster"},
	})
	vpc := rdsTestNode("vpc-node", core.NodeTypeVPC, map[string]interface{}{"resource_id": "vpc-1"})
	sub := rdsTestNode("sub-node", core.NodeTypeSubnet, map[string]interface{}{"resource_id": "subnet-1"})
	sg1 := rdsTestNode("sg1-node", core.NodeTypeSecurityGroup, map[string]interface{}{"resource_id": "sg-1"})
	sg2 := rdsTestNode("sg2-node", core.NodeTypeSecurityGroup, map[string]interface{}{"resource_id": "sg-2"})
	eks := rdsTestNode("eks-node", core.NodeTypeManagedCluster, map[string]interface{}{"name": "prod-cluster"})

	nodes := []*core.DbNode{ec2, vpc, sub, sg1, sg2, eks}
	lookup := sources.NewNodeLookup(nodes)
	s := &AWSSource{}

	legacy := s.createEC2Edges([]*core.DbNode{ec2}, lookup, req)
	engine := s.createEC2EdgesViaEngine([]*core.DbNode{ec2}, lookup, req)

	// 1 vpc + 1 subnet + 2 sg + 1 eks = 5 edges.
	if len(legacy) != 5 {
		t.Fatalf("expected 5 legacy edges, got %d: %v", len(legacy), fullEdgeKeys(legacy))
	}
	lk, ek := fullEdgeKeys(legacy), fullEdgeKeys(engine)
	if fmt.Sprint(lk) != fmt.Sprint(ek) {
		t.Fatalf("EC2 edge set mismatch:\n legacy=%v\n engine=%v", lk, ek)
	}
}
