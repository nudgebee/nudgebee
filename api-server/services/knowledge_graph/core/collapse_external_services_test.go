package core

import (
	"io"
	"log/slog"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mkNode(id string, nodeType NodeType, name string) *DbNode {
	return &DbNode{
		ID:        id,
		NodeType:  nodeType,
		UniqueKey: string(nodeType) + ":" + id,
		Properties: map[string]interface{}{
			"name": name,
		},
	}
}

func mkEdge(id, src, dst string, rel RelationshipType) *DbEdge {
	return &DbEdge{
		ID:                id,
		SourceNodeID:      src,
		DestinationNodeID: dst,
		RelationshipType:  rel,
		Properties:        map[string]interface{}{},
	}
}

func edgeFor(t *testing.T, edges []*DbEdge, src, dst string, rel RelationshipType) *DbEdge {
	t.Helper()
	for _, e := range edges {
		if e.SourceNodeID == src && e.DestinationNodeID == dst && e.RelationshipType == rel {
			return e
		}
	}
	t.Fatalf("expected edge %s -[%s]-> %s, not found in %d edges", src, rel, dst, len(edges))
	return nil
}

func nodeIDs(nodes []*DbNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

// Happy path: ES with workload CALLS in and ROUTES_THROUGH out collapses to
// a direct CALLS edge to the cloud node, and the ES node disappears.
func TestCollapse_HappyPath_RoutesThrough(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "db.example.com")
	db := mkNode("db1", NodeTypeDatabase, "prod-db")

	calls := mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls)
	bridge := mkEdge("e_bridge", es.ID, db.ID, RelationshipRoutesThrough)

	gotNodes, gotEdges, redirectCount := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, db},
		[]*DbEdge{calls, bridge},
		quietLogger(),
	)

	if redirectCount != 1 {
		t.Fatalf("redirectCount = %d, want 1", redirectCount)
	}
	if len(gotNodes) != 2 {
		t.Fatalf("nodes = %v, want 2 (wl + db)", nodeIDs(gotNodes))
	}
	for _, n := range gotNodes {
		if n.ID == es.ID {
			t.Fatalf("ES node should have been pruned, got %v", nodeIDs(gotNodes))
		}
	}
	if len(gotEdges) != 1 {
		t.Fatalf("edges = %d, want 1 (the rewritten CALLS)", len(gotEdges))
	}
	rewritten := edgeFor(t, gotEdges, wl.ID, db.ID, RelationshipCalls)
	if got := rewritten.Properties[CollapseEdgePropOriginalHostname]; got != "db.example.com" {
		t.Errorf("original_hostname = %v, want db.example.com", got)
	}
	if got := rewritten.Properties[CollapseEdgePropOriginalESUniqueKey]; got != es.UniqueKey {
		t.Errorf("original_es_unique_key = %v, want %s", got, es.UniqueKey)
	}
}

// RESOLVES_TO is treated identically to ROUTES_THROUGH.
func TestCollapse_HappyPath_ResolvesTo(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "lb.example.com")
	lb := mkNode("lb1", NodeTypeLoadBalancer, "prod-lb")

	calls := mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls)
	bridge := mkEdge("e_bridge", es.ID, lb.ID, RelationshipResolvesTo)

	_, gotEdges, redirectCount := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, lb},
		[]*DbEdge{calls, bridge},
		quietLogger(),
	)

	if redirectCount != 1 {
		t.Fatalf("redirectCount = %d, want 1", redirectCount)
	}
	edgeFor(t, gotEdges, wl.ID, lb.ID, RelationshipCalls)
}

// An ES with no bridge edge survives untouched.
func TestCollapse_UnmatchedESSurvives(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "api.stripe.com")

	calls := mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls)

	gotNodes, gotEdges, redirectCount := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es},
		[]*DbEdge{calls},
		quietLogger(),
	)

	if redirectCount != 0 {
		t.Fatalf("redirectCount = %d, want 0", redirectCount)
	}
	if len(gotNodes) != 2 {
		t.Fatalf("nodes = %v, want 2", nodeIDs(gotNodes))
	}
	if len(gotEdges) != 1 {
		t.Fatalf("edges = %d, want 1", len(gotEdges))
	}
	if got, ok := gotEdges[0].Properties[CollapseEdgePropOriginalHostname]; ok {
		t.Errorf("unmatched ES edge should not be stamped, got %v", got)
	}
}

// Multiple workloads CALLS the same ES → all rewritten to the cloud target.
func TestCollapse_MultipleInboundCalls(t *testing.T) {
	wlA := mkNode("wlA", NodeTypeWorkload, "service-a")
	wlB := mkNode("wlB", NodeTypeWorkload, "service-b")
	wlC := mkNode("wlC", NodeTypeWorkload, "service-c")
	es := mkNode("es1", NodeTypeExternalService, "cache.example.com")
	cache := mkNode("c1", NodeTypeCache, "prod-redis")

	edges := []*DbEdge{
		mkEdge("e1", wlA.ID, es.ID, RelationshipCalls),
		mkEdge("e2", wlB.ID, es.ID, RelationshipCalls),
		mkEdge("e3", wlC.ID, es.ID, RelationshipCalls),
		mkEdge("e_bridge", es.ID, cache.ID, RelationshipRoutesThrough),
	}

	_, gotEdges, _ := CollapseEnrichedExternalServices(
		[]*DbNode{wlA, wlB, wlC, es, cache},
		edges,
		quietLogger(),
	)
	if len(gotEdges) != 3 {
		t.Fatalf("edges = %d, want 3 rewritten CALLS", len(gotEdges))
	}
	for _, src := range []string{wlA.ID, wlB.ID, wlC.ID} {
		edgeFor(t, gotEdges, src, cache.ID, RelationshipCalls)
	}
}

