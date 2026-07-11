package core

import (
	"testing"
)

func edge(id, src, dst string) *DbEdge {
	return &DbEdge{ID: id, SourceNodeID: src, DestinationNodeID: dst}
}

func edgeIDSetFrom(edges []*DbEdge) map[string]bool {
	m := make(map[string]bool, len(edges))
	for _, e := range edges {
		m[e.ID] = true
	}
	return m
}

// TestAddBridgeEdges covers reconnecting a multi-seed layered forest.
func TestAddBridgeEdges(t *testing.T) {
	t.Run("connects two trees via a single non-layered edge", func(t *testing.T) {
		// Tree A: lb -> bp ; Tree B: ks -> wl . Bridge bp <-> wl connects them
		// (both bp and wl are depth-1, so it was dropped by the layered filter).
		layered := []*DbEdge{
			edge("e1", "lb", "bp"),
			edge("e2", "ks", "wl"),
		}
		candidates := []*DbEdge{
			edge("e1", "lb", "bp"),
			edge("e2", "ks", "wl"),
			edge("e3", "bp", "wl"), // the bridge, not in layered set
		}
		got := addBridgeEdges(layered, candidates)
		ids := edgeIDSetFrom(got)
		if !ids["e3"] {
			t.Errorf("expected bridge e3 to be added, got %v", ids)
		}
		if len(got) != 3 {
			t.Errorf("expected 3 edges (2 layered + 1 bridge), got %d", len(got))
		}
	})

	t.Run("adds only the minimal bridges (union-find)", func(t *testing.T) {
		// Two trees, three candidate edges all connecting the same pair of trees.
		// Only one bridge should be added; the rest are redundant.
		layered := []*DbEdge{
			edge("e1", "lb", "bp"),
			edge("e2", "ks", "wl"),
		}
		candidates := []*DbEdge{
			edge("e5", "bp", "wl"),
			edge("e3", "bp", "ks"),
			edge("e4", "lb", "wl"),
		}
		got := addBridgeEdges(layered, candidates)
		ids := edgeIDSetFrom(got)
		// Deterministic: candidates sorted by id -> e3 chosen first, joins the trees.
		if !ids["e3"] {
			t.Errorf("expected lowest-id bridge e3 to be chosen, got %v", ids)
		}
		if ids["e4"] || ids["e5"] {
			t.Errorf("redundant bridges should be skipped once components are joined, got %v", ids)
		}
		if len(got) != 3 {
			t.Errorf("expected 2 layered + 1 bridge = 3 edges, got %d", len(got))
		}
	})

	t.Run("single connected tree adds no bridges", func(t *testing.T) {
		layered := []*DbEdge{
			edge("e1", "root", "a"),
			edge("e2", "root", "b"),
		}
		// Induced set includes a sibling edge a<->b that the layered filter dropped.
		candidates := []*DbEdge{
			edge("e1", "root", "a"),
			edge("e2", "root", "b"),
			edge("e3", "a", "b"), // sibling edge within one tree, not a bridge
		}
		got := addBridgeEdges(layered, candidates)
		if len(got) != 2 {
			t.Errorf("already-connected tree should gain no bridges, got %d edges", len(got))
		}
	})

	t.Run("empty candidates returns layered unchanged", func(t *testing.T) {
		layered := []*DbEdge{edge("e1", "a", "b")}
		got := addBridgeEdges(layered, nil)
		if len(got) != 1 || got[0].ID != "e1" {
			t.Errorf("expected layered edges unchanged, got %v", got)
		}
	})

	t.Run("three disconnected trees get two bridges", func(t *testing.T) {
		layered := []*DbEdge{
			edge("a1", "a", "a2"),
			edge("b1", "b", "b2"),
			edge("c1", "c", "c2"),
		}
		candidates := []*DbEdge{
			edge("x1", "a2", "b"), // joins A-B
			edge("x2", "b2", "c"), // joins B-C
		}
		got := addBridgeEdges(layered, candidates)
		ids := edgeIDSetFrom(got)
		if !ids["x1"] || !ids["x2"] {
			t.Errorf("expected both bridges to connect 3 trees, got %v", ids)
		}
		if len(got) != 5 {
			t.Errorf("expected 3 layered + 2 bridges = 5, got %d", len(got))
		}
	})
}
