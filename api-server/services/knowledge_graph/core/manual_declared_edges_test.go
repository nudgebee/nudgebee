package core

import "testing"

// A manual edge of a type only users declare never displaces another source's
// edge on the same node pair, in either order (the dedup keys on the pair, not
// the relationship), and never ties with an unlisted source.
func TestDeduplicateEdgesWithPriority_ManualDeclaredLosesEverywhere(t *testing.T) {
	edge := func(source string, rt RelationshipType) *DbEdge {
		return &DbEdge{SourceNodeID: "a", DestinationNodeID: "b", TenantID: "t", Source: source, RelationshipType: rt, Properties: map[string]interface{}{}}
	}
	cases := []struct {
		name      string
		collector *DbEdge
		manual    *DbEdge
	}{
		{"k8s RUNS_ON vs manual RUNS_ON", edge("k8s", RelationshipRunsOn), edge("manual", RelationshipRunsOn)},
		{"traces CALLS vs manual DEPENDS_ON", edge("traces", RelationshipCalls), edge("manual", RelationshipDependsOn)},
		{"vpc-flow CALLS vs manual CONNECTED_TO", edge("aws-vpc-flow", RelationshipCalls), edge("manual", RelationshipConnectedTo)},
		{"unlisted source vs manual BELONGS_TO", edge("some-enricher", RelationshipBelongsTo), edge("manual", RelationshipBelongsTo)},
	}
	for _, c := range cases {
		for _, order := range [][]*DbEdge{{c.collector, c.manual}, {c.manual, c.collector}} {
			got := DeduplicateEdgesWithPriority([]*DbEdge{cloneEdge(order[0]), cloneEdge(order[1])})
			if len(got) != 1 || got[0].Source != c.collector.Source || got[0].RelationshipType != c.collector.RelationshipType {
				t.Errorf("%s (manual first: %v): kept %s %s", c.name, order[0].Source == "manual", got[0].Source, got[0].RelationshipType)
			}
		}
	}
	if GetEdgeSourcePriority("manual", RelationshipCalls) != EdgePriority2 {
		t.Error("manual CALLS priority changed")
	}
}

func cloneEdge(e *DbEdge) *DbEdge {
	c := *e
	c.Properties = map[string]interface{}{}
	return &c
}

// CONNECTED_TO has no direction: an upstream walk reaches across it from
// either end, and the impact helpers attribute it either way round.
func TestUndirectedEdgesInLayeredWalks(t *testing.T) {
	// seed (depth 0) is the EC2; the switch at depth 1 links to it with
	// switch → EC2 or EC2 → switch.
	depths := map[string]int{"ec2": 0, "sw": 1}
	forward := &DbEdge{ID: "f", SourceNodeID: "sw", DestinationNodeID: "ec2", RelationshipType: RelationshipConnectedTo}
	reverse := &DbEdge{ID: "r", SourceNodeID: "ec2", DestinationNodeID: "sw", RelationshipType: RelationshipConnectedTo}
	directed := &DbEdge{ID: "d", SourceNodeID: "ec2", DestinationNodeID: "sw", RelationshipType: RelationshipCalls}

	for _, e := range []*DbEdge{forward, reverse} {
		node, toward, ok := orientTowardSeed(e, TraverseDirectionUpstream, depths)
		if !ok || node != "sw" || toward != "ec2" {
			t.Errorf("edge %s: orient = %s, %s, %v", e.ID, node, toward, ok)
		}
	}
	if _, _, ok := orientTowardSeed(directed, TraverseDirectionUpstream, depths); ok {
		t.Error("a directed edge pointing away from the seed was walked upstream")
	}

	kept := filterLayeredEdges([]*DbEdge{forward, reverse, directed}, depths, TraverseDirectionUpstream)
	ids := map[string]bool{}
	for _, e := range kept {
		ids[e.ID] = true
	}
	if !ids["f"] || !ids["r"] || ids["d"] {
		t.Errorf("upstream layered edges = %v, want f and r only", ids)
	}
}
