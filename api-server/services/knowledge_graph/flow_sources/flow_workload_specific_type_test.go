package flow_sources

import (
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
)

// TestFlowCreatedWorkloadKeepsConcreteSpecificType pins the specific_type
// labelling to the live creation path.
//
// #41619 gated Workload creation for accounts that have a K8s inventory, which
// made the labelling momentarily unreachable, and it was deleted as dead code.
// The gate then became per account, so an eBPF-only install creates Workload
// nodes again — and those nodes shipped with specific_type "Workload", which
// has no registered schema, so they also lost every ontology attribute. The
// accounts affected were exactly the ones the per-account policy was added for.
//
// Asserted through an inventory-less context so the test exercises the path
// that is actually reachable; a future reader checking whether this is dead
// code gets an answer from the test rather than from the gate.
func TestFlowCreatedWorkloadKeepsConcreteSpecificType(t *testing.T) {
	ebpfOnly := func() *k8sCreationContext {
		return &k8sCreationContext{
			inventoryAuthoritative: false,
			skippedKinds:           map[string]int{},
			inventoryMisses:        map[string]bool{},
		}
	}
	account := core.K8sAccount{CloudAccountID: "acct-1", Name: "ebpf-only-cluster"}

	// The kinds both sources agree on, in the PascalCase form Kubernetes and
	// OTel resource attributes use.
	cases := []struct{ kind, wantSpecific string }{
		{"Deployment", "KubernetesDeployment"},
		{"StatefulSet", "KubernetesStatefulSet"},
		{"DaemonSet", "KubernetesDaemonSet"},
	}

	t.Run("ebpf", func(t *testing.T) {
		src := NewEbpfFlowSource(slog.Default())
		src.InitializeNodeMatcher(nil)
		for _, c := range cases {
			app := &core.ServiceApplication{
				Id: core.ServiceApplicationId{Name: "payment-api", Kind: c.kind, Namespace: "app-100b"},
			}
			node := src.createNodeForApplication(app, "tenant-1", account, ebpfOnly())
			if node == nil {
				t.Fatalf("kind %q: no node created for an eBPF-only account", c.kind)
			}
			if node.SpecificType != c.wantSpecific {
				t.Errorf("kind %q: specific_type = %q, want %q — a generic specific_type has no "+
					"registered schema, so the node also loses its ontology attributes",
					c.kind, node.SpecificType, c.wantSpecific)
			}

			// The ID-only path (upstream/downstream absent from the service
			// map) must label it identically, or the same workload is typed
			// differently depending on which door it came through.
			byID := src.createNodeForApplicationID(app.Id, "tenant-1", account, ebpfOnly())
			if byID == nil {
				t.Fatalf("kind %q: ID-only path created no node", c.kind)
			}
			if byID.SpecificType != c.wantSpecific {
				t.Errorf("kind %q: ID-path specific_type = %q, want %q", c.kind, byID.SpecificType, c.wantSpecific)
			}
		}

		// eBPF only: ebpfKindMap is keyed lowercase and the agent's casing
		// varies, so the label must normalise. (The traces kind map is
		// case-SENSITIVE and maps an unrecognised kind to Service; its input
		// is OTel resource attributes, which are PascalCase. That asymmetry
		// predates this test and is not asserted here.)
		app := &core.ServiceApplication{
			Id: core.ServiceApplicationId{Name: "payment-api", Kind: "deployment", Namespace: "app-100b"},
		}
		node := src.createNodeForApplication(app, "tenant-1", account, ebpfOnly())
		if node == nil {
			t.Fatal("lowercase kind created no node")
		}
		if node.SpecificType != "KubernetesDeployment" {
			t.Errorf("lowercase kind: specific_type = %q, want KubernetesDeployment", node.SpecificType)
		}
	})

	t.Run("traces", func(t *testing.T) {
		src := NewTracesFlowSource(slog.Default())
		src.InitializeNodeMatcher(nil)
		for _, c := range cases {
			app := &traces.ServiceApplication{
				Id: traces.ServiceApplicationId{Name: "payment-api", Kind: c.kind, Namespace: "app-100b"},
			}
			node := src.createNodeForServiceApplication(app, "tenant-1", account, ebpfOnly())
			if node == nil {
				t.Fatalf("kind %q: no node created for an eBPF-only account", c.kind)
			}
			if node.SpecificType != c.wantSpecific {
				t.Errorf("kind %q: specific_type = %q, want %q", c.kind, node.SpecificType, c.wantSpecific)
			}
		}
	})

	// An inventory-backed account creates nothing, so there is nothing to
	// label — the guard against the opposite mistake (reviving the node the
	// #41619 fix refuses) stays in TestEbpfDoesNotMaterialiseWorkloadAbsentFromInventory.
}
