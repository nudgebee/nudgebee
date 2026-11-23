package flow_sources

import (
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// testCreationContext builds a per-account creation context without touching
// the database, so the gate can be exercised for both account shapes:
// inventoryAuthoritative=true is a cluster a collector describes, false is an
// eBPF-only install where observed traffic is the only description there is.
func testCreationContext(inventoryAuthoritative bool) *k8sCreationContext {
	return &k8sCreationContext{
		inventoryAuthoritative: inventoryAuthoritative,
		skippedKinds:           map[string]int{},
		inventoryMisses:        map[string]bool{},
	}
}

// TestK8sAuthoritativeGateClassifiesEverySynthesisableInfraType is the forcing
// function behind #41619.
//
// The bug was not that the gate's membership was wrong in principle — it was
// that the list had been assembled from "which types feel infrastructural"
// rather than from "which infra-authoritative types these creation paths can
// actually produce". Workload, Job and CronJob were reachable and ungated, so
// eBPF minted nodes for deleted workloads that no sweep could retire.
//
// So derive the requirement instead of restating it: every infra-authoritative
// NodeType a flow-source creation path can yield must be deliberately
// classified as either gated (a static source owns its existence) or
// flow-produced (the flow source is its legitimate producer). Adding a kind to
// ebpfKindMap that maps to an unclassified infra-authoritative type fails this
// test, which is the point — the alternative is a node type that may be
// created freely and never retired.
func TestK8sAuthoritativeGateClassifiesEverySynthesisableInfraType(t *testing.T) {
	check := func(t *testing.T, origin, kind string, nodeType core.NodeType) {
		t.Helper()
		if !nodeType.IsInfraAuthoritative() {
			return
		}
		gated := isK8sAuthoritativeType(nodeType) || inventoryOwnedWorkloadTypes[nodeType]
		flowProduced := flowProducedInfraTypes[nodeType]
		switch {
		case gated && flowProduced:
			t.Errorf("%s kind %q infers %s, which is both gated and listed as "+
				"flow-produced — the two are mutually exclusive", origin, kind, nodeType)
		case !gated && !flowProduced:
			t.Errorf("%s kind %q infers %s, an infra-authoritative type that is neither "+
				"gated nor declared flow-produced: as it stands a flow source may create "+
				"one and re-stamp it every build, so the sweep can never retire it (#41619). "+
				"Add it to isK8sAuthoritativeType (never flow-created) or "+
				"inventoryOwnedWorkloadTypes (flow-created only when the account has no "+
				"inventory) if a static source owns it, or to flowProducedInfraTypes with "+
				"the reason if it has no other producer.",
				origin, kind, nodeType)
		}
	}

	for kind, nodeType := range ebpfKindMap {
		check(t, "ebpf", kind, nodeType)
	}

	// The traces source reaches the same gate through its own kind map.
	traces := NewTracesFlowSource(slog.Default())
	for _, kind := range []string{"Service", "Deployment", "StatefulSet", "DaemonSet", "Pod", "Runner", "Database", "ExternalService", "Node"} {
		nodeType, _ := traces.inferNodeType(kind, nil)
		check(t, "traces", kind, nodeType)
	}
}

// TestK8sAuthoritativeGateHasNoUnreachableTypes is the other half: a type in
// the gate that no creation path can produce is dead weight that misleads the
// next reader into thinking a path is guarded when none exists. That is what
// K8sService would have been — nodeTypesToTryFor("service") probes it for
// MATCHING, which makes it look creatable, but inferNodeType maps "service" to
// the logical NodeTypeService and never to K8sService.
//
// Namespace and Cluster are the deliberate exceptions: nothing infers them
// today either, but they are kept as standing guards because the agent has
// historically mislabelled observations with cluster-shaped kinds, and a
// future kind mapping to one of them must not silently gain a creation path.
func TestK8sAuthoritativeGateHasNoUnreachableTypes(t *testing.T) {
	standingGuards := map[core.NodeType]bool{
		core.NodeTypeNamespace: true,
		core.NodeTypeCluster:   true,
	}

	reachable := map[core.NodeType]bool{}
	for _, nodeType := range ebpfKindMap {
		reachable[nodeType] = true
	}
	traces := NewTracesFlowSource(slog.Default())
	for _, kind := range []string{"Service", "Deployment", "StatefulSet", "DaemonSet", "Pod", "Runner", "Database", "ExternalService", "Node"} {
		nodeType, _ := traces.inferNodeType(kind, nil)
		reachable[nodeType] = true
	}

	for nodeType := range core.InfraAuthoritativeNodeTypes {
		if !isK8sAuthoritativeType(nodeType) && !inventoryOwnedWorkloadTypes[nodeType] {
			continue
		}
		if standingGuards[nodeType] {
			continue
		}
		if !reachable[nodeType] {
			t.Errorf("gate names %s, but no flow-source creation path can produce it; "+
				"it guards nothing and implies a path that does not exist", nodeType)
		}
	}

	if isK8sAuthoritativeType(core.NodeTypeK8sService) || inventoryOwnedWorkloadTypes[core.NodeTypeK8sService] {
		t.Error("K8sService is gated, but no creation path infers it — " +
			"nodeTypesToTryFor probes it for matching only")
	}
}

// TestEbpfDoesNotMaterialiseWorkloadAbsentFromInventory is the behavioural
// test for #41619: the exact situation QA reported. k8s_source emits only
// active inventory rows, so a workload deleted from the cluster has no node
// in the pool; the eBPF service map keeps reporting it for hours afterwards.
// Before the fix that miss minted a `source="ebpf"` Workload with a
// k8s-shaped unique key, re-created on every build and therefore always
// stamped at the current sync version — the one state markInactiveNodes
// cannot touch.
func TestEbpfDoesNotMaterialiseWorkloadAbsentFromInventory(t *testing.T) {
	source := NewEbpfFlowSource(slog.Default())
	const acctID = "acct-1"

	// Only the running workload is in the pool, as k8s_source would leave it.
	running := &core.DbNode{
		ID:             "node-running",
		NodeType:       core.NodeTypeWorkload,
		UniqueKey:      "k8s:acct-1::Workload:app-100b:checkout-api",
		CloudAccountID: acctID,
		Properties:     map[string]interface{}{"name": "checkout-api", "namespace": "app-100b"},
	}
	source.InitializeNodeMatcher([]*core.DbNode{running})
	account := core.K8sAccount{CloudAccountID: acctID, Name: "iteration-test"}

	// Workload kinds only. Job and CronJob deliberately still materialise —
	// k8s_source emits neither node type, so refusing them would delete every
	// Job and CronJob in the graph rather than the stale ones.
	deleted := []struct {
		name, kind string
	}{
		{"payment-api-100b", "Deployment"}, // the issue's own example
		{"orders-api", "StatefulSet"},
		{"log-shipper", "DaemonSet"},
	}
	for _, d := range deleted {
		t.Run(d.kind+"/"+d.name, func(t *testing.T) {
			app := &core.ServiceApplication{
				Id: core.ServiceApplicationId{Name: d.name, Kind: d.kind, Namespace: "app-100b"},
			}
			if _, err := source.matchApplicationToNode(app, acctID); err == nil {
				t.Fatalf("precondition broken: %s matched a node although the inventory has none", d.name)
			}
			cc := testCreationContext(true)
			if node := source.createNodeForApplication(app, "tenant-1", account, cc); node != nil {
				t.Errorf("created %s node %q for a workload with no active inventory row; "+
					"it would be re-stamped every build and no sweep could retire it (#41619)",
					node.NodeType, node.UniqueKey)
			}
			// The ID-only path (upstream/downstream absent from the service map)
			// must refuse it too, or the same orphan arrives by the other door.
			if node := source.createNodeForApplicationID(app.Id, "tenant-1", account, cc); node != nil {
				t.Errorf("createNodeForApplicationID created %s node %q for the same workload",
					node.NodeType, node.UniqueKey)
			}
		})
	}

	// A running workload still resolves, so its CALLS edges survive — the
	// trade-off is bounded to workloads the inventory does not have.
	app := &core.ServiceApplication{
		Id: core.ServiceApplicationId{Name: "checkout-api", Kind: "Deployment", Namespace: "app-100b"},
	}
	node, err := source.matchApplicationToNode(app, acctID)
	if err != nil {
		t.Fatalf("running workload must still match: %v", err)
	}
	if node.ID != running.ID {
		t.Errorf("expected match on %s, got %s", running.ID, node.ID)
	}
}

// TestRecordK8sInventoryMiss_DedupesPerAccount: the creation paths are called
// from inside the per-link loops, so one deleted workload that many services
// still call is refused once per calling link. Without the per-account set
// that is one synchronous DB round-trip per refusal, all colliding onto the
// same review row, inside a build holding the tenant's lock. Asserted on the
// set rather than on the write, so it holds with no database present.
func TestRecordK8sInventoryMiss_DedupesPerAccount(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		recordK8sInventoryMiss(slog.Default(), seen, "ebpf", "tenant-1",
			core.NodeTypeWorkload, "Deployment", "payment-api-100b", "app-100b", "iteration-test")
	}
	if len(seen) != 1 {
		t.Errorf("five refusals of one workload recorded %d entries, want 1", len(seen))
	}

	// A different object in the same account is its own entry.
	recordK8sInventoryMiss(slog.Default(), seen, "ebpf", "tenant-1",
		core.NodeTypeWorkload, "Deployment", "payment-api-100b", "other-ns", "iteration-test")
	recordK8sInventoryMiss(slog.Default(), seen, "ebpf", "tenant-1",
		core.NodeTypeJob, "Job", "load-driver", "app-100b", "iteration-test")
	if len(seen) != 3 {
		t.Errorf("distinct objects collapsed: %d entries, want 3 (%v)", len(seen), seen)
	}

	// Types the gate refuses for other reasons are not recorded at all: a pod
	// name the agent mislabelled as kind="node" is unbounded in cardinality
	// and means nothing to the inventory.
	before := len(seen)
	recordK8sInventoryMiss(slog.Default(), seen, "ebpf", "tenant-1",
		core.NodeTypeNode, "node", "ip-10-0-0-1", "", "iteration-test")
	if len(seen) != before {
		t.Error("a refused Node observation was recorded; only workload-shaped types are")
	}
}

