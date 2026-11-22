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

	// Empty specific_type exercises the per-NodeType fallback path (no schema is
	// registered in core-only tests, which don't import the source packages).
	attrs := ExtractQueryAttributes(NodeTypeWorkload, "", props)

	if attrs["kind"] != "Deployment" {
		t.Errorf("query_attributes[kind] = %v, want %q (kind must be hoisted for filtering)", attrs["kind"], "Deployment")
	}
	if _, ok := attrs["workload_type"]; ok {
		t.Errorf("query_attributes contains dead key workload_type: %v", attrs)
	}
}

// TestRegionIsQueryableForEveryNodeType guards #41137: region was listed on
// only 8 of the node types in QueryablePropertiesMap, so the KG Attribute
// filter (which reads query_attributes) matched those 8 and silently left out
// EC2, RDS, S3, Lambda, EBS, load balancers and the rest, even though their
// Node Details pane — which reads properties — displayed that region.
func TestRegionIsQueryableForEveryNodeType(t *testing.T) {
	props := map[string]interface{}{
		"name":   "some-resource",
		"region": "us-east-1",
	}

	for nodeType := range QueryablePropertiesMap {
		// Empty specific_type exercises the per-NodeType path; core-only tests
		// register no concrete schemas.
		attrs := ExtractQueryAttributes(nodeType, "", props)
		if attrs["region"] != "us-east-1" {
			t.Errorf("%s: query_attributes[region] = %v, want %q (region must be filterable on every node type)",
				nodeType, attrs["region"], "us-east-1")
		}
	}

	// Node types with no map entry fall back to the common list and must also
	// be filterable by region.
	attrs := ExtractQueryAttributes(NodeType("zzUnmappedNodeType"), "", props)
	if attrs["region"] != "us-east-1" {
		t.Errorf("unmapped node type: query_attributes[region] = %v, want %q", attrs["region"], "us-east-1")
	}
}

// TestRegionAbsentWhenNotCollected keeps the hoist from inventing a key: a node
// whose properties carry no region must not gain an empty one, or
// jsonb_exists(query_attributes, 'region') would match every node.
func TestRegionAbsentWhenNotCollected(t *testing.T) {
	attrs := ExtractQueryAttributes(NodeTypePod, "", map[string]interface{}{
		"name":      "checkout-abc123",
		"namespace": "shop",
	})
	if _, ok := attrs["region"]; ok {
		t.Errorf("query_attributes gained a region key that properties never had: %v", attrs)
	}
}

// TestGetQueryablePropertiesIncludesCommon pins the exported helper's contract:
// it answers "can this be filtered on", so it must report the common keys for a
// node type that has its own map entry too, and must not repeat a key a type
// already lists. Without this, a future caller reading it for validation or
// autocomplete would treat region as unfilterable on most types.
func TestGetQueryablePropertiesIncludesCommon(t *testing.T) {
	for nodeType := range QueryablePropertiesMap {
		props := GetQueryableProperties(nodeType)

		seen := make(map[string]int, len(props))
		for _, p := range props {
			seen[p]++
		}
		for _, common := range CommonQueryableProperties {
			if seen[common] == 0 {
				t.Errorf("%s: GetQueryableProperties() omits common key %q: %v", nodeType, common, props)
			}
		}
		for p, n := range seen {
			if n > 1 {
				t.Errorf("%s: GetQueryableProperties() repeats %q %d times: %v", nodeType, p, n, props)
			}
		}
	}
}
