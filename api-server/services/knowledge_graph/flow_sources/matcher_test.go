package flow_sources

import (
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// nameCriteria is the shape of traces strategy 2 (name + type, same account)
// with the observed namespace threaded through.
func nameCriteria(accountID, name, observedNamespace string) MatchCriteria {
	return MatchCriteria{
		AccountID:         accountID,
		NodeType:          core.NodeTypeWorkload,
		ObservedNamespace: observedNamespace,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact},
		},
	}
}

func matchedKey(t *testing.T, m *NodeMatcher, criteria MatchCriteria) string {
	t.Helper()
	result, err := m.FindNode(criteria)
	if err != nil || !result.Matched {
		return ""
	}
	return result.Node.UniqueKey
}

func TestNamespaceGuard_RejectsCandidateInAnotherKnownNamespace(t *testing.T) {
	const acct = "acct-a"
	frontend := nsWorkloadNode("n-frontend", "frontend", "demo", acct)
	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{frontend, payments})

	if got := matchedKey(t, m, nameCriteria(acct, "payments", "demo")); got != "" {
		t.Fatalf("payments observed in known namespace demo matched %q, want no match", got)
	}
	// The comparison is case-insensitive on both sides.
	if got := matchedKey(t, m, nameCriteria(acct, "payments", "DEMO")); got != "" {
		t.Fatalf("payments observed in DEMO matched %q, want no match", got)
	}
}

func TestNamespaceGuard_AcceptsSameNamespaceAndNamespacelessCandidates(t *testing.T) {
	const acct = "acct-a"
	payments := nsWorkloadNode("n-payments", "payments", "Demo", acct)
	k8sNode := &core.DbNode{
		ID:             "n-node",
		NodeType:       core.NodeTypeNode,
		UniqueKey:      "k8s:" + acct + "::Node::worker-1",
		CloudAccountID: acct,
		Source:         "k8s",
		Properties:     map[string]interface{}{"name": "worker-1"},
	}
	m := NewNodeMatcher([]*core.DbNode{payments, k8sNode})

	if got := matchedKey(t, m, nameCriteria(acct, "payments", "demo")); got != payments.UniqueKey {
		t.Errorf("same namespace (different case): matched %q, want %q", got, payments.UniqueKey)
	}

	nodeCriteria := nameCriteria(acct, "worker-1", "demo")
	nodeCriteria.NodeType = core.NodeTypeNode
	if got := matchedKey(t, m, nodeCriteria); got != k8sNode.UniqueKey {
		t.Errorf("namespace-less candidate: matched %q, want %q", got, k8sNode.UniqueKey)
	}
}

// An OTel service.namespace is often a logical name no cluster has. Such a
// namespace is known nowhere, so it must never reject a candidate.
func TestNamespaceGuard_UnknownObservedNamespaceNeverRejects(t *testing.T) {
	const acct = "acct-a"
	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{payments})

	if got := matchedKey(t, m, nameCriteria(acct, "payments", "checkout-team")); got != payments.UniqueKey {
		t.Errorf("unknown observed namespace: matched %q, want %q", got, payments.UniqueKey)
	}

	anyAccount := nameCriteria("", "payments", "checkout-team")
	anyAccount.ObservingAccountID = "acct-b"
	if got := matchedKey(t, m, anyAccount); got != payments.UniqueKey {
		t.Errorf("unknown observed namespace, any account: matched %q, want %q", got, payments.UniqueKey)
	}
}

