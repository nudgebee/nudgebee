package recommendation

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// annotateBreakdownWithImpact must be a safe no-op when the knowledge-graph
// service is unavailable or the recommendation's identity is incomplete, so the
// always-on recompute cron never panics or stamps a band it can't justify.
// Incomplete identity (no k8s namespace and no cloud resource_id) must return
// before any graph call. Resolution against a live graph — k8s by (namespace,
// name), cloud by resource_id — is exercised by the post-deploy cron run, not here.
func TestAnnotateBreakdownWithImpactNoOp(t *testing.T) {
	cases := []struct {
		name       string
		kg         *core.Service
		namespace  string
		workload   string
		resourceID string
	}{
		{"nil graph service", nil, "shop", "checkout", ""},
		{"blank namespace", &core.Service{}, "   ", "checkout", ""},
		{"blank workload", &core.Service{}, "shop", "", ""},
		{"no namespace and no resource id", &core.Service{}, "", "", "   "},
	}

	cache := map[string]*core.ImpactSummary{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := map[string]any{"base_score": 10}
			annotateBreakdownWithImpact(tc.kg, "tenant", "acct", tc.namespace, tc.workload, tc.resourceID, ChangeClassReductive, bd, cache)
			if _, ok := bd["safety_band"]; ok {
				t.Error("must not stamp safety_band on a no-op path")
			}
			if _, ok := bd["impact_summary"]; ok {
				t.Error("must not stamp impact_summary on a no-op path")
			}
			if _, ok := bd["change_class"]; ok {
				t.Error("must not stamp change_class on a no-op path")
			}
		})
	}
	if len(cache) != 0 {
		t.Errorf("no-op paths must not populate the cache, got %d entries", len(cache))
	}
}

// The impact cache memoizes the traversal, not the verdict: two recommendations
// on the same resolved resource with different change classes must grade
// independently. Exercised through the cache-hit path (pre-seeded entry) so no
// live graph is needed.
func TestAnnotateBreakdownDerivesBandPerChangeClass(t *testing.T) {
	impact := &core.ImpactSummary{
		CoverageConfidence:   core.CoverageHigh,
		DependentCount:       3,
		ProductionDependents: 2,
		EnvironmentResolved:  true,
	}
	cache := map[string]*core.ImpactSummary{
		"tenant|acct|k8s|shop|checkout": impact,
	}

	additive := map[string]any{}
	annotateBreakdownWithImpact(&core.Service{}, "tenant", "acct", "shop", "checkout", "", ChangeClassAdditive, additive, cache)
	if additive["safety_band"] != string(SafetyBandReview) {
		t.Errorf("additive band = %v, want review (prod dependents cap at review for additive changes)", additive["safety_band"])
	}
	if additive["change_class"] != string(ChangeClassAdditive) {
		t.Errorf("change_class = %v, want additive", additive["change_class"])
	}

	destructive := map[string]any{}
	annotateBreakdownWithImpact(&core.Service{}, "tenant", "acct", "shop", "checkout", "", ChangeClassDestructive, destructive, cache)
	if destructive["safety_band"] != string(SafetyBandRisky) {
		t.Errorf("destructive band = %v, want risky", destructive["safety_band"])
	}

	unknown := map[string]any{}
	annotateBreakdownWithImpact(&core.Service{}, "tenant", "acct", "shop", "checkout", "", ChangeClassUnknown, unknown, cache)
	if unknown["safety_band"] != string(SafetyBandRisky) {
		t.Errorf("unknown-class band = %v, want risky (pre-change-aware policy)", unknown["safety_band"])
	}
	if _, ok := unknown["change_class"]; ok {
		t.Error("unknown class must not stamp change_class")
	}

	// Every call shares the one cached traversal, and each stamps its own reason.
	if len(cache) != 1 {
		t.Errorf("cache grew to %d entries, want 1 (traversal shared)", len(cache))
	}
	addSummary, _ := additive["impact_summary"].(map[string]any)
	destSummary, _ := destructive["impact_summary"].(map[string]any)
	if addSummary == nil || destSummary == nil || addSummary["safety_reason"] == destSummary["safety_reason"] {
		t.Errorf("safety_reason must be per-class: additive=%v destructive=%v", addSummary["safety_reason"], destSummary["safety_reason"])
	}
}

// The non-caller neighbourhoods are persisted only when present, and the
// hosted-workload pod counts survive compaction.
func TestBuildImpactSummaryNeighbourhoods(t *testing.T) {
	bare := buildImpactSummary(&core.ImpactSummary{CoverageConfidence: core.CoverageHigh}, "r")
	if _, ok := bare["infrastructure_dependents"]; ok {
		t.Error("empty infrastructure must not be persisted")
	}
	if _, ok := bare["hosted_workloads"]; ok {
		t.Error("empty hosted workloads must not be persisted")
	}

	full := buildImpactSummary(&core.ImpactSummary{
		CoverageConfidence:       core.CoverageHigh,
		InfrastructureCount:      1,
		InfrastructureDependents: []core.ImpactedService{{Name: "i-0abc", NodeType: core.NodeTypeComputeInstance, HopsAway: 1}},
		HostedWorkloadCount:      1,
		HostedWorkloads:          []core.ImpactedService{{Name: "k8s-collector-worker", NodeType: core.NodeTypeWorkload, Namespace: "nudgebee", PodCount: 12, HopsAway: 2}},
	}, "r")
	if full["infrastructure_count"] != 1 || full["hosted_workload_count"] != 1 {
		t.Errorf("counts not persisted: %v / %v", full["infrastructure_count"], full["hosted_workload_count"])
	}
	hosted, _ := full["hosted_workloads"].([]dependentRef)
	if len(hosted) != 1 || hosted[0].PodCount != 12 {
		t.Errorf("hosted workload pod count must survive compaction, got %+v", hosted)
	}
}

// compactDependents must project the graph's dependents into the persisted shape:
// preserve order, convert NodeType to string, and carry the identity/risk fields
// the safety UI and agent need.
func TestCompactDependents(t *testing.T) {
	deps := []core.ImpactedService{
		{NodeID: "uuid-1", Name: "checkout", NodeType: core.NodeTypeWorkload, Namespace: "shop", Environment: "prod", HopsAway: 1,
			Relationship: core.RelationshipCalls, Sources: []string{"ebpf", "traces"}},
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
	if first.Relationship != string(core.RelationshipCalls) || len(first.Sources) != 2 {
		t.Errorf("first dependent attribution = %q/%v, want CALLS/[ebpf traces]", first.Relationship, first.Sources)
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
