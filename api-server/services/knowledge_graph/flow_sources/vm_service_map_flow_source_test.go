package flow_sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
	"nudgebee/services/vmservicemap"
	"testing"
)

func TestNewVMServiceMapFlowSource(t *testing.T) {
	source := NewVMServiceMapFlowSource(slog.Default())

	if source.GetName() != core.VMServiceMapSourceName {
		t.Errorf("GetName() = %q, want %q", source.GetName(), core.VMServiceMapSourceName)
	}
	if source.GetSourceCategory() != core.FlowSourceCategoryTracing {
		t.Errorf("GetSourceCategory() = %q, want tracing", source.GetSourceCategory())
	}
	if !source.IsEnabled() {
		t.Error("expected the VM service-map flow source to be enabled by default")
	}
}

// TestCreateServiceOrExternalNode_DisambiguatesSameNameAcrossHosts guards the
// bug that made reusing the generic ConvertServiceMapToGraph adapter unsafe:
// that adapter keys solely on name+environment, so two VM hosts running an
// identically-named systemd unit (very common — "nginx.service") would
// collapse onto one graph node. This flow source's own key includes the host.
func TestCreateServiceOrExternalNode_DisambiguatesSameNameAcrossHosts(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())

	appA := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Name: "nginx.service", Kind: vmservicemap.NodeKindSystemd, Namespace: "web-host-a"},
		Labels: map[string]string{},
	}
	appB := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Name: "nginx.service", Kind: vmservicemap.NodeKindSystemd, Namespace: "web-host-b"},
		Labels: map[string]string{},
	}

	nodeA := s.createServiceOrExternalNode(appA, "tenant-1", "account-1")
	nodeB := s.createServiceOrExternalNode(appB, "tenant-1", "account-1")

	if nodeA.ID == nodeB.ID {
		t.Fatalf("two VM hosts' same-named services collapsed onto one node: %s", nodeA.ID)
	}
	if nodeA.Properties["host"] != "web-host-a" || nodeB.Properties["host"] != "web-host-b" {
		t.Errorf("host property = %v / %v, want web-host-a / web-host-b", nodeA.Properties["host"], nodeB.Properties["host"])
	}
	if nodeA.NodeType != core.NodeTypeService {
		t.Errorf("NodeType = %q, want Service", nodeA.NodeType)
	}
	if nodeA.Source != core.VMServiceMapSourceName {
		t.Errorf("Source = %q, want %q", nodeA.Source, core.VMServiceMapSourceName)
	}
}

// TestCreateServiceOrExternalNode_SameAppReobservedGetsTheSameID guards the
// re-observation path the sweep (MarkStaleVMServiceNodesInactive) depends on:
// a service built again on a later run must land on the same node id, not a
// new one, or it would never re-activate and would instead orphan the old row.
func TestCreateServiceOrExternalNode_SameAppReobservedGetsTheSameID(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())
	app := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Name: "orders-api.service", Kind: vmservicemap.NodeKindSystemd, Namespace: "orders-api-host"},
		Labels: map[string]string{"instance_id": "i-0api"},
	}

	first := s.createServiceOrExternalNode(app, "tenant-1", "account-1")
	second := s.createServiceOrExternalNode(app, "tenant-1", "account-1")

	if first.ID != second.ID {
		t.Fatalf("re-observing the same app produced different ids: %s vs %s", first.ID, second.ID)
	}
}

func TestCreateServiceOrExternalNode_ExternalKindsHaveNoHostHierarchy(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())

	for _, kind := range []string{vmservicemap.NodeKindExternal, vmservicemap.NodeKindUnknownInternal} {
		app := &traces.ServiceApplication{
			Id:     traces.ServiceApplicationId{Name: "172.31.9.9", Kind: kind, Namespace: "unknown-internal"},
			Labels: map[string]string{},
		}
		node := s.createServiceOrExternalNode(app, "tenant-1", "account-1")
		if node.NodeType != core.NodeTypeExternalService {
			t.Errorf("kind %q: NodeType = %q, want ExternalService", kind, node.NodeType)
		}
		if _, hasHost := node.Properties["host"]; hasHost {
			t.Errorf("kind %q: external node should not carry a host property", kind)
		}
	}
}

func computeInstanceNode(accountID, tenantID, resourceID, hostKind, instanceID, privateIP string) *core.DbNode {
	props := map[string]interface{}{
		"host_kind": hostKind,
	}
	if instanceID != "" {
		props["instance_id"] = instanceID
	}
	if privateIP != "" {
		props["private_ip"] = privateIP
	}
	uniqueKey := core.BuildUniqueKey(core.CloudProviderSelfHosted, accountID, "global", core.NodeTypeComputeInstance, "", resourceID)
	return core.NewNode(core.NodeTypeComputeInstance, uniqueKey, props, tenantID, accountID, "selfhosted")
}

