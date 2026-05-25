package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

func makeServiceNode(name, cluster, ip string) *core.DbNode {
	return &core.DbNode{
		NodeType: core.NodeTypeK8sService,
		Properties: map[string]interface{}{
			"name":       name,
			"cluster":    cluster,
			"cluster_ip": ip,
		},
	}
}

func TestK8sServiceIPResolver_SameClusterHit(t *testing.T) {
	nodes := []*core.DbNode{
		makeServiceNode("loki-gateway", "prod-us-east", "10.0.0.10"),
		makeServiceNode("redis", "prod-us-east", "10.0.0.11"),
	}
	r := NewK8sServiceIPResolver(nodes)

	got, ok := r.Resolve("prod-us-east", "10.0.0.10")
	if !ok {
		t.Fatalf("expected hit for same-cluster IP")
	}
	if got.Properties["name"] != "loki-gateway" {
		t.Errorf("expected loki-gateway, got %v", got.Properties["name"])
	}
}

func TestK8sServiceIPResolver_MultiClusterDisambiguates(t *testing.T) {
	// Same IP exists in two clusters — common in multi-cluster tenants.
	nodes := []*core.DbNode{
		makeServiceNode("svc-a", "cluster-a", "10.0.0.1"),
		makeServiceNode("svc-b", "cluster-b", "10.0.0.1"),
	}
	r := NewK8sServiceIPResolver(nodes)

	gotA, okA := r.Resolve("cluster-a", "10.0.0.1")
	if !okA || gotA.Properties["name"] != "svc-a" {
		t.Errorf("caller in cluster-a should resolve to svc-a, got %v ok=%v", gotA, okA)
	}

	gotB, okB := r.Resolve("cluster-b", "10.0.0.1")
	if !okB || gotB.Properties["name"] != "svc-b" {
		t.Errorf("caller in cluster-b should resolve to svc-b, got %v ok=%v", gotB, okB)
	}
}

func TestK8sServiceIPResolver_AmbiguousIPWithoutCallerCluster(t *testing.T) {
	// Same IP in two clusters; caller has no cluster context (non-K8s caller).
	// Resolver must refuse to guess — wrong-edge is worse than no-edge.
	nodes := []*core.DbNode{
		makeServiceNode("svc-a", "cluster-a", "10.0.0.1"),
		makeServiceNode("svc-b", "cluster-b", "10.0.0.1"),
	}
	r := NewK8sServiceIPResolver(nodes)

	if _, ok := r.Resolve("", "10.0.0.1"); ok {
		t.Errorf("ambiguous IP without caller cluster should return false")
	}
}

func TestK8sServiceIPResolver_UniqueIPWithoutCallerClusterHits(t *testing.T) {
	// One Service globally with this IP — safe to resolve even without caller cluster.
	nodes := []*core.DbNode{
		makeServiceNode("only-svc", "cluster-a", "10.0.0.99"),
	}
	r := NewK8sServiceIPResolver(nodes)

	got, ok := r.Resolve("", "10.0.0.99")
	if !ok || got.Properties["name"] != "only-svc" {
		t.Errorf("unique global IP should resolve, got %v ok=%v", got, ok)
	}
}