// TestEbpfCreatesWorkloadWhenAccountHasNoInventory is the eBPF-only install.
// Gating on inventory alone would leave such an account with no workload
// nodes at all — on test, `iteration-test-onboarding-e2e` is exactly this
// shape (0 k8s_workloads rows, 0 k8s-sourced nodes, 761 flow-sourced ones).
// With nothing else able to describe the cluster, observed traffic is the
// best available description and the flow source may create from it.
func TestEbpfCreatesWorkloadWhenAccountHasNoInventory(t *testing.T) {
	source := NewEbpfFlowSource(slog.Default())
	source.InitializeNodeMatcher(nil)
	account := core.K8sAccount{CloudAccountID: "acct-ebpf-only", Name: "ebpf-only-cluster"}

	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
		t.Run(kind, func(t *testing.T) {
			app := &core.ServiceApplication{
				Id: core.ServiceApplicationId{Name: "payment-api", Kind: kind, Namespace: "app-100b"},
			}

			// With an inventory, the same observation is refused.
			if node := source.createNodeForApplication(app, "tenant-1", account, testCreationContext(true)); node != nil {
				t.Fatalf("inventory-backed account created %s; the gate did not apply", node.UniqueKey)
			}

			// Without one, it must be created.
			node := source.createNodeForApplication(app, "tenant-1", account, testCreationContext(false))
			if node == nil {
				t.Fatalf("eBPF-only account got no %s node; its graph would have no workloads at all", kind)
			}

			// And it must carry the marker that exempts it from the sweep,
			// or it would be retired the first hour the workload goes quiet.
			if node.Properties[core.FlowAuthoritativeProperty] != true {
				t.Errorf("%s node is missing %s=true; the sweep would retire it on silence",
					kind, core.FlowAuthoritativeProperty)
			}

			// The ID-only path (upstream/downstream absent from the service
			// map) must agree, or the same workload appears or not depending
			// on which door it came through.
			byID := source.createNodeForApplicationID(app.Id, "tenant-1", account, testCreationContext(false))
			if byID == nil {
				t.Errorf("createNodeForApplicationID refused %s for an eBPF-only account", kind)
			} else if byID.UniqueKey != node.UniqueKey {
				t.Errorf("the two creation paths disagree on identity: %q vs %q", byID.UniqueKey, node.UniqueKey)
			}
		})
	}
}

