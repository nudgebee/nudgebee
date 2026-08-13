package core

import (
	"reflect"
	"testing"
)

// These tests lock the schema-registry engine (schema_registry.go). They run in
// package core, which does NOT import the source packages, so the only schemas
// present are the test-only ones registered here (unique zz* labels avoid the
// duplicate-registration panic). The per-cloud coverage/conformance/consistency
// guards that need the real registered schemas live where sources are imported.

func TestSpecificTypeSchemaIndexedAndPropertyNames(t *testing.T) {
	RegisterSpecificTypeSchema(SpecificTypeSchema{
		SpecificType: "zzTestType",
		NodeType:     NodeTypeCloudResource,
		Properties: []PropertyDef{
			{Name: "foo", Indexed: true, Required: true},
			{Name: "bar"},
			{Name: "baz", Indexed: true},
		},
	})

	s, ok := LookupSpecificTypeSchema("zzTestType")
	if !ok {
		t.Fatal("LookupSpecificTypeSchema(zzTestType) = false, want registered")
	}
	if got, want := s.IndexedFields(), []string{"baz", "foo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("IndexedFields() = %v, want %v (sorted, Indexed-only)", got, want)
	}
	if got, want := s.PropertyNames(), []string{"bar", "baz", "foo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("PropertyNames() = %v, want %v (sorted, all)", got, want)
	}
}

func TestExtractQueryAttributesSchemaUnion(t *testing.T) {
	RegisterSpecificTypeSchema(SpecificTypeSchema{
		SpecificType: "zzUnionType",
		NodeType:     NodeTypeComputeInstance, // map contributes instance_type, name, ...
		Properties:   []PropertyDef{{Name: "custom_field", Indexed: true}},
	})

	props := map[string]interface{}{
		"name":          "x",
		"instance_type": "t3.micro", // from the per-NodeType map
		"custom_field":  "v",        // from the schema
		"not_declared":  "y",        // neither → must not be hoisted
	}

	attrs := ExtractQueryAttributes(NodeTypeComputeInstance, "zzUnionType", props)
	if attrs["instance_type"] != "t3.micro" {
		t.Errorf("map-driven key missing: query_attributes[instance_type]=%v", attrs["instance_type"])
	}
	if attrs["custom_field"] != "v" {
		t.Errorf("schema-driven key missing: query_attributes[custom_field]=%v", attrs["custom_field"])
	}
	if _, ok := attrs["not_declared"]; ok {
		t.Errorf("undeclared key leaked into query_attributes: %v", attrs)
	}

	// Unknown specific_type → fallback to the per-NodeType map only (no schema keys).
	fallback := ExtractQueryAttributes(NodeTypeComputeInstance, "zzUnknownType", props)
	if fallback["instance_type"] != "t3.micro" {
		t.Errorf("fallback lost map key: %v", fallback)
	}
	if _, ok := fallback["custom_field"]; ok {
		t.Errorf("fallback hoisted a schema-only key with no registered schema: %v", fallback)
	}
}

func TestRegisterSpecificTypeSchemaDuplicatePanics(t *testing.T) {
	RegisterSpecificTypeSchema(SpecificTypeSchema{SpecificType: "zzDupType", NodeType: NodeTypeCloudResource})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate specific_type registration, got none")
		}
	}()
	RegisterSpecificTypeSchema(SpecificTypeSchema{SpecificType: "zzDupType", NodeType: NodeTypeCloudResource})
}

func TestIsUniversalBaseProperty(t *testing.T) {
	if !IsUniversalBaseProperty("name") {
		t.Error("name should be a universal base property")
	}
	if IsUniversalBaseProperty("instance_type") {
		t.Error("instance_type is resource-specific, not a universal base property")
	}
}