func TestIsSpecialIPName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Special — flow sources should drop these instead of creating
		// orphan ExternalService nodes.
		{"127.0.0.1", true},
		{"127.0.0.1:8080", true},
		{"::1", true},
		{"[::1]:8080", true},
		{"0.0.0.0", true},
		{"169.254.169.254", true}, // AWS/GCP metadata service
		{"169.254.169.254:80", true},
		{"fe80::1", true}, // IPv6 link-local
		// Not special — fall through to normal resolution / ExternalService.
		{"10.100.3.32", false},       // K8s ClusterIP
		{"172.31.5.25", false},       // pod IP
		{"172.31.5.25:5672", false},  // pod IP with port
		{"api.pagerduty.com", false}, // hostname
		{"", false},                  // empty — let normal flow handle
		{"not-an-ip-or-host", false}, // malformed — not our concern
	}
	for _, tc := range cases {
		if got := IsSpecialIPName(tc.name); got != tc.want {
			t.Errorf("IsSpecialIPName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestK8sServiceIPResolver_HeadlessServiceSkipped(t *testing.T) {
	nodes := []*core.DbNode{
		makeServiceNode("headless", "cluster-a", "None"),
		makeServiceNode("empty-ip", "cluster-a", ""),
		makeServiceNode("zero-ip", "cluster-a", "0.0.0.0"),
		makeServiceNode("real", "cluster-a", "10.0.0.5"),
	}
	r := NewK8sServiceIPResolver(nodes)

	if _, ok := r.Resolve("cluster-a", "None"); ok {
		t.Errorf(`"None" should not be resolvable`)
	}
	if _, ok := r.Resolve("cluster-a", ""); ok {
		t.Errorf("empty IP should not be resolvable")
	}
	if _, ok := r.Resolve("cluster-a", "0.0.0.0"); ok {
		t.Errorf("0.0.0.0 should not be resolvable")
	}
	got, ok := r.Resolve("cluster-a", "10.0.0.5")
	if !ok || got.Properties["name"] != "real" {
		t.Errorf("real IP should resolve, got %v ok=%v", got, ok)
	}
}

func TestK8sServiceIPResolver_NonK8sNodesIgnored(t *testing.T) {
	// Pollute the input with non-K8sService nodes; resolver should ignore them.
	nodes := []*core.DbNode{
		{NodeType: core.NodeTypeDatabase, Properties: map[string]interface{}{
			"name":       "rds-pg",
			"cluster_ip": "10.0.0.10",
			"cluster":    "prod",
		}},
		{NodeType: core.NodeTypePod, Properties: map[string]interface{}{
			"name":       "some-pod",
			"cluster_ip": "10.0.0.11",
			"cluster":    "prod",
		}},
		makeServiceNode("real", "prod", "10.0.0.10"),
	}
	r := NewK8sServiceIPResolver(nodes)

	got, ok := r.Resolve("prod", "10.0.0.10")
	if !ok || got.Properties["name"] != "real" {
		t.Errorf("expected K8s Service hit, got %v ok=%v", got, ok)
	}
}

func TestK8sServiceIPResolver_ServiceWithoutClusterFallsBackToGlobalIndex(t *testing.T) {
	// A K8s Service node lacking the "cluster" property still gets indexed in
	// byIPAcrossClusters so it remains resolvable when the caller has no cluster.
	nodes := []*core.DbNode{
		{NodeType: core.NodeTypeK8sService, Properties: map[string]interface{}{
			"name":       "no-cluster-svc",
			"cluster_ip": "172.16.0.1",
			// no "cluster" key
		}},
	}
	r := NewK8sServiceIPResolver(nodes)

	got, ok := r.Resolve("", "172.16.0.1")
	if !ok || got.Properties["name"] != "no-cluster-svc" {
		t.Errorf("unique-IP global lookup should succeed, got %v ok=%v", got, ok)
	}
}

func TestResolveIPToK8sService_SameClusterHit(t *testing.T) {
	nodes := []*core.DbNode{
		makeServiceNode("services-server", "k8s-dev", "34.118.228.207"),
	}
	r := NewK8sServiceIPResolver(nodes)

	got, reason, ok := ResolveIPToK8sService("34.118.228.207", "k8s-dev", r)
	if !ok {
		t.Fatalf("expected hit")
	}
	if reason != IPResolutionReasonSameCluster {
		t.Errorf("expected reason=same_cluster, got %q", reason)
	}
	if got.Properties["name"] != "services-server" {
		t.Errorf("expected services-server, got %v", got.Properties["name"])
	}
}

func TestResolveIPToK8sService_PortStripped(t *testing.T) {
	nodes := []*core.DbNode{makeServiceNode("svc", "c", "10.0.0.5")}
	r := NewK8sServiceIPResolver(nodes)

	got, _, ok := ResolveIPToK8sService("10.0.0.5:8000", "c", r)
	if !ok || got.Properties["name"] != "svc" {
		t.Errorf("expected port-stripped hit, got %v ok=%v", got, ok)
	}
}

func TestResolveIPToK8sService_IPv6PortStripped(t *testing.T) {
	nodes := []*core.DbNode{makeServiceNode("v6svc", "c", "fd00::1")}
	r := NewK8sServiceIPResolver(nodes)

	got, _, ok := ResolveIPToK8sService("[fd00::1]:8443", "c", r)
	if !ok || got.Properties["name"] != "v6svc" {
		t.Errorf("expected bracketed-IPv6 hit, got %v ok=%v", got, ok)
	}
}

func TestResolveIPToK8sService_BareIPv6(t *testing.T) {
	nodes := []*core.DbNode{makeServiceNode("v6svc", "c", "fd00::1")}
	r := NewK8sServiceIPResolver(nodes)

	got, _, ok := ResolveIPToK8sService("fd00::1", "c", r)
	if !ok || got.Properties["name"] != "v6svc" {
		t.Errorf("expected bare-IPv6 hit, got %v ok=%v", got, ok)
	}
}

func TestResolveIPToK8sService_GlobalUniqueFallback(t *testing.T) {
	// Caller cluster unknown; only one Service has this IP globally.
	nodes := []*core.DbNode{makeServiceNode("only", "any", "10.0.0.99")}
	r := NewK8sServiceIPResolver(nodes)

	got, reason, ok := ResolveIPToK8sService("10.0.0.99", "", r)
	if !ok {
		t.Fatalf("expected global-unique hit")
	}
	if reason != IPResolutionReasonGlobalUnique {
		t.Errorf("expected reason=global_unique, got %q", reason)
	}
	if got.Properties["name"] != "only" {
		t.Errorf("expected only-svc, got %v", got.Properties["name"])
	}
}

func TestResolveIPToK8sService_SpecialIPsRejected(t *testing.T) {
	// Build a resolver that WOULD match if asked — verifies the skip list
	// short-circuits before delegating.
	nodes := []*core.DbNode{
		makeServiceNode("loopback", "c", "127.0.0.1"),
		makeServiceNode("metadata", "c", "169.254.169.254"),
		makeServiceNode("unspec", "c", "0.0.0.0"),
		makeServiceNode("v6loop", "c", "::1"),
	}
	r := NewK8sServiceIPResolver(nodes)

	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "::1", "fe80::1"} {
		if _, _, ok := ResolveIPToK8sService(ip, "c", r); ok {
			t.Errorf("special IP %q should be rejected", ip)
		}
	}
}

func TestResolveIPToK8sService_NonIPRejected(t *testing.T) {
	r := NewK8sServiceIPResolver(nil)
	for _, name := range []string{"", "services-server.nudgebee.svc.cluster.local", "rds-host.eu-west-1.rds.amazonaws.com", "12345"} {
		if _, _, ok := ResolveIPToK8sService(name, "c", r); ok {
			t.Errorf("non-IP %q should be rejected", name)
		}
	}
}

func TestResolveIPToK8sService_NilResolver(t *testing.T) {
	if _, _, ok := ResolveIPToK8sService("10.0.0.1", "c", nil); ok {
		t.Errorf("nil resolver should return false")
	}
}

func TestK8sServiceIPResolver_NilSafety(t *testing.T) {
	r := NewK8sServiceIPResolver(nil)
	if _, ok := r.Resolve("any", "1.2.3.4"); ok {
		t.Errorf("empty resolver should return false")
	}

	var nilResolver *K8sServiceIPResolver
	if _, ok := nilResolver.Resolve("any", "1.2.3.4"); ok {
		t.Errorf("nil resolver should return false")
	}

	// Nil node in input shouldn't panic.
	r2 := NewK8sServiceIPResolver([]*core.DbNode{nil, makeServiceNode("ok", "c", "10.0.0.1")})
	if _, ok := r2.Resolve("c", "10.0.0.1"); !ok {
		t.Errorf("expected hit after skipping nil")
	}
}
