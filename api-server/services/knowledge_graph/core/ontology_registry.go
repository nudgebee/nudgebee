package core

import (
	"fmt"
	"sort"
)

// Ontology registry — the cross-cloud ontology field mapping. Each abstract
// NodeType (the ontology) has a fixed, cross-cloud-normalized field set; each
// concrete `specific_type` (e.g. EC2Instance) declares how to populate that
// field set from its source's raw properties. The result is written to
// DbNode.OntologyAttributes so that, e.g., every ComputeInstance exposes the
// same keys (name/region/type/state/...) regardless of the cloud it came from.

// OntologyField maps one canonical ontology field to a source property.
type OntologyField struct {
	// OntologyField is the canonical key written to OntologyAttributes.
	OntologyField string
	// NodeField is the key read from node.Properties (source-specific). Ignored
	// when Special == "static_value".
	NodeField string
	// Required marks a field as identity-defining for the concept (advisory —
	// enforced by the consistency test, not at build time).
	Required bool
	// Special selects a transform: "" (direct copy), "static_value", "coalesce",
	// "to_boolean", "equal_boolean", "invert_boolean", "mapping".
	Special string
	// Extra carries parameters for Special (e.g. {"value": ...}, {"fields": [...]},
	// {"values": [...]}, {"map": {...}}).
	Extra map[string]any
}

// OntologyNodeSpec is the field schema for one concrete specific_type, grouped
// under the ontology NodeType it rolls up to.
type OntologyNodeSpec struct {
	NodeType NodeType
	Fields   []OntologyField
}

// ontologyRegistry is keyed by specific_type (the concrete label). Data files
// (ontology_data_*.go) register into it via registerOntology in their init().
var ontologyRegistry = map[string]OntologyNodeSpec{}

// registerOntology merges a batch of specs into the registry. Data files call
// this from init(); duplicate specific_type keys panic to catch collisions.
func registerOntology(specs map[string]OntologyNodeSpec) {
	for specificType, spec := range specs {
		if _, dup := ontologyRegistry[specificType]; dup {
			panic(fmt.Sprintf("ontology registry: duplicate specific_type %q", specificType))
		}
		ontologyRegistry[specificType] = spec
	}
}

// BuildOntologyAttributes produces the normalized ontology field map for a node.
// It looks up the spec by specific_type, falling back to a bare NodeType-name
// spec for defaulted/generic nodes. Returns an empty (non-nil) map when no spec
// exists so the column is always a valid JSONB object.
func BuildOntologyAttributes(specificType string, nodeType NodeType, properties map[string]interface{}) map[string]interface{} {
	spec, ok := ontologyRegistry[specificType]
	if !ok {
		// Fallback: a NodeType-keyed spec for nodes whose specific_type defaulted
		// to the ontology name (flow-source ExternalService, unmapped resources).
		spec, ok = ontologyRegistry[string(nodeType)]
	}
	out := make(map[string]interface{})
	if !ok {
		return out
	}
	for _, f := range spec.Fields {
		if v, present := applyOntologyField(f, properties); present {
			out[f.OntologyField] = v
		}
	}
	return out
}

// applyOntologyField resolves a single field's value from properties, applying
// its Special transform. Returns (value, true) only when a value is produced.
func applyOntologyField(f OntologyField, props map[string]interface{}) (interface{}, bool) {
	switch f.Special {
	case "static_value":
		v, ok := f.Extra["value"]
		return v, ok
	case "coalesce":
		return ontologyCoalesce(f, props)
	case "to_boolean":
		_, ok := nonEmpty(props[f.NodeField])
		return ok, true
	case "invert_boolean":
		if b, ok := props[f.NodeField].(bool); ok {
			return !b, true
		}
		return nil, false
	case "equal_boolean":
		return ontologyEqualBoolean(f, props)
	case "mapping":
		return ontologyMapping(f, props)
	default: // direct copy
		return nonEmpty(props[f.NodeField])
	}
}

// ontologyCoalesce returns the first non-empty value among NodeField then Extra["fields"].
func ontologyCoalesce(f OntologyField, props map[string]interface{}) (interface{}, bool) {
	if v, ok := nonEmpty(props[f.NodeField]); ok {
		return v, true
	}
	for _, alt := range extraStrings(f.Extra["fields"]) {
		if v, ok := nonEmpty(props[alt]); ok {
			return v, true
		}
	}
	return nil, false
}

// ontologyEqualBoolean reports whether the raw value matches one of Extra["values"].
func ontologyEqualBoolean(f OntologyField, props map[string]interface{}) (interface{}, bool) {
	raw, present := props[f.NodeField]
	if !present {
		return nil, false
	}
	rawStr := fmt.Sprintf("%v", raw)
	for _, w := range extraStrings(f.Extra["values"]) {
		if rawStr == w {
			return true, true
		}
	}
	return false, true
}

// ontologyMapping normalizes a provider-specific value via Extra["map"]; unmapped
// values drop out.
func ontologyMapping(f OntologyField, props map[string]interface{}) (interface{}, bool) {
	raw, present := props[f.NodeField]
	if !present {
		return nil, false
	}
	m, _ := f.Extra["map"].(map[string]string)
	if mapped, ok := m[fmt.Sprintf("%v", raw)]; ok {
		return mapped, true
	}
	return nil, false
}

// nonEmpty reports whether v is a usable (non-nil, non-empty-string) value.
func nonEmpty(v interface{}) (interface{}, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.(string); ok && s == "" {
		return nil, false
	}
	return v, true
}

// extraStrings coerces an Extra list ([]string or []any) to []string.
func extraStrings(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	}
	return nil
}

// CanonicalOntologyFields returns the sorted union of ontology field names
// declared across every specific_type that rolls up to nodeType. Used by the
// consistency guardrail test and for documentation.
func CanonicalOntologyFields(nodeType NodeType) []string {
	set := map[string]struct{}{}
	for _, spec := range ontologyRegistry {
		if spec.NodeType != nodeType {
			continue
		}
		for _, f := range spec.Fields {
			set[f.OntologyField] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// OntologyFieldKeys returns the sorted ontology field names for one spec.
func (s OntologyNodeSpec) OntologyFieldKeys() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.OntologyField)
	}
	sort.Strings(out)
	return out
}

// RegisteredSpecificTypes returns all registered specific_type keys, sorted.
// Exposed for guardrail tests.
func RegisteredSpecificTypes() []string {
	out := make([]string, 0, len(ontologyRegistry))
	for k := range ontologyRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LookupOntologySpec returns the spec for a specific_type, if registered.
func LookupOntologySpec(specificType string) (OntologyNodeSpec, bool) {
	s, ok := ontologyRegistry[specificType]
	return s, ok
}
