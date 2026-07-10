package gcp

// GCP all-types NODE-coverage completeness (Tier A). Moved here from package
// sources because it reads the unexported GCP registry maps. Small test helpers
// are local copies (the package split makes the sources originals un-shareable).

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

func TestE2E_GCPAllTypesNodeCoverage(t *testing.T) {
	defer disableCloudCLIForTest()()
	src, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}
	h := NewGCPSourceTestHelper(src)

	var rows []sources.CloudResourceRow
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

	excluded := map[core.NodeType]bool{}
	assertNodeTypeCoverage(t, "GCP", produced, expected, excluded)
}
