package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

func TestBuildASGNodesAndEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AWSSource{}

	i1 := &core.DbNode{ID: "i1", NodeType: core.NodeTypeComputeInstance, Properties: map[string]interface{}{"resource_id": "i-aaa"}}
	i2 := &core.DbNode{ID: "i2", NodeType: core.NodeTypeComputeInstance, Properties: map[string]interface{}{"resource_id": "i-bbb"}}
	lookup := sources.NewNodeLookup([]*core.DbNode{i1, i2})

	asgs := []asgData{
		{Name: "eks-node-group-x", Min: 2, Max: 6, Desired: 3, Instances: []string{"i-aaa", "i-bbb", "i-ghost"}},
	}
	nodes, edges := s.buildASGNodesAndEdges(asgs, "us-east-1", lookup, req)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 ASG node, got %d", len(nodes))
	}
	asg := nodes[0]
	if asg.NodeType != core.NodeTypeComputeInstancePool || asg.SpecificType != "AutoScalingGroup" {
		t.Errorf("ASG node type=%s specific=%s, want ComputeInstancePool/AutoScalingGroup", asg.NodeType, asg.SpecificType)
	}
	if asg.Properties["desired_capacity"] != 3 || asg.Properties["max_size"] != 6 {
		t.Errorf("ASG capacity props wrong: %+v", asg.Properties)
	}

	// i-aaa + i-bbb resolve; i-ghost dropped.
	if len(edges) != 2 {
		t.Fatalf("expected 2 EC2→ASG edges, got %d", len(edges))
	}
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		if e.DestinationNodeID != asg.ID || e.RelationshipType != core.RelationshipBelongsTo || conn != "auto_scaling_group" {
			t.Errorf("edge %s→%s (%s/%s), want *→ASG BELONGS_TO/auto_scaling_group", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, conn)
		}
	}
}
