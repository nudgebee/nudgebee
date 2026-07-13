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
	// svc-1 has a connecting edge in the fixture: it must carry the relationship
	// and the sorted union of the edge's sources. wl-1 has none — attribution
	// stays empty rather than inventing provenance.
	svc1 := got.Dependents[1]
	if svc1.Relationship != RelationshipCalls {
		t.Errorf("svc-1 Relationship = %q, want CALLS", svc1.Relationship)
	}
	if len(svc1.Sources) != 2 || svc1.Sources[0] != "datadog" || svc1.Sources[1] != "traces" {
		t.Errorf("svc-1 Sources = %v, want [datadog traces]", svc1.Sources)
	}
	if got.Dependents[0].Relationship != "" || len(got.Dependents[0].Sources) != 0 {
		t.Errorf("wl-1 has no connecting edge, want empty attribution, got %+v", got.Dependents[0])
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

func TestAttributeConnectingEdges(t *testing.T) {
	// seed(0) ← a(1) ← c(2); b(1) is a sibling of a. Edges: a→seed twice (CALLS
	// single-source, MOUNTS dual-source), a→b sibling noise, b→seed legacy edge
	// with only the winning Source, c→a one layer deeper.
	depth := map[string]int{"seed": 0, "a": 1, "b": 1, "c": 2}
	edges := []*DbEdge{
		{SourceNodeID: "a", DestinationNodeID: "seed", RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "ebpf"}}},
		{SourceNodeID: "a", DestinationNodeID: "seed", RelationshipType: RelationshipMounts,
			ContributingSources: []EdgeContributingSource{{Source: "k8s"}, {Source: "traces"}}},
		{SourceNodeID: "a", DestinationNodeID: "b", RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "noise"}}},
		{SourceNodeID: "b", DestinationNodeID: "seed", RelationshipType: RelationshipCalls, Source: "k8s"},
		{SourceNodeID: "c", DestinationNodeID: "a", RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "traces"}}},
		nil,
	}

	got := attributeConnectingEdges(edges, depth, TraverseDirectionUpstream)

	a := got["a"]
	if a.relationship != RelationshipMounts {
		t.Errorf("a relationship = %q, want MOUNTS (better corroborated)", a.relationship)
	}
	if len(a.sources) != 3 || a.sources[0] != "ebpf" || a.sources[1] != "k8s" || a.sources[2] != "traces" {
		t.Errorf("a sources = %v, want union [ebpf k8s traces] (sibling edge excluded)", a.sources)
	}
	b := got["b"]
	if b.relationship != RelationshipCalls || len(b.sources) != 1 || b.sources[0] != "k8s" {
		t.Errorf("b = %+v, want CALLS with legacy Source fallback [k8s]", b)
	}
	c := got["c"]
	if c.relationship != RelationshipCalls || len(c.sources) != 1 || c.sources[0] != "traces" {
		t.Errorf("c = %+v, want CALLS/[traces] via its own layer-2 edge", c)
	}

	// Downstream mirrors the direction: seed→x edges attribute x.
	downDepth := map[string]int{"seed": 0, "x": 1}
	downEdges := []*DbEdge{
		{SourceNodeID: "seed", DestinationNodeID: "x", RelationshipType: RelationshipPublishesTo,
			ContributingSources: []EdgeContributingSource{{Source: "ebpf"}}},
	}
	down := attributeConnectingEdges(downEdges, downDepth, TraverseDirectionDownstream)
	if x := down["x"]; x.relationship != RelationshipPublishesTo || len(x.sources) != 1 || x.sources[0] != "ebpf" {
		t.Errorf("downstream x = %+v, want PUBLISHES_TO/[ebpf]", x)
	}
}

func TestSummarizeDownstream(t *testing.T) {
	seedID := "wl-1"
	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeWorkload, "checkout", "prod", "shop"),
		newImpactTestNode("db-1", NodeTypeDatabase, "orders-db", "prod", ""),
		newImpactTestNode("svc-1", NodeTypeService, "payments", "prod", "shop"),
		newImpactTestNode("ns-1", NodeTypeNamespace, "shop", "", ""), // not a dependency type
	}
	depth := map[string]int{seedID: 0, "db-1": 1, "svc-1": 1, "ns-1": 1}
	edges := []*DbEdge{
		{SourceNodeID: seedID, DestinationNodeID: "db-1", RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "ebpf"}}},
		{SourceNodeID: seedID, DestinationNodeID: "svc-1", RelationshipType: RelationshipCalls,
			ContributingSources: []EdgeContributingSource{{Source: "traces"}}},
	}

	got := summarizeDownstream(seedID, nodes, edges, depth)

	if len(got) != 2 {
		t.Fatalf("expected 2 downstream dependencies (namespace + seed excluded), got %d: %+v", len(got), got)
	}
	// Sorted by (hops, name): orders-db before payments.
	if got[0].Name != "orders-db" || got[0].NodeType != NodeTypeDatabase || got[0].Relationship != RelationshipCalls {
		t.Errorf("first = %+v, want orders-db Database CALLS", got[0])
	}
	if got[1].Name != "payments" || len(got[1].Sources) != 1 || got[1].Sources[0] != "traces" {
		t.Errorf("second = %+v, want payments with sources [traces]", got[1])
	}
}

func TestDownstreamRelationshipStrings(t *testing.T) {
	wl := downstreamRelationshipStrings(NodeTypeWorkload)
	if len(wl) != 3 || wl[0] != string(RelationshipCalls) {
		t.Errorf("Workload downstream = %v, want [CALLS PUBLISHES_TO SUBSCRIBES_TO]", wl)
	}
	if got := downstreamRelationshipStrings(NodeTypeDatabase); len(got) != 0 {
		t.Errorf("Database gets no downstream pass, got %v", got)
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
