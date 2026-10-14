package core

import "testing"

// Node whose only caller is cloud-collector-server is dropped.
func TestDropCollectorOnlyAWSEndpoints_CollectorOnly(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	es := mkNode("es1", NodeTypeExternalService, "elasticache.sa-east-1.amazonaws.com")
	calls := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)

	gotNodes, gotEdges, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(gotNodes) != 1 || gotNodes[0].ID != collector.ID {
		t.Fatalf("nodes = %v, want only collector", nodeIDs(gotNodes))
	}
	if len(gotEdges) != 0 {
		t.Fatalf("edges = %d, want 0", len(gotEdges))
	}
}

// A GovCloud/ISO region (4-segment, e.g. "us-gov-west-1") is still recognized
// as a bare regional control-plane host, not just the standard 3-segment form.
func TestDropCollectorOnlyAWSEndpoints_GovCloudRegion(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	es := mkNode("es1", NodeTypeExternalService, "elasticache.us-gov-west-1.amazonaws.com")
	calls := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)

	_, _, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

// A node also called by a customer workload is kept, including the
// collector's own edge to it.
func TestDropCollectorOnlyAWSEndpoints_CustomerCallerKeepsNode(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	customer := mkNode("wl2", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "dynamodb.us-east-1.amazonaws.com")
	collectorCall := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)
	customerCall := mkEdge("e2", customer.ID, es.ID, RelationshipCalls)

	gotNodes, gotEdges, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, customer, es},
		[]*DbEdge{collectorCall, customerCall},
		quietLogger(),
	)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(gotNodes) != 3 {
		t.Fatalf("nodes = %v, want 3 (unchanged)", nodeIDs(gotNodes))
	}
	if len(gotEdges) != 2 {
		t.Fatalf("edges = %d, want 2 (unchanged)", len(gotEdges))
	}
}

// A resource-specific endpoint (not a bare regional control-plane host) is
// never a candidate, even when the collector is its only caller.
func TestDropCollectorOnlyAWSEndpoints_ResourceSpecificHostUntouched(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	es := mkNode("es1", NodeTypeExternalService, "mydb.abc123.us-east-1.rds.amazonaws.com")
	calls := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)

	gotNodes, gotEdges, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(gotNodes) != 2 || len(gotEdges) != 1 {
		t.Fatalf("expected no change, got %d nodes / %d edges", len(gotNodes), len(gotEdges))
	}
}

// A non-AWS external service (e.g. a customer calling api.anthropic.com) is
// never a candidate.
func TestDropCollectorOnlyAWSEndpoints_NonAWSHostUntouched(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	es := mkNode("es1", NodeTypeExternalService, "api.anthropic.com")
	calls := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)

	_, _, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
}

// A manually-declared ExternalService node is never dropped, even if it
// matches the AWS regional host pattern and the collector is its only edge.
func TestDropCollectorOnlyAWSEndpoints_ManualNodeUntouched(t *testing.T) {
	collector := mkNode("wl1", NodeTypeWorkload, cloudCollectorWorkloadName)
	es := mkNode("es1", NodeTypeExternalService, "kafka.ap-northeast-3.amazonaws.com")
	es.Source = ManualNodeSource
	calls := mkEdge("e1", collector.ID, es.ID, RelationshipCalls)

	_, _, dropped := DropCollectorOnlyAWSEndpoints(
		[]*DbNode{collector, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
}
