package core

import "testing"

// relEdge builds an edge by relationship; the package already has an edge()
// helper keyed on id, so this one is named for what it varies.
func relEdge(src, dst string, rel RelationshipType) *DbEdge {
	return &DbEdge{SourceNodeID: src, DestinationNodeID: dst, RelationshipType: rel}
}

func hasEdge(edges []*DbEdge, src, dst string, rel RelationshipType) bool {
	for _, e := range edges {
		if e.SourceNodeID == src && e.DestinationNodeID == dst && e.RelationshipType == rel {
			return true
		}
	}
	return false
}

// TestDropResponseTrafficCalls_BreaksTheLoadBalancerCycle is the case this pass
// exists for. Flow logs are symmetric, so once a balancer's own addresses
// resolve onto the balancer, the backend answering it becomes a CALLS edge
// pointing straight back along the balancer's ROUTES_TO. The two then appear to
// need each other, and nothing downstream can tell which way a failure travels:
// blast radius reports the balancer as impacted by its own backend, and incident
// grouping sees a tie where it needs a direction.
func TestDropResponseTrafficCalls_BreaksTheLoadBalancerCycle(t *testing.T) {
	edges := []*DbEdge{
		relEdge("alb", "web", RelationshipRoutesTo),
		relEdge("web", "alb", RelationshipCalls), // the reply
		relEdge("web", "api", RelationshipCalls), // a real dependency
	}

	_, got, dropped := DropResponseTrafficCalls(nil, edges, nil)

	if dropped != 1 {
		t.Errorf("dropped %d edges, want 1", dropped)
	}
	if hasEdge(got, "web", "alb", RelationshipCalls) {
		t.Error("the reply edge survived; the balancer and its backend still look mutually dependent")
	}
	if !hasEdge(got, "alb", "web", RelationshipRoutesTo) {
		t.Error("the routing edge was dropped — it is the provider's own configuration and must win")
	}
	if !hasEdge(got, "web", "api", RelationshipCalls) {
		t.Error("a genuine dependency was dropped")
	}
}

// Only the edge opposite a routing assertion goes. A CALLS edge between two
// things with no routing relationship is ordinary traffic, and a mutual CALLS
// pair is two services that really do call each other.
func TestDropResponseTrafficCalls_LeavesOrdinaryTrafficAlone(t *testing.T) {
	edges := []*DbEdge{
		relEdge("alb", "web", RelationshipRoutesTo),
		relEdge("a", "b", RelationshipCalls),
		relEdge("b", "a", RelationshipCalls),
		relEdge("web", "db", RelationshipCalls),
	}

	_, got, dropped := DropResponseTrafficCalls(nil, edges, nil)

	if dropped != 0 {
		t.Errorf("dropped %d edges, want 0", dropped)
	}
	if len(got) != len(edges) {
		t.Errorf("kept %d of %d edges", len(got), len(edges))
	}
}

// Every routing type counts, not just the AWS-shaped one.
func TestDropResponseTrafficCalls_CoversEveryRoutingType(t *testing.T) {
	for _, rel := range []RelationshipType{
		RelationshipRoutesTo, RelationshipRoutesToBackend,
		RelationshipRoutesToService, RelationshipRoutesThrough,
	} {
		t.Run(string(rel), func(t *testing.T) {
			edges := []*DbEdge{relEdge("front", "back", rel), relEdge("back", "front", RelationshipCalls)}
			_, got, dropped := DropResponseTrafficCalls(nil, edges, nil)
			if dropped != 1 {
				t.Errorf("dropped %d, want 1", dropped)
			}
			if hasEdge(got, "back", "front", RelationshipCalls) {
				t.Errorf("reply edge survived a %s assertion", rel)
			}
		})
	}
}

// A graph with no routing edges must come back untouched, and cheaply — this
// runs on every build for every tenant.
func TestDropResponseTrafficCalls_NoRoutingEdgesIsANoOp(t *testing.T) {
	edges := []*DbEdge{relEdge("a", "b", RelationshipCalls), relEdge("b", "c", RelationshipCalls)}
	_, got, dropped := DropResponseTrafficCalls(nil, edges, nil)
	if dropped != 0 || len(got) != 2 {
		t.Errorf("dropped=%d kept=%d, want 0 and 2", dropped, len(got))
	}
}
