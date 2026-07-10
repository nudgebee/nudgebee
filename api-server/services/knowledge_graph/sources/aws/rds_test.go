package aws

import (
	"fmt"
	"nudgebee/services/knowledge_graph/sources"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// node is a tiny helper to build a DbNode with a stable ID and resource_id.
func rdsTestNode(id string, nt core.NodeType, props map[string]interface{}) *core.DbNode {
	if props == nil {
		props = map[string]interface{}{}
	}
	return &core.DbNode{ID: id, NodeType: nt, Properties: props}
}

// edgeKeySet renders edges as a sorted, comparable slice of
// "src|dst|rel|connection_type" so two builders can be diffed as sets.
func edgeKeySet(edges []*core.DbEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		out = append(out, fmt.Sprintf("%s|%s|%s|%s", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, conn))
	}
	sort.Strings(out)
	return out
}

// TestAWSRDSEdges_EngineMatchesLegacy is the RDS vertical-slice fidelity gate:
// the declarative engine (createRDSEdgesViaEngine) must emit byte-identical edges
// to the hand-written createRDSEdges for the same nodes + lookup.
func TestAWSRDSEdges_EngineMatchesLegacy(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	rds := rdsTestNode("rds-node", core.NodeTypeDatabase, map[string]interface{}{
		"name":                   "orders-db",
		"resource_id":            "db-orders",
		"vpc_id":                 "vpc-1",
		"subnet_ids":             []string{"subnet-1", "subnet-2"},
		"vpc_security_group_ids": []string{"sg-1"},
	})
	vpc := rdsTestNode("vpc-node", core.NodeTypeVPC, map[string]interface{}{"resource_id": "vpc-1", "name": "vpc-1"})
	sub1 := rdsTestNode("sub1-node", core.NodeTypeSubnet, map[string]interface{}{"resource_id": "subnet-1"})
	sub2 := rdsTestNode("sub2-node", core.NodeTypeSubnet, map[string]interface{}{"resource_id": "subnet-2"})
	sg1 := rdsTestNode("sg1-node", core.NodeTypeSecurityGroup, map[string]interface{}{"resource_id": "sg-1"})

	nodes := []*core.DbNode{rds, vpc, sub1, sub2, sg1}
	lookup := sources.NewNodeLookup(nodes)
	s := &AWSSource{}

	legacy := s.createRDSEdges([]*core.DbNode{rds}, lookup, req)
	engine := s.createRDSEdgesViaEngine([]*core.DbNode{rds}, lookup, req)

	// 1 vpc + 2 subnets + 1 sg = 4 edges.
	if len(legacy) != 4 {
		t.Fatalf("expected 4 legacy edges, got %d", len(legacy))
	}

	legacyKeys := edgeKeySet(legacy)
	engineKeys := edgeKeySet(engine)
	if fmt.Sprint(legacyKeys) != fmt.Sprint(engineKeys) {
		t.Fatalf("edge set mismatch:\n legacy=%v\n engine=%v", legacyKeys, engineKeys)
	}

	// Every edge must be HostedOn, sourced "aws", and carry the same tenant/account.
	for _, e := range engine {
		if e.RelationshipType != core.RelationshipHostedOn {
			t.Errorf("engine edge rel = %s, want HOSTED_ON", e.RelationshipType)
		}
		if e.Source != "aws" || e.TenantID != "t1" || e.CloudAccountID != "a1" {
			t.Errorf("engine edge stamping mismatch: %+v", e)
		}
	}

	// Edge IDs must match (deterministic from src/dst/rel) — proves the engine's
	// output is identical, not just equivalent.
	legacyByID := map[string]bool{}
	for _, e := range legacy {
		legacyByID[e.ID] = true
	}
	for _, e := range engine {
		if !legacyByID[e.ID] {
			t.Errorf("engine edge ID %s not produced by legacy builder", e.ID)
		}
	}
}

// TestAWSRDSTopologyEdges checks the identifier-matched Aurora cluster-membership
// and read-replica edges built by createRDSTopologyEdges.
func TestAWSRDSTopologyEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AWSSource{}

	cluster := rdsTestNode("cluster-node", core.NodeTypeDatabase, map[string]interface{}{
		"name": "aurora-prod", "db_cluster_identifier": "aurora-prod",
	})
	cluster.SpecificType = "RDSCluster"
	member := rdsTestNode("member-node", core.NodeTypeDatabase, map[string]interface{}{
		"name": "aurora-prod-1", "db_instance_identifier": "aurora-prod-1",
		"db_cluster_identifier": "aurora-prod",
	})
	member.SpecificType = "RDSInstance"
	primary := rdsTestNode("primary-node", core.NodeTypeDatabase, map[string]interface{}{
		"name": "orders-primary", "db_instance_identifier": "orders-primary",
	})
	primary.SpecificType = "RDSInstance"
	replica := rdsTestNode("replica-node", core.NodeTypeDatabase, map[string]interface{}{
		"name": "orders-replica", "db_instance_identifier": "orders-replica",
		"read_replica_source": "orders-primary",
	})
	replica.SpecificType = "RDSInstance"

	nodes := []*core.DbNode{cluster, member, primary, replica}
	lookup := sources.NewNodeLookup(nodes)
	edges := s.createRDSTopologyEdges(nodes, lookup, req)

	if len(edges) != 2 {
		t.Fatalf("expected 2 topology edges (cluster member + read replica), got %d: %v", len(edges), edgeKeySet(edges))
	}

	var gotMember, gotReplica bool
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		if e.SourceNodeID == "member-node" && e.DestinationNodeID == "cluster-node" &&
			e.RelationshipType == core.RelationshipBelongsTo && conn == "rds_cluster_member" {
			gotMember = true
		}
		if e.SourceNodeID == "replica-node" && e.DestinationNodeID == "primary-node" &&
			e.RelationshipType == core.RelationshipAssociatedWith && conn == "read_replica" {
			gotReplica = true
		}
	}
	if !gotMember {
		t.Error("missing instance -BELONGS_TO(rds_cluster_member)-> cluster edge")
	}
	if !gotReplica {
		t.Error("missing replica -ASSOCIATED_WITH(read_replica)-> primary edge")
	}
}
