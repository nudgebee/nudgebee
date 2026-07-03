package recommendation

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// annotateBreakdownWithImpact must be a safe no-op when the knowledge-graph
// service is unavailable or the recommendation's identity is incomplete, so the
// always-on recompute cron never panics or stamps a band it can't justify.
// Resolution against a live graph is exercised by the post-deploy cron run (this
// path now derives ns+name from the cloud_resourses join, not the rec JSONB), not
// by a unit test.
func TestAnnotateBreakdownWithImpactNoOp(t *testing.T) {
	cases := []struct {
		name      string
		kg        *core.Service
		namespace string
		workload  string
	}{
		{"nil graph service", nil, "shop", "checkout"},
		{"blank namespace", &core.Service{}, "   ", "checkout"},
		{"blank workload", &core.Service{}, "shop", ""},
	}

	cache := map[string]*recommendationImpact{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := map[string]any{"base_score": 10}
			annotateBreakdownWithImpact(tc.kg, "tenant", "acct", tc.namespace, tc.workload, bd, cache)
			if _, ok := bd["safety_band"]; ok {
				t.Error("must not stamp safety_band on a no-op path")
			}
			if _, ok := bd["impact_summary"]; ok {
				t.Error("must not stamp impact_summary on a no-op path")
			}
		})
	}
	if len(cache) != 0 {
		t.Errorf("no-op paths must not populate the cache, got %d entries", len(cache))
	}
}

// compactDependents must project the graph's dependents into the persisted shape:
// preserve order, convert NodeType to string, and carry the identity/risk fields
// the safety UI and agent need.
func TestCompactDependents(t *testing.T) {
	deps := []core.ImpactedService{
		{NodeID: "uuid-1", Name: "checkout", NodeType: core.NodeTypeWorkload, Namespace: "shop", Environment: "prod", HopsAway: 1},
		{NodeID: "uuid-2", Name: "reporting", NodeType: core.NodeTypeService, Environment: "", HopsAway: 2},
	}

	got := compactDependents(deps)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%#v)", len(got), got)
	}
	first := got[0]
	if first.Name != "checkout" || first.Namespace != "shop" || first.NodeType != string(core.NodeTypeWorkload) ||
		first.Environment != "prod" || first.HopsAway != 1 {
		t.Errorf("first dependent = %#v, want checkout/shop/workload/prod/1", first)
	}
	// node_id must not leak into the persisted shape (it's a graph-internal UUID).
	// Environment is empty on the second: it must round-trip as omitted, not "".
	if got[1].Name != "reporting" || got[1].Namespace != "" || got[1].Environment != "" {
		t.Errorf("second dependent = %#v, want reporting with empty namespace/env", got[1])
	}
}

// A nil/empty blast radius must yield a non-nil empty slice so the persisted JSON
// is [] rather than null — consumers read it as "resolved, zero dependents".
func TestCompactDependentsEmptyIsNonNil(t *testing.T) {
	got := compactDependents(nil)
	if got == nil {
		t.Fatal("compactDependents(nil) = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// The stored list is bounded at maxStoredDependents (dependent_count carries the
// true total); the retained entries are the closest-first prefix.
func TestCompactDependentsCapsList(t *testing.T) {
	deps := make([]core.ImpactedService, maxStoredDependents+10)
	for i := range deps {
		deps[i] = core.ImpactedService{Name: string(rune('a' + i%26)), NodeType: core.NodeTypeService, HopsAway: i}
	}

	got := compactDependents(deps)
	if len(got) != maxStoredDependents {
		t.Fatalf("len = %d, want %d (capped)", len(got), maxStoredDependents)
	}
	if got[0].HopsAway != 0 || got[maxStoredDependents-1].HopsAway != maxStoredDependents-1 {
		t.Errorf("cap must retain the leading prefix, got first hops=%d last hops=%d", got[0].HopsAway, got[maxStoredDependents-1].HopsAway)
	}
}
