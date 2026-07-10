package aws

// AWS all-types NODE-coverage completeness (Tier A). Synthesizes one resource per
// registry entry from awsResourceTypeMap/awsServiceFallbackMap, runs the real
// converter, and asserts every NodeType the registry can emit is produced. Moved
// here (from package sources) because it reads the unexported AWS registry maps.
// The small test helpers are local copies of the package-sources originals — the
// package split makes them un-shareable, and duplicating ~30 lines of test glue
// is cheaper than exporting test-only helpers.

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

func allTypesRow(i int, resourceType, serviceName, provider string) sources.CloudResourceRow {
	id := fmt.Sprintf("%s-res-%d", provider, i)
	empty, _ := json.Marshal(map[string]interface{}{})
	return sources.CloudResourceRow{
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

func disableCloudCLIForTest() func() {
	prev := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = ""
	return func() { config.Config.CloudCollectorServerUrl = prev }
}

func allTypesReqCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("all-types-tenant", nil, nil, nil)
}

func assertNodeTypeCoverage(t *testing.T, provider string, produced, expected, excluded map[core.NodeType]bool) {
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

	var rows []sources.CloudResourceRow
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
