package k8s

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func testBuildReq() *core.SourceBuildRequest {
	return &core.SourceBuildRequest{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}
}

// countByType tallies nodes per NodeType so assertions can talk about
// "one Namespace node" without depending on slice ordering.
func countByType(nodes []*core.DbNode) map[core.NodeType]int {
	counts := make(map[core.NodeType]int)
	for _, n := range nodes {
		counts[n.NodeType]++
	}
	return counts
}

// TestEnsureNamespaceNode_MintsWhenAbsent covers the case this change exists
// for: a namespace no earlier converter produced. Both the Namespace and its
// Cluster must be minted, wired, and registered in the caller's maps.
func TestEnsureNamespaceNode_MintsWhenAbsent(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()
	namespaceNodes := map[string]*core.DbNode{}
	clusterNodes := map[string]*core.DbNode{}

	nsNode, newNodes, newEdges := src.ensureNamespaceNode("kube-node-lease", "cluster-1", namespaceNodes, clusterNodes, req)

	if nsNode == nil {
		t.Fatal("expected a Namespace node, got nil")
	}
	if nsNode.NodeType != core.NodeTypeNamespace {
		t.Errorf("expected NodeType %q, got %q", core.NodeTypeNamespace, nsNode.NodeType)
	}
	if name, _ := core.GetNodePropertyString(nsNode, "name"); name != "kube-node-lease" {
		t.Errorf("expected name %q, got %q", "kube-node-lease", name)
	}
	if cluster, _ := core.GetNodePropertyString(nsNode, "cluster"); cluster != "cluster-1" {
		t.Errorf("expected cluster %q, got %q", "cluster-1", cluster)
	}

	counts := countByType(newNodes)
	if counts[core.NodeTypeNamespace] != 1 || counts[core.NodeTypeCluster] != 1 || len(newNodes) != 2 {
		t.Errorf("expected exactly one Namespace + one Cluster node, got %d nodes %v", len(newNodes), counts)
	}

	if len(newEdges) != 1 {
		t.Fatalf("expected 1 Namespace→Cluster edge, got %d", len(newEdges))
	}
	edge := newEdges[0]
	if edge.SourceNodeID != nsNode.ID {
		t.Error("Namespace→Cluster edge does not originate at the namespace node")
	}
	if edge.RelationshipType != core.RelationshipRunsOn {
		t.Errorf("expected relationship %q, got %q", core.RelationshipRunsOn, edge.RelationshipType)
	}
	if edge.DestinationNodeID != clusterNodes["cluster-1"].ID {
		t.Error("Namespace→Cluster edge does not terminate at the cluster node")
	}

	// Both maps must be updated so later resources reuse rather than re-mint.
	if got := namespaceNodes["cluster-1/kube-node-lease"]; got != nsNode {
		t.Error("namespaceNodes was not updated with the minted node")
	}
	if _, ok := clusterNodes["cluster-1"]; !ok {
		t.Error("clusterNodes was not updated with the minted cluster")
	}
}

// TestEnsureNamespaceNode_ReusesWhenPresent guards the common path — the
// namespace already exists because a workload minted it — where the helper
// must be a pure lookup and emit nothing.
func TestEnsureNamespaceNode_ReusesWhenPresent(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()
	existing := src.createNamespaceNode("production", "cluster-1", req)
	namespaceNodes := map[string]*core.DbNode{"cluster-1/production": existing}
	clusterNodes := map[string]*core.DbNode{}

	nsNode, newNodes, newEdges := src.ensureNamespaceNode("production", "cluster-1", namespaceNodes, clusterNodes, req)

	if nsNode != existing {
		t.Error("expected the pre-existing namespace node to be returned")
	}
	if len(newNodes) != 0 || len(newEdges) != 0 {
		t.Errorf("expected nothing to be emitted, got %d nodes and %d edges", len(newNodes), len(newEdges))
	}
	if len(clusterNodes) != 0 {
		t.Error("expected no cluster node to be minted on the reuse path")
	}
}

