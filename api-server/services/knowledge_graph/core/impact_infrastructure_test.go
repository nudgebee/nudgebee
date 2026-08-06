package core

import "testing"

func computeNode(id, name string, nodeType NodeType) *DbNode {
	return &DbNode{
		ID:              id,
		NodeType:        nodeType,
		QueryAttributes: map[string]interface{}{"name": name},
	}
}

// TestInfrastructureDependentsReported is the regression for a blast radius that
// read "nothing impacted" on a VM stack. A ComputeInstance calling a database was
// traversed and counted in DependentsByType, then dropped because it is not an
// application-level type — a judgement that holds on Kubernetes, where a VM is an
// intermediate, but not on EC2, where the instance is the application.
func TestInfrastructureDependentsReported(t *testing.T) {
	nodes := []*DbNode{
		computeNode("seed", "nb-demo-db", NodeTypeDatabase),
		computeNode("api", "nb-demo-api", NodeTypeComputeInstance),
	}
	s := summarizeImpact("seed", NodeTypeDatabase, nodes, nil, map[string]int{"api": 1})

	if s.InfrastructureCount != 1 || len(s.InfrastructureDependents) != 1 {
		t.Fatalf("InfrastructureCount=%d dependents=%d, want 1/1", s.InfrastructureCount, len(s.InfrastructureDependents))
	}
	if got := s.InfrastructureDependents[0].Name; got != "nb-demo-api" {
		t.Errorf("name = %q, want nb-demo-api", got)
	}
	if got := s.InfrastructureDependents[0].NodeType; got != NodeTypeComputeInstance {
		t.Errorf("node type = %q, want ComputeInstance", got)
	}
	if got := s.InfrastructureDependents[0].HopsAway; got != 1 {
		t.Errorf("hops = %d, want 1", got)
	}
}

// TestInfrastructureDependentsDoNotInflateSafetyInputs is the guard that made
// this the chosen approach: DependentCount and ProductionDependents feed the
// FinOps recommendation safety band, so surfacing infrastructure must not move
// them. Widening appDependentTypes instead would have changed safety scoring for
// every tenant.
func TestInfrastructureDependentsDoNotInflateSafetyInputs(t *testing.T) {
	nodes := []*DbNode{
		computeNode("seed", "nb-demo-db", NodeTypeDatabase),
		computeNode("api", "nb-demo-api", NodeTypeComputeInstance),
		computeNode("node1", "ip-10-0-0-1", NodeTypeNode),
	}
	s := summarizeImpact("seed", NodeTypeDatabase, nodes, nil, map[string]int{"api": 1, "node1": 2})

	if s.DependentCount != 0 {
		t.Errorf("DependentCount = %d, want 0 — infrastructure must not count as an app dependent", s.DependentCount)
	}
	if len(s.Dependents) != 0 {
		t.Errorf("Dependents = %d, want 0", len(s.Dependents))
	}
	if s.ProductionDependents != 0 {
		t.Errorf("ProductionDependents = %d, want 0", s.ProductionDependents)
	}
	if s.InfrastructureCount != 2 {
		t.Errorf("InfrastructureCount = %d, want 2", s.InfrastructureCount)
	}
}

// TestApplicationDependentsStillCounted confirms the Kubernetes path is
// untouched: a Workload dependent still lands in Dependents, not in the
// infrastructure bucket.
func TestApplicationDependentsStillCounted(t *testing.T) {
	nodes := []*DbNode{
		computeNode("seed", "postgres", NodeTypeDatabase),
		computeNode("wl", "checkout", NodeTypeWorkload),
	}
	s := summarizeImpact("seed", NodeTypeDatabase, nodes, nil, map[string]int{"wl": 1})

	if s.DependentCount != 1 || len(s.Dependents) != 1 {
		t.Fatalf("DependentCount=%d dependents=%d, want 1/1", s.DependentCount, len(s.Dependents))
	}
	if s.InfrastructureCount != 0 {
		t.Errorf("InfrastructureCount = %d, want 0", s.InfrastructureCount)
	}
}
