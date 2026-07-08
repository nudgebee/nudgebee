package sources

// Provider all-types NODE-coverage completeness (E2E KG test module, Tier A).
//
// For each cloud source, we synthesize one resource per registry entry directly
// from the canonical maps, run the REAL converter, and assert that every distinct
// NodeType the registry can emit is actually produced. This goes beyond the
// dispatch-completeness test (which only checks determineNodeType): it verifies
// the full convertResourcesToGraph path actually emits a node of that type and
// doesn't silently suppress or re-type it.
//
// Because the expected set is derived from the registry, adding a new type/
// NodeType to a registry automatically requires it to be produced here — the
// test fails until the converter emits it (or it's added to the documented
// exclusions below).

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// allTypesRow synthesizes a minimal cloud-resource row for a (type, service).
func allTypesRow(i int, resourceType, serviceName, provider string) CloudResourceRow {
	id := fmt.Sprintf("%s-res-%d", provider, i)
	empty, _ := json.Marshal(map[string]interface{}{})
	return CloudResourceRow{
		ID:                 id,
		ResourceID:         id,
		Name:               fmt.Sprintf("%s-%s-%d", provider, resourceType, i),
		Type:               resourceType,
		ServiceName:        serviceName,
		Account:            "all-types-account",
		Tenant:             "all-types-tenant",
		CloudProvider:      provider,
		Region:             "us-east-1",
		IsActive:           true,
		ExternalResourceID: id,
		Meta:               empty,
		Tags:               empty,
	}
}

// disableCloudCLIForTest forces cloud.ExecuteCli to early-return an error so the
// converters run purely in-memory (mirrors the helper in the knowledge_graph
// package). Returns a restore func.
func disableCloudCLIForTest() func() {
	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = ""
	return func() { config.Config.CloudCollectorServerUrl = prev }
}

func allTypesReqCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("all-types-tenant", nil, nil, nil)
}

// assertNodeTypeCoverage runs the converter over the generated rows and asserts
// every expected NodeType is produced (minus documented exclusions).
func assertNodeTypeCoverage(t *testing.T, provider string, produced map[core.NodeType]bool, expected map[core.NodeType]bool, excluded map[core.NodeType]bool) {
	t.Helper()
	var missing []string
	for nt := range expected {
		if excluded[nt] {
			continue
		}
		if !produced[nt] {
			missing = append(missing, string(nt))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s all_types: registry NodeTypes not produced by converter: %v\n"+
			"(add a fixture that triggers them, or add to the documented exclusions with a reason)", provider, missing)
	}
	t.Logf("%s all_types: %d distinct NodeTypes produced, %d expected (%d excluded)",
		provider, len(produced), len(expected), len(excluded))
}

func TestE2E_AWSAllTypesNodeCoverage(t *testing.T) {
	defer disableCloudCLIForTest()()
	src, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}
	h := NewAWSSourceTestHelper(src)

	var rows []CloudResourceRow
	expected := map[core.NodeType]bool{}
	i := 0
	for rt, svcMap := range awsResourceTypeMap {
		for svc, nt := range svcMap {
			rows = append(rows, allTypesRow(i, rt, svc, "AWS"))
			expected[nt] = true
			i++
		}
	}
	for svc, nt := range awsServiceFallbackMap {
		rows = append(rows, allTypesRow(i, "__fallback_type__", svc, "AWS"))
		expected[nt] = true
		i++
	}

	req := &core.SourceBuildRequest{TenantID: "all-types-tenant", CloudAccountID: "all-types-account"}
	nodes, _ := h.ConvertResourcesToGraph(allTypesReqCtx(), rows, req)

	produced := map[core.NodeType]bool{}
	for _, n := range nodes {
		produced[n.NodeType] = true
	}

	// Documented exclusions — registry NodeTypes the converter does not emit from
	// a bare synthetic row:
	//   NetworkInterface: ENI nodes are synthesized from attached-resource
	//   metadata via the ENI resolver (createENIEdges), never from a standalone
	//   network-interface row.
	excluded := map[core.NodeType]bool{
		core.NodeTypeNetworkInterface: true,
	}
	assertNodeTypeCoverage(t, "AWS", produced, expected, excluded)
}

func TestE2E_GCPAllTypesNodeCoverage(t *testing.T) {
	defer disableCloudCLIForTest()()
	src, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}
	h := NewGCPSourceTestHelper(src)

	var rows []CloudResourceRow
	expected := map[core.NodeType]bool{}
	i := 0
	for rt, svcMap := range gcpResourceTypeMap {
		for svc, nt := range svcMap {
			rows = append(rows, allTypesRow(i, rt, svc, "GCP"))
			expected[nt] = true
			i++
		}
	}
	for svc, nt := range gcpServiceFallbackMap {
		rows = append(rows, allTypesRow(i, "__fallback_type__", svc, "GCP"))
		expected[nt] = true
		i++
	}

	req := &core.SourceBuildRequest{TenantID: "all-types-tenant", CloudAccountID: "all-types-account"}
	nodes, _ := h.ConvertResourcesToGraph(allTypesReqCtx(), rows, req)

	produced := map[core.NodeType]bool{}
	for _, n := range nodes {
		produced[n.NodeType] = true
	}

	// Documented exclusions populated after empirical run.
	excluded := map[core.NodeType]bool{}
	assertNodeTypeCoverage(t, "GCP", produced, expected, excluded)
}

func TestE2E_AzureAllTypesNodeCoverage(t *testing.T) {
	defer disableCloudCLIForTest()()
	src, err := NewAzureSource(AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource: %v", err)
	}
	h := NewAzureSourceTestHelper(src)

	var rows []CloudResourceRow
	expected := map[core.NodeType]bool{}
	i := 0
	for rt, nt := range azureTypeToNodeType {
		// Azure service_name uses the "provider/type" form; the type alone is
		// enough for the flat registry lookup.
		rows = append(rows, allTypesRow(i, rt, "microsoft.compute/"+rt, "Azure"))
		expected[nt] = true
		i++
	}
	for prefix, nt := range azureServicePrefixToNodeType {
		rows = append(rows, allTypesRow(i, "__fallback_type__", prefix, "Azure"))
		expected[nt] = true
		i++
	}

	req := &core.SourceBuildRequest{TenantID: "all-types-tenant", CloudAccountID: "all-types-account"}
	nodes, _ := h.ConvertResourcesToGraph(allTypesReqCtx(), rows, req)

	produced := map[core.NodeType]bool{}
	for _, n := range nodes {
		produced[n.NodeType] = true
	}

	// Documented exclusions populated after empirical run.
	excluded := map[core.NodeType]bool{}
	assertNodeTypeCoverage(t, "Azure", produced, expected, excluded)
}
