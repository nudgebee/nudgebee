package core

import "testing"

// TestWorkloadKindIsQueryable guards the fix that makes Workload nodes
// filterable by Kubernetes kind. The K8s source writes the property under
// the key "kind" (sources/k8s_source.go), but the queryable list previously
// listed the never-populated key "workload_type" — so kind was never hoisted
// into query_attributes and never appeared as a KG filter option.
func TestWorkloadKindIsQueryable(t *testing.T) {
	props := map[string]interface{}{
		"name":      "checkout",
		"namespace": "shop",
		"cluster":   "prod-eks",
		"kind":      "Deployment",
		// subtype duplicates kind for other node types' conventions; it must
		// not be the thing callers filter workloads on.
		"subtype": "Deployment",
	}

	attrs := ExtractQueryAttributes(NodeTypeWorkload, props)

	if attrs["kind"] != "Deployment" {
		t.Errorf("query_attributes[kind] = %v, want %q (kind must be hoisted for filtering)", attrs["kind"], "Deployment")
	}
	if _, ok := attrs["workload_type"]; ok {
		t.Errorf("query_attributes contains dead key workload_type: %v", attrs)
	}
}
