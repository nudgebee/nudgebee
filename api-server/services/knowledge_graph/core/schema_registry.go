package core

import (
	"fmt"
	"sort"
)

// Concrete-type schema registry — a fixed, declared per-specific_type
// concrete-property schema. Each concrete `specific_type` (e.g. EC2Instance)
// declares a FIXED property schema: the set of keys a node of that type carries
// in DbNode.Properties, which of them are hoisted into query_attributes for fast
// filtering (Indexed), and which are identity-defining (Required).
//
// This is the sibling of the ontology registry (ontology_registry.go): that one
// declares the cross-cloud-normalized field set per NodeType; this one declares
// the concrete, native property set per specific_type. The two are kept
// deliberately separate: the concrete per-resource property schema is independent
// of the cross-cloud ontology mapping.
//
// Schemas are declared CO-LOCATED with their source module (sources/<cloud>/<x>.go)
// and registered via RegisterSpecificTypeSchema in that file's init(). The engine
// side lives here in package core so that NewNode/ExtractQueryAttributes can read
// the registry without importing the source packages (which would be a cycle).

// PropertyDef declares one concrete property of a specific_type — a source-key →
// node-field mapping. Name is the key as written into node.Properties by the
// source's extractor (snake_case, Nudgebee convention, so it matches the value
// the extractor actually stores).
type PropertyDef struct {
	// Name is the node.Properties key this field maps to.
	Name string
	// Indexed hoists the field into query_attributes for fast SQL filtering.
	Indexed bool
	// Required marks an identity/always-present field, enforced by the conformance
	// test (real nodes of this type must carry it).
	Required bool
}

// SpecificTypeSchema is the fixed concrete-property schema for one specific_type,
// grouped under the ontology NodeType it rolls up to.
type SpecificTypeSchema struct {
	SpecificType string
	NodeType     NodeType
	Properties   []PropertyDef
}

// specificTypeSchemas is keyed by specific_type (the concrete label). Source
// modules register into it via RegisterSpecificTypeSchema in their init().
var specificTypeSchemas = map[string]SpecificTypeSchema{}

// RegisterSpecificTypeSchema records a schema. Source modules call this from
// init(); an empty label or a duplicate specific_type panics to catch mistakes.
func RegisterSpecificTypeSchema(s SpecificTypeSchema) {
	if s.SpecificType == "" {
		panic("schema registry: SpecificTypeSchema with empty SpecificType")
	}
	if _, dup := specificTypeSchemas[s.SpecificType]; dup {
		panic(fmt.Sprintf("schema registry: duplicate specific_type %q", s.SpecificType))
	}
	specificTypeSchemas[s.SpecificType] = s
}

// LookupSpecificTypeSchema returns the schema for a specific_type, if registered.
func LookupSpecificTypeSchema(specificType string) (SpecificTypeSchema, bool) {
	s, ok := specificTypeSchemas[specificType]
	return s, ok
}

// RegisteredSpecificTypeSchemas returns all registered specific_type keys, sorted.
// Exposed for the coverage guardrail test.
func RegisteredSpecificTypeSchemas() []string {
	out := make([]string, 0, len(specificTypeSchemas))
	for k := range specificTypeSchemas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IndexedFields returns the sorted names of the schema's Indexed properties —
// the keys hoisted into query_attributes.
func (s SpecificTypeSchema) IndexedFields() []string {
	out := make([]string, 0, len(s.Properties))
	for _, p := range s.Properties {
		if p.Indexed {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

// PropertyNames returns the sorted names of every declared property. Used by the
// conformance and schema↔ontology consistency tests.
func (s SpecificTypeSchema) PropertyNames() []string {
	out := make([]string, 0, len(s.Properties))
	for _, p := range s.Properties {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// universalBaseProperties are the keys stamped on EVERY node by the common node
// builders (createNodeFromResource + NewNode) regardless of resource type.
// Per-type schemas declare only their resource-specific fields; the conformance
// test treats these base keys as implicitly declared so they don't have to be
// repeated in every schema.
var universalBaseProperties = map[string]struct{}{
	"name":                 {},
	"type":                 {},
	"subtype":              {},
	"status":               {},
	"state":                {},
	"environment":          {},
	"cloud_provider":       {},
	"region":               {},
	"location":             {},
	"labels":               {},
	"tags":                 {},
	"arn":                  {},
	"resource_id":          {},
	"external_resource_id": {},
	"nb_resource_id":       {},
	"nb_account_id":        {},
	"account_number":       {},
	"aws_account_number":   {},
	"service_name":         {},
	"is_active":            {},
	"source":               {},
	"managed":              {},
}

// IsUniversalBaseProperty reports whether key is stamped on every node by the
// common builders (and therefore need not appear in a per-type schema).
func IsUniversalBaseProperty(key string) bool {
	_, ok := universalBaseProperties[key]
	return ok
}
