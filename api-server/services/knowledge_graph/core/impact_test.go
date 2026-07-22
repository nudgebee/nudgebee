package core

import "testing"

func newImpactTestNode(id string, nt NodeType, name, env, ns string) *DbNode {
	qa := map[string]interface{}{}
	if name != "" {
		qa["name"] = name
	}
	if env != "" {
		qa["environment"] = env
	}
	if ns != "" {
		qa["namespace"] = ns
	}
	return &DbNode{ID: id, NodeType: nt, QueryAttributes: qa, UniqueKey: id}
}

func TestSummarizeImpact_CountsAppDependentsAndProd(t *testing.T) {
	seedID := "db-1"
	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeDatabase, "orders-db", "prod", ""),
		newImpactTestNode("svc-1", NodeTypeService, "orders", "prod", "shop"),
		newImpactTestNode("wl-1", NodeTypeWorkload, "checkout", "production", "shop"),
		newImpactTestNode("svc-2", NodeTypeService, "orders-canary", "staging", "shop"),
		newImpactTestNode("ns-1", NodeTypeNamespace, "shop", "", ""), // infra intermediate, not an app dependent
	}
	minDepth := map[string]int{seedID: 0, "svc-1": 1, "wl-1": 1, "svc-2": 2, "ns-1": 1}
	edges := []*DbEdge{
		{
			SourceNodeID: "svc-1", DestinationNodeID: seedID, RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "traces"}, {Source: "datadog"}},
		},
	}

	got := summarizeImpact(seedID, NodeTypeDatabase, nodes, edges, minDepth)

	if got.DependentCount != 3 {
		t.Errorf("DependentCount = %d, want 3 (2 services + 1 workload; namespace excluded)", got.DependentCount)
	}
	if got.ProductionDependents != 2 {
		t.Errorf("ProductionDependents = %d, want 2", got.ProductionDependents)
	}
	if got.CoverageConfidence != CoverageHigh {
		t.Errorf("CoverageConfidence = %q, want high (multi-source edge)", got.CoverageConfidence)
	}
	if got.DependentsByType[NodeTypeNamespace] != 1 {
		t.Errorf("namespace should still appear in DependentsByType, got %v", got.DependentsByType)
	}
	// Sorted by (hops, name): hop 1 {checkout, orders} then hop 2 {orders-canary}.
	wantOrder := []string{"wl-1", "svc-1", "svc-2"}
	if len(got.Dependents) != 3 {
		t.Fatalf("expected 3 dependents, got %d: %+v", len(got.Dependents), got.Dependents)
	}
	for i, id := range wantOrder {
		if got.Dependents[i].NodeID != id {
			t.Errorf("dependents not sorted by (hops, name): position %d = %q, want %q (full: %+v)", i, got.Dependents[i].NodeID, id, got.Dependents)
		}
	}
}

func TestSummarizeImpact_SingleSourceIsLowCoverage(t *testing.T) {
	seedID := "vol-1"
	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeStorage, "data-vol", "", ""),
		newImpactTestNode("wl-1", NodeTypeWorkload, "ingester", "dev", "data"),
	}
	edges := []*DbEdge{
		{
			SourceNodeID: "wl-1", DestinationNodeID: seedID, RelationshipType: RelationshipMounts,
			ContributingSources: []EdgeContributingSource{{Source: "k8s"}},
		},
	}
	got := summarizeImpact(seedID, NodeTypeStorage, nodes, edges, map[string]int{seedID: 0, "wl-1": 1})
	if got.CoverageConfidence != CoverageLow {
		t.Errorf("CoverageConfidence = %q, want low", got.CoverageConfidence)
	}
	if got.DependentCount != 1 || got.ProductionDependents != 0 {
		t.Errorf("got DependentCount=%d prod=%d, want 1/0", got.DependentCount, got.ProductionDependents)
	}
}

func TestSummarizeImpact_NoDependentsLowNotNone(t *testing.T) {
	// An orphaned resource that IS present in the graph with no dependency edges
	// is low coverage, not none — none is reserved for a seed absent entirely,
	// decided by the caller before traversal.
	seedID := "vol-orphan"
	nodes := []*DbNode{newImpactTestNode(seedID, NodeTypeStorage, "orphan-vol", "", "")}
	got := summarizeImpact(seedID, NodeTypeStorage, nodes, nil, map[string]int{seedID: 0})
	if got.DependentCount != 0 {
		t.Errorf("DependentCount = %d, want 0", got.DependentCount)
	}
	if got.CoverageConfidence != CoverageLow {
		t.Errorf("CoverageConfidence = %q, want low", got.CoverageConfidence)
	}
}

func TestDefaultImpactRelationshipStrings(t *testing.T) {
	db := defaultImpactRelationshipStrings(NodeTypeDatabase)
	if len(db) != 1 || db[0] != string(RelationshipCalls) {
		t.Errorf("Database defaults = %v, want [CALLS]", db)
	}
	fb := defaultImpactRelationshipStrings(NodeType("Mystery"))
	if len(fb) != len(fallbackImpactRelationships) {
		t.Errorf("unknown type should use fallback (%d rels), got %v", len(fallbackImpactRelationships), fb)
	}
}

func TestIsProdEnv(t *testing.T) {
	for _, e := range []string{"prod", "Production", " PRD "} {
		if !isProdEnv(e) {
			t.Errorf("isProdEnv(%q) = false, want true", e)
		}
	}
	for _, e := range []string{"dev", "staging", "", "preprod"} {
		if isProdEnv(e) {
			t.Errorf("isProdEnv(%q) = true, want false", e)
		}
	}
}

func TestFilterNodesByTenant(t *testing.T) {
	nodes := []*DbNode{
		{ID: "a", TenantID: "t1"},
		{ID: "b", TenantID: "t2"},
		{ID: "c", TenantID: "t1"},
		nil,
	}
	got := filterNodesByTenant(nodes, "t1")
	ids := nodeIDsOf(got)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "c" {
		t.Errorf("filterNodesByTenant/nodeIDsOf = %v, want [a c] (drops other tenant + nil)", ids)
	}
}
