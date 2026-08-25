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
}
