package api

import (
	"testing"
	"time"

	"nudgebee/services/triage"
)

// Node memory peers are fetched on their own because the window fetch keeps only
// the newest rows of a four-hour window, and a node running out of memory is when
// that window overflows. A peer the window did return is marked in place; one it
// cut is added back; the rows stay newest-first for the collapse.
func TestMergeNodeMemoryPeers(t *testing.T) {
	root := time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)
	row := func(id string, offset time.Duration, node string) windowRow {
		return windowRow{
			id:       triage.AlertIdentity{ID: id, SubjectName: id, NodeMemory: node, TsOffsetS: int(offset.Seconds())},
			startsAt: root.Add(offset),
		}
	}
	wr := []windowRow{
		row("newest", 30*time.Minute, ""),
		row("in-window-peer", 10*time.Second, ""),
		row("oldest", -time.Hour, ""),
	}
	peers := []windowRow{
		row("in-window-peer", 10*time.Second, "node-1"),
		row("cut-peer", -20*time.Second, "node-1"),
	}

	got := mergeNodeMemoryPeers(wr, peers)

	if len(got) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(got))
	}
	order := []string{"newest", "in-window-peer", "cut-peer", "oldest"}
	for i, id := range order {
		if got[i].id.ID != id {
			t.Fatalf("row %d = %q, want %q (rows must stay newest-first)", i, got[i].id.ID, id)
		}
	}
	nodes := map[string]string{"newest": "", "in-window-peer": "node-1", "cut-peer": "node-1", "oldest": ""}
	for _, r := range got {
		if r.id.NodeMemory != nodes[r.id.ID] {
			t.Errorf("%s: NodeMemory = %q, want %q", r.id.ID, r.id.NodeMemory, nodes[r.id.ID])
		}
	}

	// With no peers the window rows come back untouched.
	if same := mergeNodeMemoryPeers(wr[:1], nil); len(same) != 1 || same[0].id.ID != "newest" {
		t.Errorf("no peers should leave the rows alone, got %+v", same)
	}
}

// The merged rows are what the tiering sees: a peer in another namespace lands in
// same_node, not in the seed's own lane and not among the weak co-firing candidates.
func TestNodeMemoryPeerReachesTheSameNodeTier(t *testing.T) {
	seed := triage.AlertIdentity{ID: "seed", SubjectNamespace: "agents", SubjectOwner: "log-agent",
		AggregationKey: "pod_oom_killer_enricher", NodeMemory: "node-1"}
	wr := mergeNodeMemoryPeers(nil, []windowRow{{
		id: triage.AlertIdentity{ID: "peer", SubjectNamespace: "monitoring", SubjectOwner: "metrics-db",
			AggregationKey: "pod_oom_killer_enricher", NodeMemory: "node-1", TsOffsetS: -10},
	}})
	cands := []triage.AlertIdentity{wr[0].id}

	tiers := triage.AssembleTiers(seed, cands, nil, nil)
	if tiers["peer"] != triage.TierSameNode {
		t.Fatalf("peer tier = %q, want %q", tiers["peer"], triage.TierSameNode)
	}
	if co := triage.CoFiring(seed, cands, tiers, nil); len(co) != 0 {
		t.Errorf("a same_node peer must not also be co-firing, got %+v", co)
	}
}