// TestEnsureNamespaceNode_ReusesExistingCluster: a second namespace on a
// known cluster must attach to that cluster node rather than mint a rival.
func TestEnsureNamespaceNode_ReusesExistingCluster(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()
	cluster := src.createClusterNode("cluster-1", req)
	namespaceNodes := map[string]*core.DbNode{}
	clusterNodes := map[string]*core.DbNode{"cluster-1": cluster}

	nsNode, newNodes, newEdges := src.ensureNamespaceNode("kube-public", "cluster-1", namespaceNodes, clusterNodes, req)

	if len(newNodes) != 1 || newNodes[0] != nsNode {
		t.Errorf("expected only the namespace node to be minted, got %d nodes", len(newNodes))
	}
	if len(newEdges) != 1 || newEdges[0].DestinationNodeID != cluster.ID {
		t.Error("expected the edge to point at the pre-existing cluster node")
	}
	if len(clusterNodes) != 1 {
		t.Errorf("expected clusterNodes to stay at 1 entry, got %d", len(clusterNodes))
	}
}

// TestEnsureNamespaceNode_NoCluster covers the account that carries no
// workload with a cluster name at all: mint the namespace, but do not point
// an edge at an empty-named Cluster node.
func TestEnsureNamespaceNode_NoCluster(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()
	namespaceNodes := map[string]*core.DbNode{}
	clusterNodes := map[string]*core.DbNode{}

	nsNode, newNodes, newEdges := src.ensureNamespaceNode("orphan-ns", "", namespaceNodes, clusterNodes, req)

	if nsNode == nil {
		t.Fatal("expected the namespace to still be minted without a cluster")
	}
	if len(newNodes) != 1 {
		t.Errorf("expected only the namespace node, got %d nodes", len(newNodes))
	}
	if len(newEdges) != 0 {
		t.Errorf("expected no cluster edge, got %d", len(newEdges))
	}
	if len(clusterNodes) != 0 {
		t.Error("expected no Cluster node to be minted for an empty cluster name")
	}
}

// TestConvertK8sSecretsToGraph_MintsMissingNamespace is the regression this
// change fixes end-to-end: before minting, a Secret in a namespace holding no
// workloads/Services/PVCs lost its BELONGS_TO edge and landed in the graph as
// an orphan.
func TestConvertK8sSecretsToGraph_MintsMissingNamespace(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()

	// A workload in `production` only — the Secret lives in `cert-manager`,
	// which nothing else in the build touches.
	workloads := []K8sWorkloadRow{{
		Kind: "Deployment", Namespace: "production", Name: "api", ClusterName: "cluster-1", IsActive: true,
	}}
	secrets := []K8sSecretFromRelay{{
		Metadata: K8sServiceMetadata{Name: "issuer-key", Namespace: "cert-manager"},
		Type:     "Opaque",
	}}

	namespaceNodes := map[string]*core.DbNode{}
	clusterNodes := map[string]*core.DbNode{}
	nodes, edges, byKey := src.convertK8sSecretsToGraph(secrets, workloads, clusterNodes, namespaceNodes, req)

	counts := countByType(nodes)
	if counts[core.NodeTypeK8sSecret] != 1 {
		t.Errorf("expected 1 Secret node, got %d", counts[core.NodeTypeK8sSecret])
	}
	if counts[core.NodeTypeNamespace] != 1 {
		t.Errorf("expected the cert-manager namespace to be minted, got %d Namespace nodes", counts[core.NodeTypeNamespace])
	}

	secretNode := byKey["cert-manager/issuer-key"]
	if secretNode == nil {
		t.Fatal("expected the secret to be registered in the lookup map")
	}
	nsNode := namespaceNodes["cluster-1/cert-manager"]
	if nsNode == nil {
		t.Fatal("expected the minted namespace to be registered under the fallback cluster")
	}

	var found bool
	for _, e := range edges {
		if e.SourceNodeID == secretNode.ID && e.DestinationNodeID == nsNode.ID && e.RelationshipType == core.RelationshipBelongsTo {
			found = true
		}
	}
	if !found {
		t.Error("expected a Secret → BELONGS_TO → Namespace edge")
	}
}

