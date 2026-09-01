package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

// Two cloud accounts under one tenant, each a K8s cluster running an
// identically-named deployment in an identically-named namespace. This is the
// shape that produced a false prod->dev CALLS edge in production
// (nudgebee/ml-k8s-server existed in both k8s-prod and k8s-dev).
const (
	acctProd = "acct-prod"
	acctDev  = "acct-dev"
)

// workloadNodeInAccount mirrors makeWorkloadNode but binds the node to a cloud
// account, which is what the resolvers scope on. The plain helper leaves
// CloudAccountID empty, and "empty matches empty" keeps those fixtures valid.
func workloadNodeInAccount(id, account, kind, name, namespace, cluster string) *core.DbNode {
	n := makeWorkloadNode(kind, name, namespace, cluster)
	n.ID = id
	n.CloudAccountID = account
	return n
}

// TestIndexWorkloadsByOwner_CrossAccountCollisionScopedToCallerAccount is the
// regression test for the false cross-cluster CALLS edge.
//
// Both accounts run nudgebee/Deployment/ml-k8s-server, so both collapse onto the
// same cluster-less index key. GetNodesByTenant returns nodes created_at DESC,
// so the prod node (created later) is seen first and the dev node last — and
// last-write-wins previously handed every prod pod the *dev* Workload node.
func TestIndexWorkloadsByOwner_CrossAccountCollisionScopedToCallerAccount(t *testing.T) {
	prod := workloadNodeInAccount("prod-wl", acctProd, "Deployment", "ml-k8s-server", "nudgebee", "k8s-prod")
	dev := workloadNodeInAccount("dev-wl", acctDev, "Deployment", "ml-k8s-server", "nudgebee", "k8s-dev")

	// created_at DESC ordering: prod first, dev last.
	nodes := []*core.DbNode{prod, dev}

	// cluster="" is the production call: addInventoryPodName has no cluster
	// column in public.k8s_pods, so it always takes the cluster-less path.
	got, ok := indexWorkloadsByOwner(nodes, acctProd).lookup("", "nudgebee", "Deployment", "ml-k8s-server", "")
	if !ok {
		t.Fatal("prod account: expected the cluster-less fallback to resolve within the account")
	}
	if got.ID != prod.ID {
		t.Errorf("prod account resolved to %q (account %q); want %q", got.ID, got.CloudAccountID, prod.ID)
	}

	// Symmetric: the dev account must get its own node, not whichever was last written.
	got, ok = indexWorkloadsByOwner(nodes, acctDev).lookup("", "nudgebee", "Deployment", "ml-k8s-server", "")
	if !ok {
		t.Fatal("dev account: expected the cluster-less fallback to resolve within the account")
	}
	if got.ID != dev.ID {
		t.Errorf("dev account resolved to %q (account %q); want %q", got.ID, got.CloudAccountID, dev.ID)
	}
}

// TestIndexWorkloadsByOwner_ForeignAccountNodesAreNotIndexed pins the
// fail-closed direction: when the only candidate belongs to another account the
// resolver must refuse rather than fall back to it.
func TestIndexWorkloadsByOwner_ForeignAccountNodesAreNotIndexed(t *testing.T) {
	dev := workloadNodeInAccount("dev-wl", acctDev, "Deployment", "ml-k8s-server", "nudgebee", "k8s-dev")

	if _, ok := indexWorkloadsByOwner([]*core.DbNode{dev}, acctProd).lookup("", "nudgebee", "Deployment", "ml-k8s-server", ""); ok {
		t.Error("a workload from another account must not resolve for the prod account")
	}
}