// TestFlowMayCreate_PolicyMatrix states the whole policy in one place, since
// it is now three sets rather than one and the interesting cases are the
// combinations.
func TestFlowMayCreate_PolicyMatrix(t *testing.T) {
	cases := []struct {
		nodeType               core.NodeType
		inventoryAuthoritative bool
		want                   bool
		why                    string
	}{
		{core.NodeTypeWorkload, true, false, "inventory owns which workloads exist"},
		{core.NodeTypeWorkload, false, true, "nothing else can describe this cluster"},
		// Job and CronJob are NOT gated: k8s_source maps both inventory kinds
		// to NodeTypeWorkload, so nothing static emits these two types and a
		// flow source is their only producer (same position as CRD).
		{core.NodeTypeJob, true, true, "no static source emits NodeTypeJob"},
		{core.NodeTypeJob, false, true, "no static source emits NodeTypeJob"},
		{core.NodeTypeCronJob, true, true, "no static source emits NodeTypeCronJob"},
		{core.NodeTypeCronJob, false, true, "no static source emits NodeTypeCronJob"},

		// Unconditional: these are refused whatever the inventory looks like,
		// because what reaches this path is a mislabelled pod name rather
		// than a cluster object the collector missed.
		{core.NodeTypeNode, true, false, "k8s_source owns cluster nodes"},
		{core.NodeTypeNode, false, false, "still refused with no inventory"},
		{core.NodeTypeNamespace, false, false, "still refused with no inventory"},
		{core.NodeTypeCluster, false, false, "still refused with no inventory"},

		// Never gated: k8s_source emits only Karpenter CRDs, so flow sources
		// are the only producer of operator CRDs either way.
		{core.NodeTypeCRD, true, true, "flow sources are the only CRD producer"},
		{core.NodeTypeCRD, false, true, "flow sources are the only CRD producer"},

		// Not infra-authoritative at all; the flow source owns the lifecycle.
		{core.NodeTypeService, true, true, "logical service, flow-owned"},
		{core.NodeTypeExternalService, true, true, "external endpoint, flow-owned"},
	}
	for _, c := range cases {
		if got := flowMayCreate(c.nodeType, c.inventoryAuthoritative); got != c.want {
			t.Errorf("flowMayCreate(%s, inventoryAuthoritative=%v) = %v, want %v — %s",
				c.nodeType, c.inventoryAuthoritative, got, c.want, c.why)
		}
	}
}
