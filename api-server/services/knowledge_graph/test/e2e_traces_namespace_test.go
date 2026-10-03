package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/traces"
)

// ============================================================================
// Scenario: traces_cross_namespace — two namespaces running a same-named service,
// each with its own caller. Locks in that trace-derived CALLS edges stay inside the
// caller's namespace instead of collapsing onto whichever copy the service map
// happened to list last.
//
// This is the first traces-driven scenario in the suite. It needs no trace backend:
// the service map is a checked-in fixture, fed straight to the flow source's
// BuildGraphFromServiceMap seam.
// ============================================================================

func loadTracesServiceMap(t *testing.T, scenario string) *traces.ServiceMap {
	t.Helper()
	path := filepath.Join("testdata", "e2e", scenario, "input", "service_map.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	var sm traces.ServiceMap
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	return &sm
}

// seedWorkload builds the k8s_source-authoritative node a trace observation should
// resolve to.
func seedWorkload(name, namespace string) *core.DbNode {
	return &core.DbNode{
		ID:             "seed-" + namespace + "-" + name,
		NodeType:       core.NodeTypeWorkload,
		UniqueKey:      "k8s:" + e2eK8sAccount + ":" + namespace + ":Workload::" + name,
		CloudAccountID: e2eK8sAccount,
		TenantID:       e2eTenantID,
		Source:         "k8s",
		Properties: map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"kind":      "Deployment",
		},
	}
}

func TestE2E_TierA_TracesCrossNamespace(t *testing.T) {
	seeds := []*core.DbNode{
		seedWorkload("checkout", "ns-prod"),
		seedWorkload("notification-server", "ns-prod"),
		seedWorkload("probe", "ns-staging"),
		seedWorkload("notification-server", "ns-staging"),
	}

	source := flow_sources.NewTracesFlowSource(e2eDiscardLogger())
	source.InitializeNodeMatcher(seeds)

	serviceMap := loadTracesServiceMap(t, "traces_cross_namespace")
	flowEdges, flowNodes := source.BuildGraphFromServiceMap(
		serviceMap,
		&core.FlowSourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eK8sAccount},
		core.K8sAccount{CloudAccountID: e2eK8sAccount, Tenant: e2eTenantID},
		nil, nil, nil,
	)

	nodes := append(append([]*core.DbNode{}, seeds...), flowNodes...)
	nodes, edges := mergeGraph(nodes, flowEdges, mergeOpts{})

	// Guard the property the golden exists to protect, so a careless -update
	// cannot silently bless a cross-namespace edge.
	byID := make(map[string]*core.DbNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, e := range edges {
		if e.RelationshipType != core.RelationshipCalls {
			continue
		}
		src, dst := byID[e.SourceNodeID], byID[e.DestinationNodeID]
		if src == nil || dst == nil {
			continue
		}
		srcNS, _ := src.Properties["namespace"].(string)
		dstNS, _ := dst.Properties["namespace"].(string)
		if srcNS != "" && dstNS != "" && srcNS != dstNS {
			t.Errorf("CALLS edge crosses namespaces: %s/%v -> %s/%v",
				srcNS, src.Properties["name"], dstNS, dst.Properties["name"])
		}
	}

	assertGoldenGraph(t, e2eGoldenPath("traces_cross_namespace"), nodes, edges)
}
