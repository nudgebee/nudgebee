package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

func TestBuildVPCPeeringEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AWSSource{}

	vpcA := &core.DbNode{ID: "A", NodeType: core.NodeTypeVPC, Properties: map[string]interface{}{"resource_id": "vpc-a", "region": "us-east-1"}}
	vpcB := &core.DbNode{ID: "B", NodeType: core.NodeTypeVPC, Properties: map[string]interface{}{"resource_id": "vpc-b", "region": "us-east-1"}}
	lookup := sources.NewNodeLookup([]*core.DbNode{vpcA, vpcB})

	peerings := [][]string{
		{"vpc-a", "vpc-b", "active"},             // both present + active → edge
		{"vpc-a", "vpc-external", "active"},      // accepter not in graph (cross-account) → skip
		{"vpc-a", "vpc-b", "pending-acceptance"}, // not active → skip
		{"vpc-b", "", "active"},                  // malformed → skip
	}

	edges := s.buildVPCPeeringEdges(peerings, lookup, req)

	if len(edges) != 1 {
		t.Fatalf("expected 1 peering edge (vpc-a→vpc-b active), got %d", len(edges))
	}
	e := edges[0]
	conn, _ := e.Properties["connection_type"].(string)
	if e.SourceNodeID != "A" || e.DestinationNodeID != "B" ||
		e.RelationshipType != core.RelationshipAssociatedWith || conn != "vpc_peering" {
		t.Errorf("edge = %s→%s (%s/%s), want A→B ASSOCIATED_WITH/vpc_peering", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, conn)
	}
}
