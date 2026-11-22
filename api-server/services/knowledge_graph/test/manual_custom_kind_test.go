package test

import (
	"testing"

	"nudgebee/services/knowledge_graph/flow_sources"
)

// A custom kind may not take the name of a type the code already uses. Runs in
// package test, where every source's specific types are registered.
func TestValidateCustomKindName_RegisteredTypes(t *testing.T) {
	for _, taken := range []string{"EC2Instance", "RDSInstance", "Database", "CloudResource"} {
		if err := flow_sources.ValidateCustomKindName(taken); err == nil {
			t.Errorf("%s accepted as a custom kind; it is a type the code already uses", taken)
		}
	}
	if err := flow_sources.ValidateCustomKindName("NetworkSwitch"); err != nil {
		t.Errorf("NetworkSwitch refused: %v", err)
	}
}