// TestNamespaceMintedTwiceDeduplicates is the safety property the design rests
// on: a namespace minted by two converters carries the same unique key and
// therefore the same node ID, so DeduplicateNodes collapses the pair without
// orphaning either converter's edge.
func TestNamespaceMintedTwiceDeduplicates(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()

	workloads := []K8sWorkloadRow{{
		Kind: "Deployment", Namespace: "production", Name: "api", ClusterName: "cluster-1", IsActive: true,
	}}
	secrets := []K8sSecretFromRelay{{
		Metadata: K8sServiceMetadata{Name: "api-creds", Namespace: "production"},
		Type:     "Opaque",
	}}

	k8sNodeMap := map[string]*core.DbNode{}
	wlNodes, wlEdges, _, wlNamespaceMap, _ := src.convertWorkloadsToGraph(workloads, &k8sNodeMap, map[string]string{}, req)

	// Deliberately hand the Secret converter *empty* maps so it re-mints the
	// same namespace instead of reusing the workload's.
	secNodes, secEdges, _ := src.convertK8sSecretsToGraph(secrets, workloads, map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)

	wlNamespace := wlNamespaceMap["cluster-1/production"]
	if wlNamespace == nil {
		t.Fatal("expected the workload converter to mint the production namespace")
	}

	deduped := core.DeduplicateNodes(append(append([]*core.DbNode{}, wlNodes...), secNodes...))
	if got := countByType(deduped)[core.NodeTypeNamespace]; got != 1 {
		t.Fatalf("expected the duplicate namespace to collapse to 1 node, got %d", got)
	}

	// Every edge from either converter must still resolve to a surviving node.
	surviving := make(map[string]bool, len(deduped))
	for _, n := range deduped {
		surviving[n.ID] = true
	}
	for _, e := range append(append([]*core.DbEdge{}, wlEdges...), secEdges...) {
		if !surviving[e.SourceNodeID] || !surviving[e.DestinationNodeID] {
			t.Errorf("edge %s → %s (%s) dangles after dedup", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType)
		}
	}
}

// namespaceClusterEdges returns the edges that link the named namespace to the
// named cluster, whatever their type. Node IDs are derived from unique keys, and
// `cluster` is not part of a Namespace's key, so both IDs can be built directly.
func namespaceClusterEdges(src *K8sSource, edges []*core.DbEdge, namespace, cluster string) []*core.DbEdge {
	req := testBuildReq()
	nsID := src.createNamespaceNode(namespace, "", req).ID
	clusterID := src.createClusterNode(cluster, req).ID
	var out []*core.DbEdge
	for _, e := range edges {
		if e.SourceNodeID == nsID && e.DestinationNodeID == clusterID {
			out = append(out, e)
		}
	}
	return out
}

