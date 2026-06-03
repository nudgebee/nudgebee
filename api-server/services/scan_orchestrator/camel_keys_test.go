package scan_orchestrator

import (
	"reflect"
	"testing"
)

func TestSnakeToCamel(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"name":                "name",
		"claim_ref":           "claimRef",
		"creation_timestamp":  "creationTimestamp",
		"resource_version":    "resourceVersion",
		"node_selector_terms": "nodeSelectorTerms",
		"already_camelCase":   "alreadyCamelCase", // edge: existing case preserved on suffix
		"trailing_":           "trailing",
		"__leading":           "Leading",
	}
	for in, want := range cases {
		if got := snakeToCamel(in); got != want {
			t.Errorf("snakeToCamel(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCamelKeysDeep(t *testing.T) {
	in := map[string]any{
		"metadata": map[string]any{
			"name":               "pvc-1",
			"creation_timestamp": "2026-01-01T00:00:00Z",
		},
		"spec": map[string]any{
			"capacity": map[string]any{"storage": "10Gi"},
			"claim_ref": map[string]any{
				"namespace": "default",
				"name":      "my-pvc",
			},
			"access_modes":     []any{"ReadWriteOnce"},
			"node_affinity":    nil,
			"storage_class_id": 42,
		},
	}
	want := map[string]any{
		"metadata": map[string]any{
			"name":              "pvc-1",
			"creationTimestamp": "2026-01-01T00:00:00Z",
		},
		"spec": map[string]any{
			"capacity": map[string]any{"storage": "10Gi"},
			"claimRef": map[string]any{
				"namespace": "default",
				"name":      "my-pvc",
			},
			"accessModes":    []any{"ReadWriteOnce"},
			"nodeAffinity":   nil,
			"storageClassId": 42,
		},
	}
	got := camelKeysDeep(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("camelKeysDeep mismatch\n got:  %#v\n want: %#v", got, want)
	}
}