// Pre-existing direct CALLS edge from the same workload to the cloud node:
// after collapse, both edges exist with the same composite key. Caller will
// dedup; the collapse function itself does not. We assert that both edges are
// returned (one rewritten, one untouched) so the dedup pass has visibility
// into the priority resolution.
func TestCollapse_PreExistingDirectCallsCollision(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "db.example.com")
	db := mkNode("db1", NodeTypeDatabase, "prod-db")

	rewritten := mkEdge("e_rewritten", wl.ID, es.ID, RelationshipCalls)
	rewritten.Source = "traces"
	preExisting := mkEdge("e_pre", wl.ID, db.ID, RelationshipCalls)
	preExisting.Source = "ebpf"
	bridge := mkEdge("e_bridge", es.ID, db.ID, RelationshipRoutesThrough)

	_, gotEdges, _ := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, db},
		[]*DbEdge{rewritten, preExisting, bridge},
		quietLogger(),
	)
	if len(gotEdges) != 2 {
		t.Fatalf("edges = %d, want 2 (rewritten + pre-existing collide; dedup is caller's job)", len(gotEdges))
	}
	// Both should target db1; one carries provenance, the other does not.
	hasProvenance := 0
	for _, e := range gotEdges {
		if e.SourceNodeID != wl.ID || e.DestinationNodeID != db.ID || e.RelationshipType != RelationshipCalls {
			t.Errorf("unexpected edge: %+v", e)
		}
		if _, ok := e.Properties[CollapseEdgePropOriginalHostname]; ok {
			hasProvenance++
		}
	}
	if hasProvenance != 1 {
		t.Errorf("expected exactly 1 edge with provenance stamp, got %d", hasProvenance)
	}
}

// Non-CALLS inbound to a redirected ES: drop with a warning. No flow source
// emits this today; defensive against regressions.
func TestCollapse_NonCallsInboundToRedirectedESDropped(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "publisher-svc")
	es := mkNode("es1", NodeTypeExternalService, "queue.example.com")
	mq := mkNode("mq1", NodeTypeMessageQueue, "prod-queue")

	publishes := mkEdge("e_pub", wl.ID, es.ID, RelationshipPublishesTo)
	bridge := mkEdge("e_bridge", es.ID, mq.ID, RelationshipRoutesThrough)

	_, gotEdges, _ := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, mq},
		[]*DbEdge{publishes, bridge},
		quietLogger(),
	)
	for _, e := range gotEdges {
		if e.RelationshipType == RelationshipPublishesTo {
			t.Errorf("PUBLISHES_TO into redirected ES should have been dropped, got %+v", e)
		}
	}
}

// Idempotent: running the pass on its own output produces an identical result.
func TestCollapse_Idempotent(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "db.example.com")
	db := mkNode("db1", NodeTypeDatabase, "prod-db")
	calls := mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls)
	bridge := mkEdge("e_bridge", es.ID, db.ID, RelationshipRoutesThrough)

	n1, e1, _ := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, db}, []*DbEdge{calls, bridge}, quietLogger(),
	)
	n2, e2, redirectCount2 := CollapseEnrichedExternalServices(n1, e1, quietLogger())

	if redirectCount2 != 0 {
		t.Errorf("second pass redirectCount = %d, want 0 (no bridge edges left)", redirectCount2)
	}
	if len(n1) != len(n2) || len(e1) != len(e2) {
		t.Errorf("not idempotent: pass1=(n=%d,e=%d), pass2=(n=%d,e=%d)", len(n1), len(e1), len(n2), len(e2))
	}
}

// Multiple bridge edges on a single ES: keep the first, log the rest. Today's
// matcher cannot produce this; defensive against future emitters. Asserts
// determinism (input order decides the winner).
func TestCollapse_MultipleBridgesOnSameES_FirstWins(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "service")
	es := mkNode("es1", NodeTypeExternalService, "host.example.com")
	t1 := mkNode("t1", NodeTypeDatabase, "target-1")
	t2 := mkNode("t2", NodeTypeDatabase, "target-2")

	calls := mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls)
	bridgeA := mkEdge("e_brA", es.ID, t1.ID, RelationshipRoutesThrough)
	bridgeB := mkEdge("e_brB", es.ID, t2.ID, RelationshipResolvesTo)

	_, gotEdges, _ := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, t1, t2},
		[]*DbEdge{calls, bridgeA, bridgeB},
		quietLogger(),
	)
	edgeFor(t, gotEdges, wl.ID, t1.ID, RelationshipCalls)
	for _, e := range gotEdges {
		if e.DestinationNodeID == t2.ID && e.RelationshipType == RelationshipCalls {
			t.Errorf("CALLS should not have been routed to ignored target t2")
		}
	}
}
