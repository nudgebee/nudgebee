package flow_sources

import (
	"log/slog"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestValidateCustomKindName(t *testing.T) {
	for _, ok := range []string{"NetworkSwitch", "Plc", "MainframeJob7"} {
		if err := ValidateCustomKindName(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "N", "networkSwitch", "Network Switch", "Network-Switch", "Network_Switch", "Database", "Pod", strings.Repeat("A", 65)} {
		if err := ValidateCustomKindName(bad); err == nil {
			t.Errorf("%q accepted as a custom kind", bad)
		}
	}
}

func TestManualNode_IsCustomKind(t *testing.T) {
	cases := []struct {
		name string
		n    ManualNode
		want bool
	}{
		{"custom kind outside an account", ManualNode{NodeType: "CloudResource", SpecificType: "NetworkSwitch"}, true},
		{"built-in type outside an account", ManualNode{NodeType: "Database", SpecificType: "Database"}, false},
		{"no specific type", ManualNode{NodeType: "Database"}, false},
		{"cloud declaration", ManualNode{CloudAccountID: "acct", NodeType: "Database", SpecificType: "RDSInstance"}, false},
	}
	for _, c := range cases {
		if got := c.n.IsCustomKind(); got != c.want {
			t.Errorf("%s: IsCustomKind = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateCustomKindProperties(t *testing.T) {
	good := map[string]interface{}{
		"site": "blr-1", "port_count": float64(48), "managed": true,
		"vlans": []interface{}{"10", "20"}, "owner": map[string]interface{}{"team": "netops"},
	}
	if err := validateCustomKindProperties(good); err != nil {
		t.Fatalf("valid properties refused: %v", err)
	}
	tooMany := map[string]interface{}{}
	for i := 0; i <= maxCustomKindProperties; i++ {
		tooMany[strings.Repeat("k", i+1)] = "v"
	}
	for name, props := range map[string]map[string]interface{}{
		"identity key":           {"name": "x"},
		"region is identity":     {"region": "us-east-1"},
		"endpoint index key":     {"dns_name": "sw.corp.internal"},
		"endpoint list key":      {"dns_aliases": []interface{}{"a"}},
		"list of objects":        {"ports": []interface{}{map[string]interface{}{"n": "1"}}},
		"map of non-text values": {"owner": map[string]interface{}{"count": float64(1)}},
		"empty key":              {" ": "x"},
		"too many":               tooMany,
	} {
		if err := validateCustomKindProperties(props); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateManualNodeDetails_CustomKindTakesFreeProperties(t *testing.T) {
	n := &ManualNode{NodeType: "CloudResource", SpecificType: "NetworkSwitch", Properties: map[string]interface{}{"site": "blr-1"}}
	if err := validateManualNodeDetails(n); err != nil {
		t.Fatalf("custom kind property refused: %v", err)
	}
	// The same property on a built-in type outside an account is not in its list.
	n = &ManualNode{NodeType: "Database", SpecificType: "Database", Properties: map[string]interface{}{"site": "blr-1"}}
	if err := validateManualNodeDetails(n); err == nil {
		t.Fatal("built-in type accepted a property outside its list")
	}
}

func TestBoundNodeFor_QualifiesTenantScopedNodesByTenant(t *testing.T) {
	outside := &ManualNode{TenantID: testTenant, NodeType: "CloudResource", Name: "sw-core-01"}
	if got := boundNodeFor(outside, "node-1"); got.accountID != testTenant {
		t.Errorf("tenant-scoped ref stored with account %q, want the tenant", got.accountID)
	}
	inAccount := &ManualNode{TenantID: testTenant, CloudAccountID: "acct-1", NodeType: "Database", Name: "orders"}
	if got := boundNodeFor(inAccount, "node-2"); got.accountID != "acct-1" {
		t.Errorf("account ref stored with account %q, want acct-1", got.accountID)
	}
}

func TestClaimPair(t *testing.T) {
	edge := func() *core.DbEdge { return &core.DbEdge{SourceNodeID: "a", DestinationNodeID: "b"} }
	row := func(id int64, rt string) manualDependencyRow {
		return manualDependencyRow{ID: id, RelationshipType: rt}
	}

	t.Run("later declared-only row on a linked pair is refused", func(t *testing.T) {
		linked := map[string]manualDependencyRow{}
		u1, u2 := manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}, manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}
		if claimPair(linked, row(1, "CONNECTED_TO"), &u1, edge()) == nil {
			t.Fatal("first row's edge dropped")
		}
		if claimPair(linked, row(2, "CALLS"), &u2, edge()) != nil {
			t.Fatal("second row on the pair still emitted")
		}
		if u2.ResolutionStatus != ManualResolutionInvalidPayload || !strings.Contains(u2.ResolutionError, "declaration 1") {
			t.Errorf("second row: status %q error %q", u2.ResolutionStatus, u2.ResolutionError)
		}
	})

	t.Run("the same declared-only link twice draws one edge, not a conflict", func(t *testing.T) {
		linked := map[string]manualDependencyRow{}
		u1, u2 := manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}, manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}
		claimPair(linked, row(1, "CONNECTED_TO"), &u1, edge())
		if claimPair(linked, row(2, "CONNECTED_TO"), &u2, edge()) == nil || u2.ResolutionStatus != ManualResolutionResolved {
			t.Error("a second CONNECTED_TO on the same pair and direction was refused")
		}
	})

	t.Run("rows that are all CALLS / PUBLISHES_TO keep sharing a pair", func(t *testing.T) {
		linked := map[string]manualDependencyRow{}
		u1, u2 := manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}, manualDependencyUpdate{ResolutionStatus: ManualResolutionResolved}
		claimPair(linked, row(1, "CALLS"), &u1, edge())
		if claimPair(linked, row(2, "PUBLISHES_TO"), &u2, edge()) == nil || u2.ResolutionStatus != ManualResolutionResolved {
			t.Error("existing behaviour changed for CALLS + PUBLISHES_TO on one pair")
		}
	})

	reversed := func() *core.DbEdge { return &core.DbEdge{SourceNodeID: "b", DestinationNodeID: "a"} }

	t.Run("an undirected link declared both ways is one link", func(t *testing.T) {
		linked := map[string]manualDependencyRow{}
		u1, u2 := manualDependencyUpdate{}, manualDependencyUpdate{}
		claimPair(linked, row(1, "CONNECTED_TO"), &u1, edge())
		if claimPair(linked, row(2, "CONNECTED_TO"), &u2, reversed()) != nil || u2.ResolutionStatus != ManualResolutionInvalidPayload {
			t.Error("b CONNECTED_TO a emitted although a CONNECTED_TO b is linked")
		}
	})

	t.Run("the reverse direction of a directed link is another pair", func(t *testing.T) {
		linked := map[string]manualDependencyRow{}
		u1, u2 := manualDependencyUpdate{}, manualDependencyUpdate{}
		claimPair(linked, row(1, "RUNS_ON"), &u1, edge())
		if claimPair(linked, row(2, "DEPENDS_ON"), &u2, reversed()) == nil {
			t.Error("b DEPENDS_ON a refused because a RUNS_ON b is linked")
		}
	})
}

// processRow judges a declared-only relationship against the build's nodes:
// a row whose both ends are discovered resources emits nothing and says why.
func TestProcessRow_DeclaredOnlyRelationshipNeedsDeclaredNode(t *testing.T) {
	s := NewManualFlowSource(slog.Default())
	switchNode := &core.DbNode{ID: "sw", Source: core.ManualNodeSource, CloudAccountID: testTenant}
	ec2 := &core.DbNode{ID: "ec2", Source: "aws", CloudAccountID: "acct-1"}
	pod := &core.DbNode{ID: "pod", Source: "k8s", CloudAccountID: "acct-2"}
	index := map[string]*core.DbNode{"sw": switchNode, "ec2": ec2, "pod": pod}
	resolved := func(src, dst, rt string) manualDependencyRow {
		return manualDependencyRow{ID: 7, RelationshipType: rt, ResolutionStatus: ManualResolutionResolved,
			ResolvedSourceNodeID: src, ResolvedDestNodeID: dst}
	}

	update, edge, err := s.processRow(t.Context(), nil, index, resolved("sw", "ec2", "CONNECTED_TO"), testTenant)
	if err != nil || edge == nil || update.ResolutionStatus != ManualResolutionResolved {
		t.Fatalf("switch → EC2: edge %v status %q err %v", edge, update.ResolutionStatus, err)
	}
	if edge.Properties["source_priority"] != int(EdgePriorityManualDeclared) {
		t.Errorf("source_priority = %v, want %d", edge.Properties["source_priority"], EdgePriorityManualDeclared)
	}

	update, edge, err = s.processRow(t.Context(), nil, index, resolved("pod", "ec2", "RUNS_ON"), testTenant)
	if err != nil || edge != nil || update.ResolutionStatus != ManualResolutionInvalidPayload {
		t.Fatalf("pod → EC2 RUNS_ON: edge %v status %q err %v", edge, update.ResolutionStatus, err)
	}

	// CALLS between two discovered resources is unchanged.
	if _, edge, _ = s.processRow(t.Context(), nil, index, resolved("pod", "ec2", "CALLS"), testTenant); edge == nil {
		t.Error("pod → EC2 CALLS no longer emitted")
	}
}
