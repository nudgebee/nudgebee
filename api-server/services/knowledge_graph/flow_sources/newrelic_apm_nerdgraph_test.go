package flow_sources

import (
	"errors"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

// helpers --------------------------------------------------------------------

func newNRSourceWithNodes(nodes []*core.DbNode) *NewRelicAPMFlowSource {
	src := NewNewRelicAPMFlowSource(slog.Default())
	src.InitializeNodeMatcher(nodes)
	return src
}

func k8sServiceNode(id, name, cluster, namespace, ip string) *core.DbNode {
	return &core.DbNode{
		ID:             id,
		NodeType:       core.NodeTypeK8sService,
		CloudAccountID: "acct-cluster",
		Properties: map[string]interface{}{
			"name":       name,
			"cluster":    cluster,
			"namespace":  namespace,
			"cluster_ip": ip,
		},
	}
}

func serviceNode(id, name, accountID string) *core.DbNode {
	return &core.DbNode{
		ID:             id,
		NodeType:       core.NodeTypeService,
		CloudAccountID: accountID,
		Properties:     map[string]interface{}{"name": name},
	}
}

func workloadNode(id, name, namespace, accountID string) *core.DbNode {
	return &core.DbNode{
		ID:             id,
		NodeType:       core.NodeTypeWorkload,
		CloudAccountID: accountID,
		Properties: map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}
}

// nerdGraphTargetKind --------------------------------------------------------

func TestNerdGraphTargetKind(t *testing.T) {
	tests := []struct {
		entityType string
		want       string
	}{
		{"THIRD_PARTY_SERVICE_ENTITY", "peer_service"},
		{"EBPFSERVER", "hostname"},
		{"KUBERNETES_POD", "workload"},
		{"KUBERNETES_DEPLOYMENT", "workload"},
		{"OTHER_WEIRD_TYPE", "other_weird_type"},
	}
	for _, tt := range tests {
		if got := nerdGraphTargetKind(tt.entityType); got != tt.want {
			t.Errorf("nerdGraphTargetKind(%q) = %q, want %q", tt.entityType, got, tt.want)
		}
	}
}

// buildNerdGraphEdgeProperties ----------------------------------------------

func TestBuildNerdGraphEdgeProperties_ThirdPartyService(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	rel := NerdGraphRelationship{
		CallerGUID: "g-cart", CallerName: "cart", CallerType: "THIRD_PARTY_SERVICE_ENTITY",
		TargetGUID: "g-checkout", TargetName: "checkout", TargetType: "THIRD_PARTY_SERVICE_ENTITY",
	}
	props := src.buildNerdGraphEdgeProperties(rel, "", "8031798")

	assertProp(t, props, "nr_source_service", "cart")
	assertProp(t, props, "nr_target_identifier", "checkout")
	assertProp(t, props, "nr_target_kind", "peer_service")
	assertProp(t, props, "nr_target_entity_type", "THIRD_PARTY_SERVICE_ENTITY")
	assertProp(t, props, "nr_caller_entity_guid", "g-cart")
	assertProp(t, props, "nr_target_entity_guid", "g-checkout")
	assertProp(t, props, "nr_strategy", nrStrategyNerdGraph)

	if _, ok := props["nr_request_count"]; ok {
		t.Errorf("nr_request_count must be omitted on NerdGraph rows, got %v", props["nr_request_count"])
	}
	if _, ok := props["nr_target_resolved_fqdn"]; ok {
		t.Errorf("nr_target_resolved_fqdn should be absent for non-EBPFSERVER targets")
	}
}

func TestBuildNerdGraphEdgeProperties_EBPFServerStampsFQDN(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	rel := NerdGraphRelationship{
		CallerName: "cart", CallerType: "THIRD_PARTY_SERVICE_ENTITY",
		TargetName: "10.0.0.5/6379/redis.cart.svc.cluster.local", TargetType: "EBPFSERVER",
	}
	props := src.buildNerdGraphEdgeProperties(rel, "redis.cart.svc.cluster.local", "8031798")
	assertProp(t, props, "nr_target_resolved_fqdn", "redis.cart.svc.cluster.local")
	assertProp(t, props, "nr_target_kind", "hostname")
	assertProp(t, props, "nr_target_entity_type", "EBPFSERVER")
}

// resolveNerdGraphTarget ----------------------------------------------------

func TestResolveNerdGraphTarget_ThirdPartyServiceMatchesByName(t *testing.T) {
	src := newNRSourceWithNodes([]*core.DbNode{
		serviceNode("svc-checkout", "checkout-api", "acct-1"),
	})
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{
		TargetName: "checkout-api", TargetType: "THIRD_PARTY_SERVICE_ENTITY",
	}
	got, fqdn := src.resolveNerdGraphTarget(rel, "", "acct-1", resolver)
	if got == nil || got.ID != "svc-checkout" {
		t.Fatalf("want svc-checkout, got %v", got)
	}
	if fqdn != "" {
		t.Errorf("fqdn should be empty for THIRD_PARTY_SERVICE_ENTITY, got %q", fqdn)
	}
}

func TestResolveNerdGraphTarget_EBPFServerFQDNHit(t *testing.T) {
	// FQDN match should win — no IP resolver lookup needed.
	src := newNRSourceWithNodes([]*core.DbNode{
		k8sServiceNode("svc-redis", "argocd-redis", "cluster-a", "argocd", "10.0.0.5"),
	})
	// IP resolver knows nothing; must hit FQDN strategy.
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{
		TargetName: "10.0.0.5/6379/argocd-redis.argocd.svc.cluster.local",
		TargetType: "EBPFSERVER",
	}
	got, fqdn := src.resolveNerdGraphTarget(rel, "cluster-a", "acct-cluster", resolver)
	if got == nil || got.ID != "svc-redis" {
		t.Fatalf("want svc-redis via FQDN, got %v", got)
	}
	if fqdn != "argocd-redis.argocd.svc.cluster.local" {
		t.Errorf("fqdn: want %q, got %q", "argocd-redis.argocd.svc.cluster.local", fqdn)
	}
}

func TestResolveNerdGraphTarget_EBPFServerIPFallback(t *testing.T) {
	// FQDN miss (node has different name), but IP resolver hits.
	nodes := []*core.DbNode{
		k8sServiceNode("svc-redis", "different-name", "cluster-a", "other", "10.0.0.5"),
	}
	src := newNRSourceWithNodes(nodes)
	resolver := NewK8sServiceIPResolver(nodes)
	rel := NerdGraphRelationship{
		TargetName: "10.0.0.5/6379/argocd-redis.argocd.svc.cluster.local",
		TargetType: "EBPFSERVER",
	}
	got, fqdn := src.resolveNerdGraphTarget(rel, "cluster-a", "acct-cluster", resolver)
	if got == nil || got.ID != "svc-redis" {
		t.Fatalf("want svc-redis via IP fallback, got %v", got)
	}
	// FQDN should still be propagated for the property even when matching via IP.
	if fqdn != "argocd-redis.argocd.svc.cluster.local" {
		t.Errorf("fqdn: want propagated, got %q", fqdn)
	}
}

func TestResolveNerdGraphTarget_EBPFServerBothMissReturnsNil(t *testing.T) {
	src := newNRSourceWithNodes(nil)
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{
		TargetName: "10.0.0.5/6379/something.unknown.svc.cluster.local",
		TargetType: "EBPFSERVER",
	}
	if got, _ := src.resolveNerdGraphTarget(rel, "cluster-a", "acct-cluster", resolver); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestResolveNerdGraphTarget_EBPFServerUnparseableSkips(t *testing.T) {
	src := newNRSourceWithNodes(nil)
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{TargetName: "bad-input", TargetType: "EBPFSERVER"}
	if got, _ := src.resolveNerdGraphTarget(rel, "", "acct-1", resolver); got != nil {
		t.Errorf("unparseable EBPFSERVER should skip, got %v", got)
	}
}

func TestResolveNerdGraphTarget_KubernetesPodMatchesWorkload(t *testing.T) {
	src := newNRSourceWithNodes([]*core.DbNode{
		workloadNode("wk-cart", "cart", "default", "acct-1"),
	})
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{TargetName: "cart", TargetType: "KUBERNETES_POD"}
	got, _ := src.resolveNerdGraphTarget(rel, "", "acct-1", resolver)
	if got == nil || got.ID != "wk-cart" {
		t.Errorf("want wk-cart, got %v", got)
	}
}

func TestResolveNerdGraphTarget_UnknownTypeReturnsNil(t *testing.T) {
	src := newNRSourceWithNodes(nil)
	resolver := NewK8sServiceIPResolver(nil)
	rel := NerdGraphRelationship{TargetName: "anything", TargetType: "WHATEVER_ENTITY"}
	if got, _ := src.resolveNerdGraphTarget(rel, "", "acct-1", resolver); got != nil {
		t.Errorf("unknown entityType should skip, got %v", got)
	}
}

// matchK8sInternalDNS -------------------------------------------------------

func TestMatchK8sInternalDNS_PicksK8sServiceFirst(t *testing.T) {
	src := newNRSourceWithNodes([]*core.DbNode{
		// Two candidates with the same name; K8sService should win over Workload.
		workloadNode("wk-redis", "redis", "argocd", "acct-cluster"),
		k8sServiceNode("svc-redis", "redis", "cluster-a", "argocd", "10.0.0.5"),
	})
	got := src.matchK8sInternalDNS("redis.argocd.svc.cluster.local", "acct-cluster")
	if got == nil || got.ID != "svc-redis" {
		t.Errorf("want svc-redis (NodeTypeK8sService preferred), got %v", got)
	}
}

func TestMatchK8sInternalDNS_NotInternalReturnsNil(t *testing.T) {
	src := newNRSourceWithNodes(nil)
	if got := src.matchK8sInternalDNS("api.openai.com", "acct-1"); got != nil {
		t.Errorf("external hostname should not match, got %v", got)
	}
}

// isIntegrationNotConfigured ------------------------------------------------

func TestIsIntegrationNotConfigured(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("new Relic integration not found for account: 19707e32-..."), true},
		{errors.New("failed to list New Relic integration configs: integrations: tenant id is required"), false},
		{errors.New("failed to decrypt New Relic API key: bad cipher"), false},
		{nil, false},
	}
	for _, tt := range tests {
		got := isIntegrationNotConfigured(tt.err)
		if got != tt.want {
			errStr := "<nil>"
			if tt.err != nil {
				errStr = tt.err.Error()
			}
			t.Errorf("isIntegrationNotConfigured(%q) = %v, want %v", errStr, got, tt.want)
		}
	}
}

// isTransientNerdGraphError -------------------------------------------------

func TestIsTransientNerdGraphError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("entitySearch: page 0: status 500: upstream"), true},
		{errors.New("entitySearch: page 0: status 502: bad gateway"), true},
		{errors.New("bulk fetch: batch 0-25: status 429: rate limited"), true},
		{errors.New("status 401: unauthorized"), false},
		{errors.New("decode: invalid character"), false},
		{nil, false},
	}
	for _, tt := range tests {
		errStr := "<nil>"
		if tt.err != nil {
			errStr = tt.err.Error()
		}
		if got := isTransientNerdGraphError(tt.err); got != tt.want {
			t.Errorf("isTransientNerdGraphError(%q) = %v, want %v", errStr, got, tt.want)
		}
	}
}