// TestResolvers_NeverReturnNodeFromAnotherAccount is the class-level guard.
//
// Every resolver in this package has, at some point, resolved identity from too
// few key components and landed on a same-named node from somewhere else:
// namespace was added as a discriminator, then cluster, and cloud account never
// was. This test exists so that hole cannot be reopened in any of the three
// resolvers — each is given a node that belongs to acctDev only, and asked to
// resolve as acctProd. All must refuse.
//
// Each case deliberately makes the target unambiguous tenant-wide, which is
// exactly the condition under which the "globally unique" fallbacks used to fire.
func TestResolvers_NeverReturnNodeFromAnotherAccount(t *testing.T) {
	const ip = "172.31.5.25"

	svcDev := makeServiceNode("loki", "k8s-dev", ip)
	svcDev.ID = "dev-svc"
	svcDev.CloudAccountID = acctDev

	nodeDev := makeK8sNode("ip-dev", "k8s-dev", ip)
	nodeDev.ID = "dev-node"
	nodeDev.CloudAccountID = acctDev

	wlDev := workloadNodeInAccount("dev-wl", acctDev, "Deployment", "ml-k8s-server", "nudgebee", "k8s-dev")

	tests := []struct {
		name string
		// resolve reports whether the resolver returned anything for a caller
		// in acctProd, and which node it was.
		resolve func() (*core.DbNode, bool)
	}{
		{
			name: "K8sServiceIPResolver/global-unique ClusterIP owned by another account",
			resolve: func() (*core.DbNode, bool) {
				return NewK8sServiceIPResolver([]*core.DbNode{svcDev}).Resolve(acctProd, "", ip)
			},
		},
		{
			name: "K8sServiceIPResolver/same-cluster hit owned by another account",
			resolve: func() (*core.DbNode, bool) {
				return NewK8sServiceIPResolver([]*core.DbNode{svcDev}).Resolve(acctProd, "k8s-dev", ip)
			},
		},
		{
			name: "K8sNodeIPResolver/global-unique node IP owned by another account",
			resolve: func() (*core.DbNode, bool) {
				return NewK8sNodeIPResolver([]*core.DbNode{nodeDev}).Resolve(acctProd, "", ip)
			},
		},
		{
			name: "K8sNodeIPResolver/same-cluster hit owned by another account",
			resolve: func() (*core.DbNode, bool) {
				return NewK8sNodeIPResolver([]*core.DbNode{nodeDev}).Resolve(acctProd, "k8s-dev", ip)
			},
		},
		{
			name: "PodIPResolver/cluster-less workload lookup owned by another account",
			resolve: func() (*core.DbNode, bool) {
				idx := indexWorkloadsByOwner([]*core.DbNode{wlDev}, acctProd)
				return idx.lookup("", "nudgebee", "Deployment", "ml-k8s-server", "")
			},
		},
	}

	for _, tt := range tests {
		got, ok := tt.resolve()
		if ok {
			t.Errorf("%s: resolved to node %q in account %q; a caller in %q must never be given another account's node",
				tt.name, got.ID, got.CloudAccountID, acctProd)
		}
	}
}

// TestResolvers_AccountScopingDisambiguatesSharedIP is the other half of the
// guard: account scoping should turn a previously-refused ambiguous lookup into
// a correct hit. The same IP exists in both accounts, which used to be
// "ambiguous, refuse to guess" — with the account known it is unambiguous.
func TestResolvers_AccountScopingDisambiguatesSharedIP(t *testing.T) {
	const ip = "10.0.0.1"

	svcProd := makeServiceNode("loki", "k8s-prod", ip)
	svcProd.ID = "prod-svc"
	svcProd.CloudAccountID = acctProd

	svcDev := makeServiceNode("loki", "k8s-dev", ip)
	svcDev.ID = "dev-svc"
	svcDev.CloudAccountID = acctDev

	r := NewK8sServiceIPResolver([]*core.DbNode{svcProd, svcDev})

	got, ok := r.Resolve(acctProd, "", ip)
	if !ok {
		t.Fatal("an IP ambiguous tenant-wide but unique within the account should resolve")
	}
	if got.ID != svcProd.ID {
		t.Errorf("resolved to %q; want %q", got.ID, svcProd.ID)
	}

	// Without account context the IP really is ambiguous, so the old contract holds.
	if _, ok := r.Resolve("", "", ip); ok {
		t.Error("with no account and no cluster context an ambiguous IP must still refuse to guess")
	}
}
