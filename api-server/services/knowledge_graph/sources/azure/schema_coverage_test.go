package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasSchema is the forcing function for Azure: every concrete
// specific_type label an Azure node can carry must have a registered
// concrete-property schema (core.SpecificTypeSchema) declared co-located in its
// sources/azure/*.go file. A newly added Azure resource kind therefore cannot
// ship a specific_type without also declaring the schema that drives its
// query_attributes.
func TestSpecificTypeHasSchema(t *testing.T) {
	labels := azureSpecificTypeLabels()
	for _, label := range labels {
		if _, ok := core.LookupSpecificTypeSchema(label); !ok {
			t.Errorf("Azure specific_type %q has no concrete schema — register a core.SpecificTypeSchema in its sources/azure/*.go file", label)
		}
	}
	// The label set is what the ontology layer covers; a drop here means a kind
	// or a synthesized label went missing rather than that Azure shrank.
	if len(labels) != 30 {
		t.Errorf("drove %d distinct Azure specific_type labels, want 30: %v", len(labels), labels)
	}
}
