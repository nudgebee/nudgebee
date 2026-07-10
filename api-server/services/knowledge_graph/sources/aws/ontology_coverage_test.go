package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasOntologySpec is the forcing function for AWS: every concrete
// specific_type label the source can emit (from awsSpecificTypeMap +
// awsServiceSpecificTypeFallback) must have a matching ontology spec registered
// in core/ontology_data_aws.go. A newly-added AWS resource type therefore cannot
// ship a specific_type without also declaring its normalized ontology schema.
func TestSpecificTypeHasOntologySpec(t *testing.T) {
	seen := map[string]bool{}
	check := func(label string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		if _, ok := core.LookupOntologySpec(label); !ok {
			t.Errorf("AWS specific_type %q has no ontology registry spec — add it to core/ontology_data_aws.go", label)
		}
	}
	for _, m := range awsSpecificTypeMap {
		for _, label := range m {
			check(label)
		}
	}
	for _, label := range awsServiceSpecificTypeFallback {
		check(label)
	}
	// Identity labels set directly in the IAM builders (iam.go) rather than via
	// the maps above.
	check("IAMRole")
	check("IAMUser")
}