// A node a flow source minted carries the trace's namespace, which may be a
// logical one, and traces-minted Workloads get cloud_provider=k8s in their
// key. Only the node's source says it did not come from a cluster, so the
// fixture is built by the real minting code rather than by hand.
func TestNamespaceGuard_FlowSourceMintedNodeDoesNotMakeNamespaceKnown(t *testing.T) {
	const acct = "acct-a"
	// The fixture needs a node a flow source genuinely mints, keyed on
	// cloud_provider k8s and carrying a namespace. Since #41619 that is no
	// longer a Deployment — workload-shaped types are gated to k8s_source (see
	// isK8sAuthoritativeType) — so use an operator CRD, which flow sources are
	// the only producer of (flowProducedInfraTypes).
	ebpfSource := NewEbpfFlowSource(slog.Default())
	minted := ebpfSource.createNodeForApplication(&core.ServiceApplication{
		Id: core.ServiceApplicationId{Name: "ledger", Kind: "Elasticsearch", Namespace: "demo"},
	}, "tenant-1", core.K8sAccount{CloudAccountID: acct}, testCreationContext(true))
	if minted == nil {
		t.Fatal("createNodeForApplication returned nil for an operator CRD kind")
	}
	if cp := core.DeriveCloudProvider("ebpf", minted.NodeType); cp != core.CloudProviderK8s {
		t.Fatalf("fixture premise: minted %s node has cloud_provider %q, want k8s", minted.NodeType, cp)
	}
	if minted.Properties["namespace"] != "demo" {
		t.Fatalf("fixture premise: minted node namespace = %v, want demo", minted.Properties["namespace"])
	}

	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{minted, payments})

	if got := matchedKey(t, m, nameCriteria(acct, "payments", "demo")); got != payments.UniqueKey {
		t.Errorf("demo known only through a traces-minted node: matched %q, want %q", got, payments.UniqueKey)
	}
}

// Strategy 4 searches every account. The observed namespace is known in the
// observing cluster A, and the only candidate lives in cluster B, which has no
// such namespace: the observing account's knowledge must still reject it, or
// the guard just pushes the wrong match from strategy 2 into strategy 4.
func TestNamespaceGuard_CrossAccount(t *testing.T) {
	frontendA := nsWorkloadNode("n-frontend", "frontend", "demo", "acct-a")
	paymentsB := nsWorkloadNode("n-payments", "payments", "shop", "acct-b")
	m := NewNodeMatcher([]*core.DbNode{frontendA, paymentsB})

	anyAccount := nameCriteria("", "payments", "demo")
	anyAccount.ObservingAccountID = "acct-a"
	if got := matchedKey(t, m, anyAccount); got != "" {
		t.Errorf("demo known in observing account: matched %q, want no match", got)
	}

	// Known in the candidate's own account also counts.
	otherInB := nsWorkloadNode("n-other", "other", "demo", "acct-b")
	m = NewNodeMatcher([]*core.DbNode{paymentsB, otherInB})
	if got := matchedKey(t, m, nameCriteria("", "payments", "demo")); got != "" {
		t.Errorf("demo known in candidate account: matched %q, want no match", got)
	}
}

func TestNamespaceGuard_KillSwitch(t *testing.T) {
	t.Setenv(namespaceGuardEnvEnabled, "false")

	const acct = "acct-a"
	frontend := nsWorkloadNode("n-frontend", "frontend", "demo", acct)
	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{frontend, payments})

	if got := matchedKey(t, m, nameCriteria(acct, "payments", "demo")); got != payments.UniqueKey {
		t.Errorf("guard disabled: matched %q, want today's name-only match %q", got, payments.UniqueKey)
	}
}

func TestNamespaceGuard_CountsRejectionsForSummary(t *testing.T) {
	const acct = "acct-a"
	frontend := nsWorkloadNode("n-frontend", "frontend", "demo", acct)
	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{frontend, payments})

	if count, _ := m.GuardRejectionSummary(); count != 0 {
		t.Fatalf("rejections before any lookup = %d, want 0", count)
	}
	matchedKey(t, m, nameCriteria(acct, "payments", "demo"))
	matchedKey(t, m, nameCriteria(acct, "payments", "demo"))
	count, sample := m.GuardRejectionSummary()
	if count != 2 {
		t.Errorf("rejections = %d, want 2", count)
	}
	if len(sample) != 1 || sample[0] != "demo->shop" {
		t.Errorf("sample = %v, want the one distinct pair [demo->shop]", sample)
	}
}

func TestNamespaceGuard_KillSwitchCountsNoRejections(t *testing.T) {
	t.Setenv(namespaceGuardEnvEnabled, "false")

	const acct = "acct-a"
	frontend := nsWorkloadNode("n-frontend", "frontend", "demo", acct)
	payments := nsWorkloadNode("n-payments", "payments", "shop", acct)
	m := NewNodeMatcher([]*core.DbNode{frontend, payments})

	matchedKey(t, m, nameCriteria(acct, "payments", "demo"))
	if count, sample := m.GuardRejectionSummary(); count != 0 || len(sample) != 0 {
		t.Errorf("guard disabled: rejections = %d sample = %v, want 0 and none", count, sample)
	}
}
