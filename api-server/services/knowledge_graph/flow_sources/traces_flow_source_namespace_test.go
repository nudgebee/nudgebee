package flow_sources

import (
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
)

func nsWorkloadNode(id, name, namespace, accountID string) *core.DbNode {
	return &core.DbNode{
		ID:             id,
		NodeType:       core.NodeTypeWorkload,
		UniqueKey:      "k8s:" + accountID + ":" + namespace + ":Workload::" + name,
		CloudAccountID: accountID,
		Source:         "k8s",
		Properties: map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}
}

func nsApp(kind, namespace, name string, upstreams []traces.UpstreamLink) traces.ServiceApplication {
	return traces.ServiceApplication{
		Id:        traces.ServiceApplicationId{Name: name, Kind: kind, Namespace: namespace},
		Upstreams: upstreams,
	}
}

// TestTracesFlowSource_BuildGraphFromServiceMap_NamespaceQualifiedAppLookup is the
// end-to-end regression test for the reported bug: a caller in one namespace must
// resolve its upstream to the same-named service in its OWN namespace.
//
// The ns-staging copy is deliberately later in the Applications slice, because
// the old kind+name lookup key was last-write-wins — so this ordering is what made
// the caller in "ns-prod" resolve to the "ns-staging" node.
func TestTracesFlowSource_BuildGraphFromServiceMap_NamespaceQualifiedAppLookup(t *testing.T) {
	const acct = "acct-1"
	source := NewTracesFlowSource(slog.Default())

	right := nsWorkloadNode("node-ns-prod", "notification-server", "ns-prod", acct)
	wrong := nsWorkloadNode("node-ns-staging", "notification-server", "ns-staging", acct)
	caller := nsWorkloadNode("node-checkout", "checkout", "ns-prod", acct)
	source.InitializeNodeMatcher([]*core.DbNode{caller, right, wrong})

	serviceMap := &traces.ServiceMap{
		Applications: []traces.ServiceApplication{
			nsApp("Service", "ns-prod", "checkout", []traces.UpstreamLink{
				{Id: "ns-prod:Service:notification-server", RequestCount: 10, Protocol: "HTTP"},
			}),
			nsApp("Service", "ns-prod", "notification-server", nil),
			// Later in the slice: wins the old last-write-wins collapse.
			nsApp("Service", "ns-staging", "notification-server", nil),
		},
	}

	edges, _ := source.BuildGraphFromServiceMap(
		serviceMap,
		&core.FlowSourceBuildRequest{TenantID: "tenant-1"},
		core.K8sAccount{CloudAccountID: acct, Tenant: "tenant-1"},
		nil, nil, nil,
	)

	var calls []*core.DbEdge
	for _, e := range edges {
		if e.SourceNodeID == caller.ID && e.RelationshipType == core.RelationshipCalls {
			calls = append(calls, e)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("got %d CALLS edges from checkout, want 1: %+v", len(calls), calls)
	}
	if calls[0].DestinationNodeID == wrong.ID {
		t.Fatalf("checkout in namespace ns-prod got a CALLS edge to notification-server in ns-staging")
	}
	if calls[0].DestinationNodeID != right.ID {
		t.Errorf("CALLS destination = %q, want %q (notification-server in ns-prod)", calls[0].DestinationNodeID, right.ID)
	}
}

// TestTracesFlowSource_BuildGraphFromServiceMap_EachNamespaceKeepsOwnEdge proves the
// fix is not simply "always pick the first" — each caller must reach its own
// namespace's copy.
func TestTracesFlowSource_BuildGraphFromServiceMap_EachNamespaceKeepsOwnEdge(t *testing.T) {
	const acct = "acct-1"
	source := NewTracesFlowSource(slog.Default())

	prodTarget := nsWorkloadNode("node-prod-target", "notification-server", "ns-prod", acct)
	testTarget := nsWorkloadNode("node-test-target", "notification-server", "ns-staging", acct)
	prodCaller := nsWorkloadNode("node-checkout", "checkout", "ns-prod", acct)
	testCaller := nsWorkloadNode("node-probe", "probe", "ns-staging", acct)
	source.InitializeNodeMatcher([]*core.DbNode{prodCaller, testCaller, prodTarget, testTarget})

	serviceMap := &traces.ServiceMap{
		Applications: []traces.ServiceApplication{
			nsApp("Service", "ns-prod", "checkout", []traces.UpstreamLink{
				{Id: "ns-prod:Service:notification-server", RequestCount: 5},
			}),
			nsApp("Service", "ns-staging", "probe", []traces.UpstreamLink{
				{Id: "ns-staging:Service:notification-server", RequestCount: 5},
			}),
			nsApp("Service", "ns-prod", "notification-server", nil),
			nsApp("Service", "ns-staging", "notification-server", nil),
		},
	}

	edges, _ := source.BuildGraphFromServiceMap(
		serviceMap,
		&core.FlowSourceBuildRequest{TenantID: "tenant-1"},
		core.K8sAccount{CloudAccountID: acct, Tenant: "tenant-1"},
		nil, nil, nil,
	)

	dest := map[string]string{}
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipCalls {
			dest[e.SourceNodeID] = e.DestinationNodeID
		}
	}
	if got := dest[prodCaller.ID]; got != prodTarget.ID {
		t.Errorf("checkout (ns-prod) -> %q, want %q", got, prodTarget.ID)
	}
	if got := dest[testCaller.ID]; got != testTarget.ID {
		t.Errorf("probe (ns-staging) -> %q, want %q", got, testTarget.ID)
	}
}

// TestLookupApp covers the qualified-miss policy: a namespace-qualified miss may
// fall back to a namespace-less application (the producer could not determine one),
// but must never accept a candidate that belongs to a different, known namespace.
func TestLookupApp(t *testing.T) {
	qualified := nsApp("Service", "ns-prod", "notification-server", nil)
	unqualified := nsApp("Service", "", "legacy-worker", nil)

	appLookup := map[string]*traces.ServiceApplication{
		appLookupKey("Service", "ns-prod", "notification-server"): &qualified,
		appLookupKey("Service", "", "legacy-worker"):              &unqualified,
	}

	tests := []struct {
		name              string
		kind, ns, svcName string
		wantFound         bool
		wantApp           *traces.ServiceApplication
	}{
		{"exact namespace hit", "Service", "ns-prod", "notification-server", true, &qualified},
		{"different known namespace is refused", "Service", "ns-staging", "notification-server", false, nil},
		{"namespace-less candidate accepted on qualified miss", "Service", "ns-prod", "legacy-worker", true, &unqualified},
		{"unknown name", "Service", "ns-prod", "absent", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, found := lookupApp(appLookup, tt.kind, tt.ns, tt.svcName)
			if found != tt.wantFound {
				t.Fatalf("lookupApp(%q, %q, %q) found = %v, want %v", tt.kind, tt.ns, tt.svcName, found, tt.wantFound)
			}
			if found && app != tt.wantApp {
				t.Errorf("lookupApp returned the wrong application: %+v", app.Id)
			}
		})
	}
}
