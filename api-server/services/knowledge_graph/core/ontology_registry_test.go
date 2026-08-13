package core

import (
	"testing"
)

// TestOntologyFieldsAreCanonical enforces the cross-cloud consistency guarantee:
// every OntologyField declared by any specific_type spec must belong to its
// NodeType's canonical vocabulary. This keeps each concept's ontology_attributes
// schema uniform regardless of source cloud, and catches typos / rogue keys.
func TestOntologyFieldsAreCanonical(t *testing.T) {
	for _, specificType := range RegisteredSpecificTypes() {
		spec, _ := LookupOntologySpec(specificType)
		if _, ok := canonicalOntologyVocab[spec.NodeType]; !ok {
			t.Errorf("specific_type %q maps to NodeType %q which has no canonicalOntologyVocab entry", specificType, spec.NodeType)
			continue
		}
		seen := map[string]bool{}
		for _, f := range spec.Fields {
			if f.OntologyField == "" {
				t.Errorf("specific_type %q has an empty OntologyField name", specificType)
			}
			if seen[f.OntologyField] {
				t.Errorf("specific_type %q declares duplicate ontology field %q", specificType, f.OntologyField)
			}
			seen[f.OntologyField] = true
			if !isCanonicalOntologyField(spec.NodeType, f.OntologyField) {
				t.Errorf("specific_type %q (NodeType %q): ontology field %q is not in canonicalOntologyVocab %v",
					specificType, spec.NodeType, f.OntologyField, canonicalOntologyVocab[spec.NodeType])
			}
		}
	}
}

// TestBuildOntologyAttributes locks the special-handling behaviour of the engine.
func TestBuildOntologyAttributes(t *testing.T) {
	// EC2Instance is registered in ontology_data_aws.go.
	props := map[string]interface{}{
		"name":           "web-01",
		"region":         "us-east-1",
		"instance_type":  "t3.large",
		"instance_state": "running",
		"public_ip":      "1.2.3.4",
		"private_ip":     "10.0.0.5",
	}
	got := BuildOntologyAttributes("EC2Instance", NodeTypeComputeInstance, props)
	want := map[string]interface{}{
		"name":               "web-01",
		"region":             "us-east-1",
		"type":               "t3.large",
		"state":              "running",
		"public_ip_address":  "1.2.3.4",
		"private_ip_address": "10.0.0.5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("EC2Instance ontology[%q] = %v, want %v", k, got[k], v)
		}
	}
	// Empty-string / missing source fields must not appear.
	got2 := BuildOntologyAttributes("EC2Instance", NodeTypeComputeInstance, map[string]interface{}{"name": "x", "public_ip": ""})
	if _, present := got2["public_ip_address"]; present {
		t.Errorf("empty public_ip should not populate public_ip_address, got %v", got2)
	}

	// Unregistered specific_type with unmapped NodeType → empty (non-nil) map.
	got3 := BuildOntologyAttributes("TotallyUnknown", NodeType("NoSuchType"), props)
	if got3 == nil || len(got3) != 0 {
		t.Errorf("unregistered spec should return empty non-nil map, got %v", got3)
	}
}

// TestOntologySpecialHandling exercises the transform helpers.
func TestOntologySpecialHandling(t *testing.T) {
	props := map[string]interface{}{
		"enc_raw":   "AES256",
		"vers":      "Enabled",
		"flag_true": true,
	}
	cases := []struct {
		name  string
		field OntologyField
		want  interface{}
		found bool
	}{
		{"static", OntologyField{OntologyField: "type", Special: "static_value", Extra: map[string]any{"value": "bigtable"}}, "bigtable", true},
		{"to_boolean_set", OntologyField{OntologyField: "encrypted", NodeField: "enc_raw", Special: "to_boolean"}, true, true},
		{"to_boolean_unset", OntologyField{OntologyField: "encrypted", NodeField: "missing", Special: "to_boolean"}, false, true},
		{"equal_boolean_hit", OntologyField{OntologyField: "versioning", NodeField: "vers", Special: "equal_boolean", Extra: map[string]any{"values": []string{"Enabled"}}}, true, true},
		{"invert", OntologyField{OntologyField: "public", NodeField: "flag_true", Special: "invert_boolean"}, false, true},
		{"coalesce", OntologyField{OntologyField: "endpoint", NodeField: "missing", Special: "coalesce", Extra: map[string]any{"fields": []string{"enc_raw"}}}, "AES256", true},
	}
	for _, c := range cases {
		got, found := applyOntologyField(c.field, props)
		if found != c.found || got != c.want {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", c.name, got, found, c.want, c.found)
		}
	}
}