func TestMatchHostNode_ByInstanceID(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())
	hostNode := computeInstanceNode("account-1", "tenant-1", "i-0web", "cloud_identified", "i-0web", "")
	s.InitializeNodeMatcher([]*core.DbNode{hostNode})

	got := s.matchHostNode([]string{"account-1"}, vmservicemap.Host{InstanceID: "i-0web", Hostname: "web-host"})
	if got == nil || got.ID != hostNode.ID {
		t.Fatalf("matchHostNode by instance_id = %v, want %s", got, hostNode.ID)
	}
}

func TestMatchHostNode_FallsBackToPrivateIPForOnPremHost(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())
	hostNode := computeInstanceNode("account-1", "tenant-1", "vm-10.0.0.7", "on_prem", "", "10.0.0.7")
	s.InitializeNodeMatcher([]*core.DbNode{hostNode})

	// No instance id (on-prem host) — must fall back to the address.
	got := s.matchHostNode([]string{"account-1"}, vmservicemap.Host{Hostname: "batch", IPs: []string{"10.0.0.7"}})
	if got == nil || got.ID != hostNode.ID {
		t.Fatalf("matchHostNode by private_ip fallback = %v, want %s", got, hostNode.ID)
	}
}

func TestMatchHostNode_NeverMatchesOutsideScopedAccounts(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())
	// Same private IP range reused by an unrelated account — must not match.
	otherAccountNode := computeInstanceNode("account-2", "tenant-1", "vm-10.0.0.7", "on_prem", "", "10.0.0.7")
	s.InitializeNodeMatcher([]*core.DbNode{otherAccountNode})

	got := s.matchHostNode([]string{"account-1"}, vmservicemap.Host{Hostname: "batch", IPs: []string{"10.0.0.7"}})
	if got != nil {
		t.Fatalf("matchHostNode matched a node outside the scoped accounts: %s", got.ID)
	}
}

func TestMatchHostNode_ReturnsNilRatherThanFabricatingAHost(t *testing.T) {
	s := NewVMServiceMapFlowSource(slog.Default())
	s.InitializeNodeMatcher([]*core.DbNode{})

	got := s.matchHostNode([]string{"account-1"}, vmservicemap.Host{InstanceID: "i-unknown"})
	if got != nil {
		t.Fatalf("matchHostNode = %v, want nil on no match", got)
	}
}

func TestIndexForagerHosts(t *testing.T) {
	hosts := []vmservicemap.Host{
		{InstanceID: "i-0web", Hostname: "orders-web-host"},
		{Hostname: "batch"}, // on-prem, no instance id
	}
	byInstanceID, byHostname := indexForagerHosts(hosts)

	if len(byInstanceID) != 1 || byInstanceID["i-0web"].Hostname != "orders-web-host" {
		t.Errorf("byInstanceID = %v", byInstanceID)
	}
	if len(byHostname) != 2 || byHostname["batch"].Hostname != "batch" {
		t.Errorf("byHostname = %v", byHostname)
	}
}

func TestForagerHostFor(t *testing.T) {
	byInstanceID, byHostname := indexForagerHosts([]vmservicemap.Host{
		{InstanceID: "i-0web", Hostname: "orders-web-host", IPs: []string{"172.31.1.10"}},
		{Hostname: "batch", IPs: []string{"10.0.0.7"}},
	})

	cloudApp := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Namespace: "orders-web-host"},
		Labels: map[string]string{"instance_id": "i-0web"},
	}
	if h, ok := foragerHostFor(cloudApp, byInstanceID, byHostname); !ok || h.Hostname != "orders-web-host" {
		t.Errorf("foragerHostFor(cloud-identified) = %v, %v", h, ok)
	}

	onPremApp := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Namespace: "batch"},
		Labels: map[string]string{},
	}
	if h, ok := foragerHostFor(onPremApp, byInstanceID, byHostname); !ok || len(h.IPs) != 1 || h.IPs[0] != "10.0.0.7" {
		t.Errorf("foragerHostFor(on-prem) = %v, %v", h, ok)
	}

	unknownApp := &traces.ServiceApplication{
		Id:     traces.ServiceApplicationId{Namespace: "nowhere"},
		Labels: map[string]string{},
	}
	if _, ok := foragerHostFor(unknownApp, byInstanceID, byHostname); ok {
		t.Error("foragerHostFor matched a namespace with no corresponding forager host")
	}
}

func TestUpstreamEdgeProperties(t *testing.T) {
	up := &traces.UpstreamLink{
		Protocol: "http", Latency: 38.5, RequestCount: 1200, FailureCount: 12,
		BytesSent: 482113, BytesReceived: 9120331, Status: 1,
	}
	props := upstreamEdgeProperties(up)

	if props["protocol"] != "http" || props["request_count"] != float64(1200) || props["status"] != 1 {
		t.Errorf("upstreamEdgeProperties = %v", props)
	}
}
