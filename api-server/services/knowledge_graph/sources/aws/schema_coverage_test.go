package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasSchema is the forcing function for the AWS concrete-schema layer: every concrete
// specific_type label the source can emit (from awsSpecificTypeMap +
// awsServiceSpecificTypeFallback, plus the IAM identity labels set directly in the
// builders) must have a registered core.SpecificTypeSchema. A newly-added AWS
// resource type therefore cannot ship a specific_type without also declaring its
// concrete property schema, which drives query_attributes.
func TestSpecificTypeHasSchema(t *testing.T) {
	seen := map[string]bool{}
	check := func(label string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		if _, ok := core.LookupSpecificTypeSchema(label); !ok {
			t.Errorf("AWS specific_type %q has no concrete schema — declare a core.SpecificTypeSchema and register it in its sources/aws/*.go init()", label)
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
