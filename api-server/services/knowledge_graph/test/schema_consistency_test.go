package test

// Cross-schema consistency guard. Runs in package test, which imports every source
// package (aws/gcp/azure/k8s), so both registries are fully populated here:
//   - the concrete per-specific_type property schema (core.SpecificTypeSchema,
//     declared co-located in sources/<cloud>/*.go) — drives query_attributes.
//   - the cross-cloud ontology mapping (core.OntologyNodeSpec, in
//     core/ontology_data_*.go) — drives ontology_attributes.
//
// Both read the SAME node.Properties by key. This test proves they agree: every
// source property key the ontology mapping reads for a specific_type must be a
// property the concrete schema declares (or a universal base key). A mismatch
// means one side has a typo'd/renamed key the other doesn't know about.

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestSchemaOntologyConsistency(t *testing.T) {
	for _, label := range core.RegisteredSpecificTypeSchemas() {
		ont, ok := core.LookupOntologySpec(label)
		if !ok {
			// A concrete schema without an ontology spec is allowed (coverage of the
			// reverse direction is enforced per-cloud). Nothing to cross-check.
			continue
		}
		schema, _ := core.LookupSpecificTypeSchema(label)

		declared := make(map[string]bool)
		for _, name := range schema.PropertyNames() {
			declared[name] = true
		}

		isKnown := func(key string) bool {
			return key == "" || declared[key] || core.IsUniversalBaseProperty(key)
		}

		for _, f := range ont.Fields {
			// static_value fields synthesize a constant and read no source key.
			if f.Special == "static_value" {
				continue
			}
			if !isKnown(f.NodeField) {
				t.Errorf("specific_type %q: the ontology mapping reads properties[%q] but the concrete schema does not declare it — add core.PropertyDef{Name: %q} to the schema in its sources/<cloud>/*.go file",
					label, f.NodeField, f.NodeField)
			}
			// coalesce fields fall back to Extra["fields"]; those are source keys too.
			for _, alt := range extraFieldNames(f.Extra["fields"]) {
				if !isKnown(alt) {
					t.Errorf("specific_type %q: ontology coalesce fallback reads properties[%q], not declared in the concrete schema — add core.PropertyDef{Name: %q}",
						label, alt, alt)
				}
			}
		}
	}
}

// extraFieldNames coerces an ontology Extra["fields"] list ([]string or []any)
// to []string, mirroring core's internal extraStrings helper.
func extraFieldNames(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
