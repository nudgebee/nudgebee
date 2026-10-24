package core

import (
	"strings"
	"testing"
)

// TestExcludeFilterKey verifies that the key currently being edited is stripped
// from the matching filter type (so the value dropdown offers all alternatives),
// while filters of the other type and the original struct are left untouched.
func TestExcludeFilterKey(t *testing.T) {
	t.Run("nil filters", func(t *testing.T) {
		if got := excludeFilterKey(nil, "label", "env"); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("strips matching label key, keeps attributes", func(t *testing.T) {
		in := &GraphFilters{
			Labels:        map[string]string{"env": "prod", "team": "core"},
			LabelKeys:     []string{"env", "region"},
			Attributes:    map[string]string{"env": "staging"},
			AttributeKeys: []string{"env"},
		}
		got := excludeFilterKey(in, "label", "env")

		if _, ok := got.Labels["env"]; ok {
			t.Errorf("label key 'env' should be stripped, got %+v", got.Labels)
		}
		if got.Labels["team"] != "core" {
			t.Errorf("unrelated label 'team' should be preserved, got %+v", got.Labels)
		}
		for _, k := range got.LabelKeys {
			if k == "env" {
				t.Errorf("'env' should be removed from LabelKeys, got %+v", got.LabelKeys)
			}
		}
		// Attribute side must be untouched when editing a label.
		if got.Attributes["env"] != "staging" || len(got.AttributeKeys) != 1 {
			t.Errorf("attribute filters should be untouched, got %+v / %+v", got.Attributes, got.AttributeKeys)
		}
		// Original must not be mutated.
		if _, ok := in.Labels["env"]; !ok {
			t.Errorf("original filters were mutated: %+v", in.Labels)
		}
	})

	t.Run("strips matching attribute key", func(t *testing.T) {
		in := &GraphFilters{
			Attributes:    map[string]string{"namespace": "default", "cluster": "prod"},
			AttributeKeys: []string{"namespace"},
			Labels:        map[string]string{"namespace": "ignored"},
		}
		got := excludeFilterKey(in, "attribute", "namespace")

		if _, ok := got.Attributes["namespace"]; ok {
			t.Errorf("attribute key 'namespace' should be stripped, got %+v", got.Attributes)
		}
		if got.Attributes["cluster"] != "prod" {
			t.Errorf("unrelated attribute 'cluster' should be preserved, got %+v", got.Attributes)
		}
		// Label side must be untouched when editing an attribute.
		if got.Labels["namespace"] != "ignored" {
			t.Errorf("label filters should be untouched, got %+v", got.Labels)
		}
	})
}

// TestBuildNodeFilterSQL_ValueScoping confirms the helper used by GetFilterValues
// produces an "AND"-prefixed clause with placeholders starting at the supplied
// counter, and stays empty when no filters are active.
func TestBuildNodeFilterSQL_ValueScoping(t *testing.T) {
	t.Run("empty when no filters", func(t *testing.T) {
		sql, args, err := buildNodeFilterSQL(nil, nil, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sql != "" || len(args) != 0 {
			t.Fatalf("expected empty clause, got sql=%q args=%v", sql, args)
		}
	})

	t.Run("scopes by account and node type starting at $3", func(t *testing.T) {
		filters := &GraphFilters{
			AccountIDs: []string{"123456789012"},
			NodeTypes:  []NodeType{NodeTypeWorkload},
		}
		sql, args, err := buildNodeFilterSQL(filters, nil, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(sql, " AND ") {
			t.Errorf("clause should be AND-prefixed for appending, got %q", sql)
		}
		if !strings.Contains(sql, "cloud_account_id IN ($3)") {
			t.Errorf("expected account placeholder at $3, got %q", sql)
		}
		if !strings.Contains(sql, "node_type = $4") {
			t.Errorf("expected node_type placeholder at $4, got %q", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d (%v)", len(args), args)
		}
	})

	t.Run("ORs node_types with specific_types", func(t *testing.T) {
		filters := &GraphFilters{
			NodeTypes:     []NodeType{NodeTypeDatabase},
			SpecificTypes: []string{"EC2Instance"},
		}
		sql, args, err := buildNodeFilterSQL(filters, nil, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(sql, "(node_type = $3 OR specific_type = ANY($4::text[]))") {
			t.Errorf("expected node_type OR specific_type, got %q", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d (%v)", len(args), args)
		}
	})
}

// TestNodeTypeFilter_UnionOfGroups covers #41502: the Node Type multi-select sends a fully
// selected group as node_types and a partly selected one as specific_types, and the user
// means "any of these", so a node in either list must match.
func TestNodeTypeFilter_UnionOfGroups(t *testing.T) {
	ec2 := &DbNode{NodeType: NodeTypeComputeInstance, SpecificType: "EC2Instance"}
	gce := &DbNode{NodeType: NodeTypeComputeInstance, SpecificType: "GCEInstance"}
	rds := &DbNode{NodeType: NodeTypeDatabase, SpecificType: "RDSInstance"}
	cloudSQL := &DbNode{NodeType: NodeTypeDatabase, SpecificType: "CloudSQLInstance"}
	// A node whose specific_type was never backfilled must still match its whole group.
	unbackfilledDB := &DbNode{NodeType: NodeTypeDatabase}
	all := []*DbNode{ec2, gce, rds, cloudSQL, unbackfilledDB}

	cases := []struct {
		name          string
		nodeTypes     []NodeType
		specificTypes []string
		want          []*DbNode
	}{
		{"partial group plus full group", []NodeType{NodeTypeDatabase}, []string{"EC2Instance"}, []*DbNode{ec2, rds, cloudSQL, unbackfilledDB}},
		{"two partial groups", nil, []string{"EC2Instance", "RDSInstance"}, []*DbNode{ec2, rds}},
		{"single full group", []NodeType{NodeTypeDatabase}, nil, []*DbNode{rds, cloudSQL, unbackfilledDB}},
		{"single partial group", nil, []string{"EC2Instance"}, []*DbNode{ec2}},
		{"no type filter", nil, nil, all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []*DbNode
			for _, n := range all {
				if matchesTypeFilter(n, tc.nodeTypes, tc.specificTypes) {
					got = append(got, n)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d nodes, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("node %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
