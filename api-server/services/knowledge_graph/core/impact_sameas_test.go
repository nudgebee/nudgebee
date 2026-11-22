package core

// A self-hosted VM and its cloud instance nodes are one machine joined by
// SAME_AS (sources/selfhosted). A blast radius seeded on any of them must reach
// the same dependents and must never list a twin as a dependent of itself.

import (
	"sort"
	"testing"
)

// Every depth-0 node is a seed: a twin seeded alongside the resource is not a
// dependent of it.
func TestSummarizeImpact_TwinSeedsAreNotDependents(t *testing.T) {
	nodes := []*DbNode{
		newImpactTestNode("vm", NodeTypeComputeInstance, "vm", "", ""),
		newImpactTestNode("ec2-a", NodeTypeComputeInstance, "ec2-a", "", ""),
		newImpactTestNode("k8s-node", NodeTypeNode, "k8s-node", "", ""),
		newImpactTestNode("svc", NodeTypeService, "orders", "prod", "shop"),
	}
	minDepth := map[string]int{"vm": 0, "ec2-a": 0, "k8s-node": 1, "svc": 1}

	got := summarizeImpact("vm", NodeTypeComputeInstance, nodes, nil, minDepth, map[string]string{}, map[string]string{})

	if got.DependentsByType[NodeTypeComputeInstance] != 0 {
		t.Errorf("a twin was counted as a dependent: %v", got.DependentsByType)
	}
	for _, d := range append(append([]ImpactedService{}, got.Dependents...), got.InfrastructureDependents...) {
		if d.NodeID == "ec2-a" {
			t.Errorf("twin ec2-a listed as a dependent: %+v", d)
		}
	}
	if got.DependentCount != 1 || got.InfrastructureCount != 1 {
		t.Errorf("DependentCount=%d InfrastructureCount=%d, want 1 and 1", got.DependentCount, got.InfrastructureCount)
	}
}

// A node missing from nodeMinDepth is not a seed, even though a map lookup of
// it returns 0.
func TestIsTraversalSeed(t *testing.T) {
	depth := map[string]int{"seed": 0, "near": 1}
	if !isTraversalSeed(depth, "seed") || isTraversalSeed(depth, "near") || isTraversalSeed(depth, "absent") {
		t.Errorf("isTraversalSeed wrong for %v", depth)
	}
}

// DB-gated: one VM twin and two EC2 twins of the same machine, with the K8s node
// running on only one EC2 node and a service calling only the VM node.
func TestGetImpactedServices_SameDependentsFromEveryTwin(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("vm", NodeTypeComputeInstance)
	f.node("ec2-a", NodeTypeComputeInstance)
	f.node("ec2-b", NodeTypeComputeInstance)
	f.node("k8s-node", NodeTypeNode)
	f.node("orders", NodeTypeService)
	edges := append(sameAs("vm", "ec2-a"), sameAs("vm", "ec2-b")...)
	edges = append(edges,
		[3]string{"k8s-node", string(RelationshipRunsOn), "ec2-a"},
		[3]string{"orders", string(RelationshipCalls), "vm"})
	f.save(t, edges...)

	twins := map[string]bool{f.nodes["vm"].ID: true, f.nodes["ec2-a"].ID: true, f.nodes["ec2-b"].ID: true}
	nameOf := map[string]string{}
	for name, n := range f.nodes {
		nameOf[n.ID] = name
	}

	dependents := func(seed string) (string, *ImpactSummary) {
		t.Helper()
		summary, err := f.svc.GetImpactedServices(twinLeafTenant, f.nodes[seed].ID, nil, 0)
		if err != nil {
			t.Fatalf("GetImpactedServices(%s): %v", seed, err)
		}
		if summary.SeedNodeID != f.nodes[seed].ID {
			t.Errorf("SeedNodeID = %s, want the requested seed %s", summary.SeedNodeID, seed)
		}
		var names []string
		for _, d := range append(append([]ImpactedService{}, summary.Dependents...), summary.InfrastructureDependents...) {
			if twins[d.NodeID] {
				t.Errorf("seed %s: twin %s listed as a dependent", seed, nameOf[d.NodeID])
			}
			names = append(names, nameOf[d.NodeID])
		}
		sort.Strings(names)
		return fmtNames(names), summary
	}

	want, ref := dependents("vm")
	if want != "k8s-node,orders" {
		t.Fatalf("from vm: dependents = %s, want k8s-node,orders", want)
	}
	for _, seed := range []string{"ec2-a", "ec2-b"} {
		got, summary := dependents(seed)
		if got != want {
			t.Errorf("from %s: dependents = %s, want %s (same machine as vm)", seed, got, want)
		}
		if summary.DependentCount != ref.DependentCount {
			t.Errorf("from %s: DependentCount = %d, want %d", seed, summary.DependentCount, ref.DependentCount)
		}
	}
}

func fmtNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
