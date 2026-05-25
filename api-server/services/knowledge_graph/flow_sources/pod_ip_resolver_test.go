package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"reflect"
	"testing"
)

func makeWorkloadNode(kind, name, namespace, cluster string) *core.DbNode {
	return &core.DbNode{
		NodeType: core.NodeTypeWorkload,
		Properties: map[string]interface{}{
			"name":      name,
			"kind":      kind,
			"namespace": namespace,
			"cluster":   cluster,
		},
	}
}

func makePodNode(name, namespace, cluster string) *core.DbNode {
	return &core.DbNode{
		NodeType: core.NodeTypePod,
		Properties: map[string]interface{}{
			"name":      name,
			"kind":      "Pod",
			"namespace": namespace,
			"cluster":   cluster,
		},
	}
}

// resolverWithPods constructs a PodIPResolver directly from an in-memory index
// so tests don't need to mock the Prometheus relay. The production path
// populates these fields via NewPodIPResolver from kube_pod_info responses.
func resolverWithPods(entries []struct {
	cluster string
	ip      string
	node    *core.DbNode
}) *PodIPResolver {
	r := &PodIPResolver{
		byClusterIP:        make(map[clusterIPKey]*core.DbNode),
		byIPAcrossClusters: make(map[string][]*core.DbNode),
	}
	for _, e := range entries {
		if e.cluster != "" {
			r.byClusterIP[clusterIPKey{e.cluster, e.ip}] = e.node
		}
		r.byIPAcrossClusters[e.ip] = append(r.byIPAcrossClusters[e.ip], e.node)
	}
	return r
}

func TestPodIPResolver_SameClusterHit(t *testing.T) {
	rabbit := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", rabbit},
	})

	got, ok := r.Resolve("k8s-prod", "172.31.5.25")
	if !ok {
		t.Fatalf("expected same-cluster hit for pod IP")
	}
	if got.Properties["name"] != "rabbitmq" {
		t.Errorf("expected rabbitmq, got %v", got.Properties["name"])
	}
}

func TestPodIPResolver_AmbiguousIPWithoutCallerCluster(t *testing.T) {
	// Same pod IP in two clusters — overlapping VPC CIDR, common in multi-cluster setups.
	a := makeWorkloadNode("StatefulSet", "svc-a", "ns", "cluster-a")
	b := makeWorkloadNode("StatefulSet", "svc-b", "ns", "cluster-b")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"cluster-a", "10.1.1.1", a},
		{"cluster-b", "10.1.1.1", b},
	})

	if _, ok := r.Resolve("", "10.1.1.1"); ok {
		t.Errorf("ambiguous pod IP without caller cluster should refuse to resolve")
	}
}

func TestPodIPResolver_UniqueIPWithoutCallerClusterHits(t *testing.T) {
	rabbit := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", rabbit},
	})

	got, ok := r.Resolve("", "172.31.5.25")
	if !ok || got.Properties["name"] != "rabbitmq" {
		t.Errorf("globally-unique pod IP should resolve without caller cluster, got %v ok=%v", got, ok)
	}
}

func TestPodIPResolver_NilResolverSafe(t *testing.T) {
	var r *PodIPResolver
	if _, ok := r.Resolve("any", "10.0.0.1"); ok {
		t.Errorf("nil resolver should return ok=false")
	}
	if _, _, ok := ResolveIPToPodWorkload("10.0.0.1", "any", r); ok {
		t.Errorf("ResolveIPToPodWorkload with nil resolver should return ok=false")
	}
}

func TestResolveIPToPodWorkload_StripsPort(t *testing.T) {
	rabbit := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", rabbit},
	})

	got, reason, ok := ResolveIPToPodWorkload("172.31.5.25:5672", "k8s-prod", r)
	if !ok {
		t.Fatalf("port-suffixed pod IP should still resolve")
	}
	if got.Properties["name"] != "rabbitmq" {
		t.Errorf("expected rabbitmq, got %v", got.Properties["name"])
	}
	if reason != IPResolutionReasonSameCluster {
		t.Errorf("expected same_cluster reason, got %q", reason)
	}
}

func TestResolveIPToPodWorkload_RejectsSpecialIPs(t *testing.T) {
	rabbit := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "127.0.0.1", rabbit},
		{"k8s-prod", "169.254.169.254", rabbit},
	})

	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "::1", "not-an-ip"} {
		if _, _, ok := ResolveIPToPodWorkload(ip, "k8s-prod", r); ok {
			t.Errorf("special/invalid IP %q should not resolve", ip)
		}
	}
}

