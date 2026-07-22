package core

import (
	"testing"
)

func gcpDataNode(id, account string, nt NodeType, name, dbType string) *DbNode {
	props := map[string]interface{}{"name": name}
	if dbType != "" {
		props["type"] = dbType
	}
	return &DbNode{ID: id, NodeType: nt, CloudAccountID: account, Source: "gcp", Properties: props, UniqueKey: id}
}

func esStub(id, name, account string) *DbNode {
	return &DbNode{
		ID:             id,
		NodeType:       NodeTypeExternalService,
		CloudAccountID: account,
		Source:         "gcp-cloud-traces",
		UniqueKey:      "ExternalService:" + name + ":",
		Properties:     map[string]interface{}{"name": name},
	}
}

func callsEdge(id, src, dst, callerAccount string) *DbEdge {
	return &DbEdge{ID: id, SourceNodeID: src, DestinationNodeID: dst, RelationshipType: RelationshipCalls, CloudAccountID: callerAccount}
}

// A caller in account A calling the shared "GCP Datastore" stub resolves to A's
// single Firestore database; the now-detached stub is dropped.
func TestResolveGCPManagedServiceCalls_FirestoreSameAccount(t *testing.T) {
	const acct = "acct-A"
	fs := gcpDataNode("fs-A", acct, NodeTypeDatabase, "(default)", "firestore")
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: acct, Source: "gcp"}
	stub := esStub("es-datastore", "GCP Datastore", acct)
	edge := callsEdge("e1", caller.ID, stub.ID, acct)

	nodes := []*DbNode{fs, caller, stub}
	edges := []*DbEdge{edge}

	outNodes, outEdges, rewritten := ResolveGCPManagedServiceCalls(nodes, edges, nil)

	if rewritten != 1 {
		t.Fatalf("expected 1 rewritten edge, got %d", rewritten)
	}
	if edge.DestinationNodeID != "fs-A" {
		t.Errorf("edge should repoint to fs-A, got %s", edge.DestinationNodeID)
	}
	// The PK (edge.ID) must be regenerated for the new destination, else it still
	// hashes the old stub dest and collides with the stub's PK row on save.
	if want := GenerateEdgeID(caller.ID, "fs-A", RelationshipCalls); edge.ID != want {
		t.Errorf("edge ID must be regenerated to %s after rewrite, got %s", want, edge.ID)
	}
	if got := edge.Properties[resolveGCPManagedServicePropResolvedBy]; got != resolveGCPManagedServiceResolver {
		t.Errorf("missing resolver provenance, got %v", got)
	}
	if edge.Properties[CollapseEdgePropOriginalHostname] != "GCP Datastore" {
		t.Errorf("missing original hostname provenance, got %v", edge.Properties[CollapseEdgePropOriginalHostname])
	}
	for _, n := range outNodes {
		if n.ID == "es-datastore" {
			t.Errorf("detached stub should have been dropped")
		}
	}
	if len(outEdges) != 1 {
		t.Errorf("expected edges preserved, got %d", len(outEdges))
	}
}

// The Firestore database lives in a different account than the caller → no
// same-account candidate → edge stays on the stub and the stub is retained.
func TestResolveGCPManagedServiceCalls_CrossAccountDeclined(t *testing.T) {
	fs := gcpDataNode("fs-B", "acct-B", NodeTypeDatabase, "(default)", "firestore")
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: "acct-A", Source: "gcp"}
	stub := esStub("es-datastore", "GCP Datastore", "acct-A")
	edge := callsEdge("e1", caller.ID, stub.ID, "acct-A")

	outNodes, _, rewritten := ResolveGCPManagedServiceCalls([]*DbNode{fs, caller, stub}, []*DbEdge{edge}, nil)

	if rewritten != 0 {
		t.Fatalf("expected 0 rewritten (cross-account), got %d", rewritten)
	}
	if edge.DestinationNodeID != "es-datastore" {
		t.Errorf("edge should remain on stub, got %s", edge.DestinationNodeID)
	}
	found := false
	for _, n := range outNodes {
		if n.ID == "es-datastore" {
			found = true
		}
	}
	if !found {
		t.Errorf("stub with a live edge must be retained")
	}
}

// Redis with several Memorystore instances in the caller's account is ambiguous
// → declined by the exactly-one guard.
func TestResolveGCPManagedServiceCalls_RedisAmbiguousDeclined(t *testing.T) {
	const acct = "acct-A"
	c1 := gcpDataNode("cache-1", acct, NodeTypeCache, "prod-cache", "memorystore")
	c2 := gcpDataNode("cache-2", acct, NodeTypeCache, "staging-cache", "memorystore")
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: acct, Source: "gcp"}
	stub := esStub("es-redis", "Redis", acct)
	edge := callsEdge("e1", caller.ID, stub.ID, acct)

	_, _, rewritten := ResolveGCPManagedServiceCalls([]*DbNode{c1, c2, caller, stub}, []*DbEdge{edge}, nil)

	if rewritten != 0 {
		t.Fatalf("expected 0 rewritten (ambiguous >1 candidate), got %d", rewritten)
	}
	if edge.DestinationNodeID != "es-redis" {
		t.Errorf("ambiguous edge should stay on stub, got %s", edge.DestinationNodeID)
	}
}

