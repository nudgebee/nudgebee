package core

import "log/slog"

// routingRelationships are the edges that assert "this thing sends traffic to
// that thing". A CALLS edge pointing back along one of them is the response.
var routingRelationships = map[RelationshipType]bool{
	RelationshipRoutesTo:        true,
	RelationshipRoutesToBackend: true,
	RelationshipRoutesToService: true,
	RelationshipRoutesThrough:   true,
}

// DropResponseTrafficCalls removes a CALLS edge whose reverse is already
// asserted as routing — a backend "calling" the load balancer in front of it.
// Pure function over (nodes, edges); nodes are returned untouched and take part
// only so the caller can chain it with the other phase-3 passes.
//
// Flow logs are symmetric: they record the backend answering the balancer just
// as plainly as the balancer addressing the backend. While a balancer's ENIs
// were unresolved that produced a harmless-looking edge to a bare IP, but once
// those addresses resolve onto the balancer itself the edge becomes
// `backend --CALLS--> balancer`, sitting opposite the balancer's own ROUTES_TO.
//
// That is not a dependency. The backend does not need the balancer to do its
// work; it is replying down the connection the balancer opened. Left in, the
// graph says the two need each other, and anything asking which way a failure
// travels gets no answer: blast radius reports the balancer as impacted by its
// own backend, and incident grouping — which elects the most depended-upon
// member as the cause — sees a tie and falls back to whichever alert happened to
// fire first, which on this shape is the symptom.
//
// Deliberately one-directional: the routing edge is kept and only the CALLS edge
// against it is dropped. A routing assertion comes from the provider's own
// configuration (a target group, an ingress rule), while the CALLS edge is
// inferred from packets, so where they disagree the configuration wins.
func DropResponseTrafficCalls(nodes []*DbNode, edges []*DbEdge, logger *slog.Logger) ([]*DbNode, []*DbEdge, int) {
	if logger == nil {
		logger = slog.Default()
	}

	// Every routing assertion, keyed source->destination.
	//
	// Deliberately not pre-sized from len(edges): this map holds only the routing
	// edges, which are a rounding error next to the rest. Measured on a live
	// tenant, 397 of 53,458 active edges are routing — 0.74% — so a len(edges)
	// hint would size the bucket array around 130x larger than it ever gets used,
	// on every graph build. Letting it grow from nothing costs a few doublings to
	// reach a few hundred entries.
	routes := make(map[string]bool)
	for _, e := range edges {
		if e != nil && routingRelationships[RelationshipType(e.RelationshipType)] {
			routes[e.SourceNodeID+"\x00"+e.DestinationNodeID] = true
		}
	}
	if len(routes) == 0 {
		return nodes, edges, 0
	}

	kept := make([]*DbEdge, 0, len(edges))
	dropped := 0
	for _, e := range edges {
		if e == nil {
			continue
		}
		// A CALLS edge B->A is response traffic when A->B is already routing.
		if RelationshipType(e.RelationshipType) == RelationshipCalls &&
			routes[e.DestinationNodeID+"\x00"+e.SourceNodeID] {
			dropped++
			logger.Debug("dropping response-traffic CALLS edge",
				"source", e.SourceNodeID, "destination", e.DestinationNodeID)
			continue
		}
		kept = append(kept, e)
	}

	if dropped > 0 {
		logger.Info("dropped response-traffic CALLS edges", "count", dropped)
	}
	return nodes, kept, dropped
}