func TestResolveIPToPodWorkload_GlobalUniqueReason(t *testing.T) {
	// Only one Workload globally with this pod IP. Caller cluster unknown
	// (non-K8s caller / bypass branch) — should still match via the
	// global-unique fallback path and surface the right reason.
	rabbit := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	r := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", rabbit},
	})

	_, reason, ok := ResolveIPToPodWorkload("172.31.5.25", "", r)
	if !ok {
		t.Fatalf("global-unique IP should resolve with empty caller cluster")
	}
	if reason != IPResolutionReasonGlobalUnique {
		t.Errorf("expected global_unique reason, got %q", reason)
	}
}

func TestIndexWorkloadsByOwner_BuildsWorkloadAndPodEntries(t *testing.T) {
	nodes := []*core.DbNode{
		makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod"),
		makeWorkloadNode("Deployment", "api-server", "default", "k8s-prod"),
		makePodNode("rabbitmq-0", "rabbit", "k8s-prod"),
		// noise: a non-workload node should be ignored
		{NodeType: core.NodeTypeK8sService, Properties: map[string]interface{}{"name": "svc"}},
		// noise: missing required fields
		{NodeType: core.NodeTypeWorkload, Properties: map[string]interface{}{"name": "x"}},
	}
	idx := indexWorkloadsByOwner(nodes)

	if got, ok := idx.lookup("k8s-prod", "rabbit", "StatefulSet", "rabbitmq", ""); !ok || got.Properties["name"] != "rabbitmq" {
		t.Errorf("expected StatefulSet rabbitmq lookup to hit, got=%v ok=%v", got, ok)
	}
	if got, ok := idx.lookup("k8s-prod", "default", "Deployment", "api-server", ""); !ok || got.Properties["name"] != "api-server" {
		t.Errorf("expected Deployment api-server lookup to hit, got=%v ok=%v", got, ok)
	}
	// Pod-by-name lookup when ownerKind/Name can't disambiguate (rare path).
	if got, ok := idx.lookup("k8s-prod", "rabbit", "", "", "rabbitmq-0"); !ok || got.Properties["name"] != "rabbitmq-0" {
		t.Errorf("expected pod-by-name lookup to hit, got=%v ok=%v", got, ok)
	}
	// Cluster-mismatch should still match via the cluster-less fallback —
	// kube_pod_info's k8s_cluster label can be empty if scrape config is incomplete.
	if got, ok := idx.lookup("", "rabbit", "StatefulSet", "rabbitmq", ""); !ok || got.Properties["name"] != "rabbitmq" {
		t.Errorf("expected cluster-less fallback to hit, got=%v ok=%v", got, ok)
	}
	// Truly unknown lookup misses.
	if _, ok := idx.lookup("k8s-prod", "rabbit", "StatefulSet", "ghost", ""); ok {
		t.Errorf("non-existent name should miss")
	}
}

func TestResolveOwner_ReplicaSetToDeployment(t *testing.T) {
	tests := []struct {
		createdByKind string
		createdByName string
		wantKind      string
		wantName      string
	}{
		// kube_pod_info shows the ReplicaSet, but K8sSource emits the parent
		// Deployment as the Workload — the resolver bridges that mismatch.
		{"ReplicaSet", "api-server-7d9b5c8b4f", "Deployment", "api-server"},
		// StatefulSets / DaemonSets are emitted by their own names; pass through.
		{"StatefulSet", "rabbitmq", "StatefulSet", "rabbitmq"},
		{"DaemonSet", "fluentd", "DaemonSet", "fluentd"},
		// Empty input: passthrough — caller will skip the entry.
		{"", "", "", ""},
	}
	for _, tt := range tests {
		gotKind, gotName := resolveOwner(tt.createdByKind, tt.createdByName)
		if gotKind != tt.wantKind || gotName != tt.wantName {
			t.Errorf("resolveOwner(%q, %q) = (%q, %q); want (%q, %q)",
				tt.createdByKind, tt.createdByName, gotKind, gotName, tt.wantKind, tt.wantName)
		}
	}
}

