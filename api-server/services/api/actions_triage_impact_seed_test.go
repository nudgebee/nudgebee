package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSeedNodeTypeGroupsAreSingleType pins the property that makes resolution
// work at all: the resolver accepts a match only when exactly one node comes
// back, and an AWS resource routinely shares its name with a security group
// (`nb-demo-db` is both a Database and a SecurityGroup). Grouping several types
// into one search would return 2 and silently resolve nothing — the bug would
// look fixed while behaving identically.
func TestSeedNodeTypeGroupsAreSingleType(t *testing.T) {
	groups := seedNodeTypeGroups()
	if len(groups) == 0 {
		t.Fatal("no seed node type groups")
	}
	seen := map[core.NodeType]bool{}
	for _, g := range groups {
		if g.nodeType == "" {
			t.Errorf("empty node type in groups")
		}
		if seen[g.nodeType] {
			t.Errorf("node type %q appears twice; the second attempt is dead code", g.nodeType)
		}
		seen[g.nodeType] = true
	}
}

// TestSeedNodeTypeGroupsPreferWorkload keeps the existing Kubernetes preference:
// a workload and the service in front of it share a name, and the workload is
// the better blast-radius seed.
func TestSeedNodeTypeGroupsPreferWorkload(t *testing.T) {
	groups := seedNodeTypeGroups()
	index := func(nt core.NodeType) int {
		for i, g := range groups {
			if g.nodeType == nt {
				return i
			}
		}
		return -1
	}
	workload, service, k8sService := index(core.NodeTypeWorkload), index(core.NodeTypeService), index(core.NodeTypeK8sService)
	if workload < 0 || service < 0 || k8sService < 0 {
		t.Fatalf("k8s types missing: workload=%d service=%d k8sService=%d", workload, service, k8sService)
	}
	if workload >= service || service >= k8sService {
		t.Errorf("k8s preference order broken: workload=%d service=%d k8sService=%d", workload, service, k8sService)
	}
}

// TestCloudSeedTypesResolvable is the regression for the blast radius returning
// {"resolved": false} on a live RDS alarm: the resolver only searched Kubernetes
// node types, so an AWS Database, ComputeInstance or LoadBalancer could never be
// found no matter how complete the topology was.
func TestCloudSeedTypesResolvable(t *testing.T) {
	groups := seedNodeTypeGroups()
	has := func(nt core.NodeType) bool {
		for _, g := range groups {
			if g.nodeType == nt {
				return true
			}
		}
		return false
	}
	for _, nt := range []core.NodeType{
		core.NodeTypeDatabase,
		core.NodeTypeComputeInstance,
		core.NodeTypeLoadBalancer,
	} {
		if !has(nt) {
			t.Errorf("%q is not resolvable as a blast-radius seed", nt)
		}
	}
}

// TestKubernetesSeedsKeepNamespace guards the hazard the namespace relaxation
// could introduce: a workload name is unique only within its namespace, so
// dropping the namespace for Kubernetes types would let a workload in one
// namespace answer for a same-named workload in another. Cloud types must drop
// it — their nodes carry no namespace at all.
func TestKubernetesSeedsKeepNamespace(t *testing.T) {
	for _, g := range seedNodeTypeGroups() {
		switch g.nodeType {
		case core.NodeTypeWorkload, core.NodeTypeService, core.NodeTypeK8sService:
			if !g.namespaced {
				t.Errorf("%q must match with the event namespace", g.nodeType)
			}
			if got := g.namespace("demo"); got != "demo" {
				t.Errorf("%q namespace = %q, want %q", g.nodeType, got, "demo")
			}
		default:
			if g.namespaced {
				t.Errorf("%q is a cloud type and must be searched namespace-blind", g.nodeType)
			}
			// A cloud event carries "AmazonRDS" here; cloud nodes have no namespace.
			if got := g.namespace("AmazonRDS"); got != "" {
				t.Errorf("%q namespace = %q, want empty", g.nodeType, got)
			}
		}
	}
}

// TestSeedTypesExcludePlumbing keeps infrastructure furniture out of seeding.
// Blast radius from a security group would report every instance attached to it.
func TestSeedTypesExcludePlumbing(t *testing.T) {
	for _, g := range seedNodeTypeGroups() {
		switch g.nodeType {
		case core.NodeTypeSecurityGroup, core.NodeTypeSubnet, core.NodeTypeVPC:
			t.Errorf("%q is plumbing and must not be a blast-radius seed", g.nodeType)
		}
	}
}

// TestInfrastructureImpactedIsNotNamespaceScoped guards a trap this change fell
// into once: scopeAndNormalize drops anything whose namespace differs from the
// event's, and infrastructure nodes carry no namespace at all (a cloud resource
// has none; a k8s Node is cluster-wide). Scoping them by the event namespace —
// "AmazonRDS" for an RDS alarm — silently emptied the list while the count still
// said 2.
func TestInfrastructureImpactedIsNotNamespaceScoped(t *testing.T) {
	infra := []core.ImpactedService{
		{NodeID: "api", Name: "nb-demo-api", NodeType: core.NodeTypeComputeInstance, HopsAway: 1},
		{NodeID: "web", Name: "nb-demo-web", NodeType: core.NodeTypeComputeInstance, HopsAway: 2},
	}
	if got := scopeAndNormalize(infra, "AmazonRDS"); len(got) != 0 {
		t.Fatalf("precondition changed: namespace scoping no longer drops them (%d kept)", len(got))
	}
	got := scopeAndNormalize(infra, "")
	if len(got) != 2 {
		t.Fatalf("unscoped = %d entries, want 2", len(got))
	}
}