// TestNamespaceClusterEdge_RelayOnlyNamespaceRunsOn is the defect behind the
// change: a namespace holding no workloads is minted by a relay converter, and
// relay converters used to link it to its cluster with BELONGS_TO. The LLM's
// cluster lookup and blast radius walk only RUNS_ON, so such a namespace was
// unreachable from its cluster. Each relay mint site must now write RUNS_ON.
func TestNamespaceClusterEdge_RelayOnlyNamespaceRunsOn(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()

	// The only workload is elsewhere; it gives the relay converters a cluster
	// name for `monitoring` through the account-wide fallback.
	workloads := []K8sWorkloadRow{{
		Kind: "Deployment", Namespace: "production", Name: "api", ClusterName: "cluster-1", IsActive: true,
	}}
	meta := K8sServiceMetadata{Name: "x", Namespace: "monitoring"}

	cases := map[string]func() []*core.DbEdge{
		"Service": func() []*core.DbEdge {
			_, edges, _, _ := src.convertK8sServicesToGraph([]K8sServiceFromRelay{{Metadata: meta}}, workloads,
				map[string]*core.DbNode{}, map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
			return edges
		},
		"PersistentVolumeClaim": func() []*core.DbEdge {
			_, edges, _, _ := src.convertK8sPVCsToGraph([]K8sPVCFromRelay{{Metadata: meta}}, workloads, nil,
				map[string]*core.DbNode{}, map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
			return edges
		},
		"ConfigMap": func() []*core.DbEdge {
			_, edges, _ := src.convertK8sConfigMapsToGraph([]K8sConfigMapFromRelay{{Metadata: meta}}, workloads,
				map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
			return edges
		},
	}

	for kind, convert := range cases {
		t.Run(kind, func(t *testing.T) {
			got := namespaceClusterEdges(src, convert(), "monitoring", "cluster-1")
			if len(got) != 1 {
				t.Fatalf("expected 1 monitoring → cluster-1 edge, got %d", len(got))
			}
			if got[0].RelationshipType != core.RelationshipRunsOn {
				t.Errorf("expected %q, got %q", core.RelationshipRunsOn, got[0].RelationshipType)
			}
		})
	}
}

// TestNamespaceClusterEdge_OneEdgeWhicheverConverterMints pins the property the
// single construction point exists for. When a workload, a Service, a PVC and a
// ConfigMap all live in one namespace, each converter may append its own copy of
// the Namespace → Cluster edge. The copies must be identical, so the per-source
// DeduplicateEdges leaves exactly one, of type RUNS_ON, in whatever order the
// converters ran. Two types for the pair would both survive DeduplicateEdges,
// and the pair-keyed phase-3 dedup would then keep whichever came first.
func TestNamespaceClusterEdge_OneEdgeWhicheverConverterMints(t *testing.T) {
	src := newTestSource(t)
	req := testBuildReq()

	workloads := []K8sWorkloadRow{{
		Kind: "Deployment", Namespace: "production", Name: "api", ClusterName: "cluster-1", IsActive: true,
	}}
	meta := K8sServiceMetadata{Name: "api", Namespace: "production"}

	workloadEdges := func() []*core.DbEdge {
		k8sNodeMap := map[string]*core.DbNode{}
		_, edges, _, _, _ := src.convertWorkloadsToGraph(workloads, &k8sNodeMap, map[string]string{}, req)
		return edges
	}
	relayEdges := func() []*core.DbEdge {
		// Fresh maps per converter, so every one of them mints the namespace and
		// appends its own Namespace → Cluster edge rather than reusing another's.
		_, svc, _, _ := src.convertK8sServicesToGraph([]K8sServiceFromRelay{{Metadata: meta}}, workloads,
			map[string]*core.DbNode{}, map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
		_, pvc, _, _ := src.convertK8sPVCsToGraph([]K8sPVCFromRelay{{Metadata: meta}}, workloads, nil,
			map[string]*core.DbNode{}, map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
		_, cm, _ := src.convertK8sConfigMapsToGraph([]K8sConfigMapFromRelay{{Metadata: meta}}, workloads,
			map[string]*core.DbNode{}, map[string]*core.DbNode{}, req)
		return append(append(svc, pvc...), cm...)
	}

	orders := map[string][]*core.DbEdge{
		"workloads first": append(workloadEdges(), relayEdges()...),
		"relay first":     append(relayEdges(), workloadEdges()...),
	}

	var firstID string
	for name, edges := range orders {
		got := namespaceClusterEdges(src, core.DeduplicateEdges(edges), "production", "cluster-1")
		if len(got) != 1 {
			t.Fatalf("%s: expected 1 production → cluster-1 edge after DeduplicateEdges, got %d", name, len(got))
		}
		if got[0].RelationshipType != core.RelationshipRunsOn {
			t.Errorf("%s: expected %q, got %q", name, core.RelationshipRunsOn, got[0].RelationshipType)
		}
		if firstID == "" {
			firstID = got[0].ID
		} else if got[0].ID != firstID {
			t.Errorf("%s: surviving edge ID %s differs from the other order's %s", name, got[0].ID, firstID)
		}
	}
}