func TestExtractPodInfoMetrics_HandlesAllRelayShapes(t *testing.T) {
	want := []map[string]interface{}{
		{"pod": "rabbitmq-0", "pod_ip": "172.31.5.25"},
	}

	// Shape 1: top-level pod_info as []interface{}
	shape1 := map[string]interface{}{
		"pod_info": []interface{}{
			map[string]interface{}{"metric": want[0]},
		},
	}
	if got := extractPodInfoMetrics(shape1); !reflect.DeepEqual(got, want) {
		t.Errorf("shape 1 mismatch: got %v want %v", got, want)
	}

	// Shape 2: top-level data as []interface{}
	shape2 := map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"metric": want[0]},
		},
	}
	if got := extractPodInfoMetrics(shape2); !reflect.DeepEqual(got, want) {
		t.Errorf("shape 2 mismatch: got %v want %v", got, want)
	}

	// Shape 3: nested data.pod_info.result
	shape3 := map[string]interface{}{
		"data": map[string]interface{}{
			"pod_info": map[string]interface{}{
				"result": []interface{}{
					map[string]interface{}{"metric": want[0]},
				},
			},
		},
	}
	if got := extractPodInfoMetrics(shape3); !reflect.DeepEqual(got, want) {
		t.Errorf("shape 3 mismatch: got %v want %v", got, want)
	}

	// Empty / malformed should yield nothing without panicking.
	if got := extractPodInfoMetrics(nil); len(got) != 0 {
		t.Errorf("nil response should yield no metrics, got %v", got)
	}
	if got := extractPodInfoMetrics(map[string]interface{}{"unknown": "shape"}); len(got) != 0 {
		t.Errorf("unknown shape should yield no metrics, got %v", got)
	}
}

func TestResolveIPNamedExternalService_PrefersClusterIPOverPodIP(t *testing.T) {
	// If the IP is both a Service ClusterIP and a pod IP somewhere (it
	// shouldn't be, but defense-in-depth) the ClusterIP resolver wins because
	// it runs first. Verifies the helper's ordering contract.
	svc := makeServiceNode("api", "k8s-prod", "10.0.0.99")
	wl := makeWorkloadNode("Deployment", "api", "default", "k8s-prod")
	clusterIPResolver := NewK8sServiceIPResolver([]*core.DbNode{svc})
	podResolver := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "10.0.0.99", wl},
	})

	node, _, _, source, ok := resolveIPNamedExternalService("10.0.0.99", "", clusterIPResolver, podResolver, nil)
	if !ok {
		t.Fatalf("expected resolution to succeed")
	}
	if node.NodeType != core.NodeTypeK8sService {
		t.Errorf("expected K8sService match, got %v", node.NodeType)
	}
	if source != IPResolutionSourceClusterIP {
		t.Errorf("expected %q source, got %q", IPResolutionSourceClusterIP, source)
	}
}

func TestResolveIPNamedExternalService_FallsThroughToPodIP(t *testing.T) {
	// 172.31.5.25 is a pod IP, not a ClusterIP. ClusterIP resolver misses,
	// pod-IP resolver matches the owning Workload, and the helper reports
	// the right source.
	wl := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "k8s-prod")
	clusterIPResolver := NewK8sServiceIPResolver(nil)
	podResolver := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", wl},
	})

	node, ip, _, source, ok := resolveIPNamedExternalService("172.31.5.25", "", clusterIPResolver, podResolver, nil)
	if !ok {
		t.Fatalf("expected pod-IP fallback to resolve 172.31.5.25 to rabbitmq")
	}
	if node.Properties["name"] != "rabbitmq" {
		t.Errorf("expected rabbitmq, got %v", node.Properties["name"])
	}
	if ip != "172.31.5.25" {
		t.Errorf("expected ip 172.31.5.25 in provenance, got %q", ip)
	}
	if source != IPResolutionSourcePodIP {
		t.Errorf("expected %q source, got %q", IPResolutionSourcePodIP, source)
	}
}