// One shared stub, two callers in two accounts each with their own Firestore:
// both edges resolve to their own account's DB, and the fully-detached stub drops.
func TestResolveGCPManagedServiceCalls_SharedStubPerCallerAccount(t *testing.T) {
	fsA := gcpDataNode("fs-A", "acct-A", NodeTypeDatabase, "(default)", "firestore")
	fsB := gcpDataNode("fs-B", "acct-B", NodeTypeDatabase, "(default)", "firestore")
	callerA := &DbNode{ID: "svc-a", NodeType: NodeTypeServerlessFunction, CloudAccountID: "acct-A", Source: "gcp"}
	callerB := &DbNode{ID: "svc-b", NodeType: NodeTypeService, CloudAccountID: "acct-B", Source: "gcp-cloud-traces"}
	stub := esStub("es-datastore", "Datastore", "acct-A")
	eA := callsEdge("eA", callerA.ID, stub.ID, "acct-A")
	eB := callsEdge("eB", callerB.ID, stub.ID, "acct-B")

	outNodes, _, rewritten := ResolveGCPManagedServiceCalls(
		[]*DbNode{fsA, fsB, callerA, callerB, stub}, []*DbEdge{eA, eB}, nil)

	if rewritten != 2 {
		t.Fatalf("expected 2 rewritten, got %d", rewritten)
	}
	if eA.DestinationNodeID != "fs-A" || eB.DestinationNodeID != "fs-B" {
		t.Errorf("edges should resolve per caller account: eA=%s eB=%s", eA.DestinationNodeID, eB.DestinationNodeID)
	}
	for _, n := range outNodes {
		if n.ID == "es-datastore" {
			t.Errorf("fully-detached shared stub should be dropped")
		}
	}
}

// A shared stub where only one of two callers can be resolved must be retained
// (the unresolved edge still references it).
func TestResolveGCPManagedServiceCalls_PartiallyResolvedStubRetained(t *testing.T) {
	fsA := gcpDataNode("fs-A", "acct-A", NodeTypeDatabase, "(default)", "firestore")
	callerA := &DbNode{ID: "svc-a", NodeType: NodeTypeServerlessFunction, CloudAccountID: "acct-A", Source: "gcp"}
	callerC := &DbNode{ID: "svc-c", NodeType: NodeTypeServerlessFunction, CloudAccountID: "acct-C", Source: "gcp"} // no DB in acct-C
	stub := esStub("es-datastore", "Firestore", "acct-A")
	eA := callsEdge("eA", callerA.ID, stub.ID, "acct-A")
	eC := callsEdge("eC", callerC.ID, stub.ID, "acct-C")

	outNodes, _, rewritten := ResolveGCPManagedServiceCalls(
		[]*DbNode{fsA, callerA, callerC, stub}, []*DbEdge{eA, eC}, nil)

	if rewritten != 1 {
		t.Fatalf("expected 1 rewritten, got %d", rewritten)
	}
	if eA.DestinationNodeID != "fs-A" {
		t.Errorf("acct-A edge should resolve, got %s", eA.DestinationNodeID)
	}
	if eC.DestinationNodeID != "es-datastore" {
		t.Errorf("acct-C edge should stay on stub, got %s", eC.DestinationNodeID)
	}
	retained := false
	for _, n := range outNodes {
		if n.ID == "es-datastore" {
			retained = true
		}
	}
	if !retained {
		t.Errorf("partially-resolved stub must be retained")
	}
}

// A Cloud SQL Database (type != firestore) must not satisfy a Datastore leaf even
// though both are NodeTypeDatabase.
func TestResolveGCPManagedServiceCalls_DbTypeConstraint(t *testing.T) {
	const acct = "acct-A"
	sql := gcpDataNode("db-sql", acct, NodeTypeDatabase, "orders", "cloudsql")
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: acct, Source: "gcp"}
	stub := esStub("es-datastore", "GCP Datastore", acct)
	edge := callsEdge("e1", caller.ID, stub.ID, acct)

	_, _, rewritten := ResolveGCPManagedServiceCalls([]*DbNode{sql, caller, stub}, []*DbEdge{edge}, nil)

	if rewritten != 0 {
		t.Fatalf("Cloud SQL must not satisfy a Datastore leaf, got %d rewritten", rewritten)
	}
}

// A non-GCP (e.g. trace-synthesized) Database node must not be a candidate.
func TestResolveGCPManagedServiceCalls_NonGCPCandidateIgnored(t *testing.T) {
	const acct = "acct-A"
	fs := gcpDataNode("fs-A", acct, NodeTypeDatabase, "(default)", "firestore")
	fs.Source = "k8s" // not collected from GCP
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: acct, Source: "gcp"}
	stub := esStub("es-datastore", "GCP Datastore", acct)
	edge := callsEdge("e1", caller.ID, stub.ID, acct)

	_, _, rewritten := ResolveGCPManagedServiceCalls([]*DbNode{fs, caller, stub}, []*DbEdge{edge}, nil)

	if rewritten != 0 {
		t.Fatalf("non-GCP database must not be a candidate, got %d rewritten", rewritten)
	}
}
