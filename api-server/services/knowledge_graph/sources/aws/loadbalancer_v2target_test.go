package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

func TestBuildLBTargetEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AWSSource{}

	lb := &core.DbNode{ID: "alb", NodeType: core.NodeTypeLoadBalancer, Properties: map[string]interface{}{"name": "web-alb"}}
	i1 := &core.DbNode{ID: "i1", NodeType: core.NodeTypeComputeInstance, Properties: map[string]interface{}{"resource_id": "i-aaa"}}
	i2 := &core.DbNode{ID: "i2", NodeType: core.NodeTypeComputeInstance, Properties: map[string]interface{}{"resource_id": "i-bbb"}}
	lookup := sources.NewNodeLookup([]*core.DbNode{lb, i1, i2})

	// i-aaa + i-bbb resolve; i-ghost has no node → dropped.
	edges := s.buildLBTargetEdges(lb, []string{"i-aaa", "i-bbb", "i-ghost"}, lookup, req)

	if len(edges) != 2 {
		t.Fatalf("expected 2 LB→instance edges, got %d", len(edges))
	}
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		if e.SourceNodeID != "alb" || e.RelationshipType != core.RelationshipRoutesTo || conn != "instance_target" {
			t.Errorf("edge %s→%s (%s/%s), want alb→* ROUTES_TO/instance_target", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, conn)
		}
	}
}

func TestParseAWSCliData(t *testing.T) {
	// Well-formed target-health output: list of instance IDs.
	var ids []string
	if !parseAWSCliData(map[string]any{"data": `["i-aaa","i-bbb"]`}, &ids) || len(ids) != 2 {
		t.Errorf("parseAWSCliData valid list = %v", ids)
	}
	// Missing/empty/non-JSON data → false.
	var x []string
	if parseAWSCliData(map[string]any{}, &x) {
		t.Error("missing data should return false")
	}
	if parseAWSCliData(map[string]any{"data": "An error occurred (AccessDenied)"}, &x) {
		t.Error("non-JSON stderr should return false")
	}
}