func TestResolveIPNamedExternalService_FallsThroughToNodeIP(t *testing.T) {
	// Host-network destination: pod-IP resolver refuses to guess between
	// three pods sharing the node's primary IP, then the Node-IP resolver
	// attributes the destination to the K8s Node itself.
	awsNode := makeWorkloadNode("DaemonSet", "aws-node", "kube-system", "k8s-prod")
	kubeProxy := makeWorkloadNode("DaemonSet", "kube-proxy", "kube-system", "k8s-prod")
	nodeExporter := makeWorkloadNode("DaemonSet", "prometheus-node-exporter", "prometheus", "k8s-prod")
	k8sNode := makeK8sNode("ip-172-31-8-2.ec2.internal", "k8s-prod", "172.31.8.2")
	clusterIPResolver := NewK8sServiceIPResolver(nil)
	// Three host-network workloads all index against the same pod IP.
	podResolver := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.8.2", awsNode},
		{"k8s-prod", "172.31.8.2", kubeProxy},
		{"k8s-prod", "172.31.8.2", nodeExporter},
	})
	nodeIPResolver := NewK8sNodeIPResolver([]*core.DbNode{k8sNode})

	node, ip, _, source, ok := resolveIPNamedExternalService(
		"172.31.8.2", "", clusterIPResolver, podResolver, nodeIPResolver)
	if !ok {
		t.Fatalf("expected Node-IP fallback to resolve 172.31.8.2")
	}
	if node.NodeType != core.NodeTypeNode {
		t.Errorf("expected Node match, got %v", node.NodeType)
	}
	if node.Properties["name"] != "ip-172-31-8-2.ec2.internal" {
		t.Errorf("expected ip-172-31-8-2 node, got %v", node.Properties["name"])
	}
	if ip != "172.31.8.2" {
		t.Errorf("expected ip in provenance, got %q", ip)
	}
	if source != "k8s_node_ip_resolver" {
		t.Errorf("expected k8s_node_ip_resolver source, got %q", source)
	}
}

func TestResolveIPNamedExternalService_PodIPWinsOverNodeIP(t *testing.T) {
	// Pods are more specific than nodes, so when a pod IP unambiguously
	// resolves to one Workload, the chain shouldn't fall through to the
	// node resolver even if a K8sNode also matches.
	wl := makeWorkloadNode("Deployment", "api", "default", "k8s-prod")
	k8sNode := makeK8sNode("ip-172-31-5-25.ec2.internal", "k8s-prod", "172.31.5.25")
	clusterIPResolver := NewK8sServiceIPResolver(nil)
	podResolver := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"k8s-prod", "172.31.5.25", wl},
	})
	nodeIPResolver := NewK8sNodeIPResolver([]*core.DbNode{k8sNode})

	node, _, _, source, ok := resolveIPNamedExternalService(
		"172.31.5.25", "", clusterIPResolver, podResolver, nodeIPResolver)
	if !ok {
		t.Fatalf("expected resolution to succeed")
	}
	if node.NodeType != core.NodeTypeWorkload {
		t.Errorf("expected Workload (pod-IP) match to win, got %v", node.NodeType)
	}
	if source != "k8s_pod_ip_resolver" {
		t.Errorf("expected k8s_pod_ip_resolver source, got %q", source)
	}
}

func TestResolveIPNamedExternalService_NoMatchReturnsFalse(t *testing.T) {
	clusterIPResolver := NewK8sServiceIPResolver(nil)
	podResolver := resolverWithPods(nil)
	if _, _, _, _, ok := resolveIPNamedExternalService("203.0.113.1", "", clusterIPResolver, podResolver, nil); ok {
		t.Errorf("unknown IP should fall through to ok=false (caller creates ExternalService)")
	}
}

func TestResolveIPNamedExternalService_CallerClusterScopesBothResolvers(t *testing.T) {
	// Same pod IP in two clusters — used to be safe-by-ambiguity (resolver
	// refused to guess). With caller-cluster scope plumbed through the
	// traces upstream branch, the same-cluster candidate must win.
	rabbitA := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "cluster-a")
	rabbitB := makeWorkloadNode("StatefulSet", "rabbitmq", "rabbit", "cluster-b")
	podResolver := resolverWithPods([]struct {
		cluster string
		ip      string
		node    *core.DbNode
	}{
		{"cluster-a", "172.31.5.25", rabbitA},
		{"cluster-b", "172.31.5.25", rabbitB},
	})
	clusterIPResolver := NewK8sServiceIPResolver(nil)

	gotA, _, _, _, okA := resolveIPNamedExternalService("172.31.5.25", "cluster-a", clusterIPResolver, podResolver, nil)
	if !okA || gotA.Properties["cluster"] != "cluster-a" {
		t.Errorf("caller in cluster-a should resolve to cluster-a's rabbitmq, got %v ok=%v", gotA, okA)
	}

	gotB, _, _, _, okB := resolveIPNamedExternalService("172.31.5.25", "cluster-b", clusterIPResolver, podResolver, nil)
	if !okB || gotB.Properties["cluster"] != "cluster-b" {
		t.Errorf("caller in cluster-b should resolve to cluster-b's rabbitmq, got %v ok=%v", gotB, okB)
	}

	// With no caller cluster, ambiguous IP must refuse to guess.
	if _, _, _, _, ok := resolveIPNamedExternalService("172.31.5.25", "", clusterIPResolver, podResolver, nil); ok {
		t.Errorf("ambiguous IP without caller cluster should not resolve")
	}
}
