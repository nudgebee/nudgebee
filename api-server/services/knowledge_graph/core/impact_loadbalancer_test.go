package core

import "testing"

// A load balancer's useful neighbourhood is entirely downstream — nothing routes
// *to* it — so before it had a downstream entry every load-balancer alarm
// reported no topology at all. The AWS collector emits plain ROUTES_TO for
// ALB/NLB → EC2 target membership, which the K8s-shaped ROUTES_TO_BACKEND /
// ROUTES_TO_SERVICE pair never matches.
func TestDownstreamRelationshipStrings_LoadBalancerCoversCloudRoutesTo(t *testing.T) {
	got := downstreamRelationshipStrings(NodeTypeLoadBalancer)
	if len(got) == 0 {
		t.Fatal("LoadBalancer must get a downstream pass; without one an ALB alarm shows no topology")
	}
	want := map[string]bool{
		string(RelationshipRoutesTo):        false,
		string(RelationshipRoutesToBackend): false,
		string(RelationshipRoutesToService): false,
	}
	for _, rel := range got {
		if rel == string(RelationshipCalls) {
			t.Error("CALLS must stay out of the LoadBalancer downstream set: a backend's flow edges point back at the balancer's own ENIs, which arrive as ExternalService IP nodes and pass the dependency-type filter, so the panel would report the load balancer depending on its own private IPs")
			continue
		}
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected LoadBalancer downstream relationship %q", rel)
			continue
		}
		want[rel] = true
	}
	for rel, seen := range want {
		if !seen {
			t.Errorf("LoadBalancer downstream is missing %q", rel)
		}
	}
}

// The upstream (blast-radius) direction must stay as it was: traversal there is
// destination→source, so listing ROUTES_TO would ask "who routes to the load
// balancer", which nothing does. Only the downstream pass is meaningful.
func TestImpactRelationshipDefaults_LoadBalancerUpstreamUnchanged(t *testing.T) {
	for _, rel := range impactRelationshipDefaults[NodeTypeLoadBalancer] {
		if rel == RelationshipRoutesTo {
			t.Fatal("ROUTES_TO must not be added to the LoadBalancer upstream defaults — nothing routes to a load balancer, so it can only add cost")
		}
	}
}

// A load balancer's backend instance must survive summarizeDownstream's
// node-type filter. It is the tier that actually serves the request, so dropping
// it as "infrastructure" leaves an ALB alarm reporting nothing it depends on.
func TestSummarizeDownstream_NamesLoadBalancerBackendInstance(t *testing.T) {
	seedID := "alb-1"
	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeLoadBalancer, "nb-demo-alb", "", ""),
		newImpactTestNode("ec2-1", NodeTypeComputeInstance, "nb-demo-web", "", ""),
		newImpactTestNode("sg-1", NodeTypeSecurityGroup, "nb-demo-app", "", ""), // plumbing, must stay out
	}
	depth := map[string]int{seedID: 0, "ec2-1": 1, "sg-1": 1}
	edges := []*DbEdge{
		{SourceNodeID: seedID, DestinationNodeID: "ec2-1", RelationshipType: RelationshipRoutesTo,
			ContributingSources: []EdgeContributingSource{{Source: "aws"}}},
	}

	got := summarizeDownstream(seedID, nodes, edges, depth, map[string]string{})

	if len(got) != 1 {
		t.Fatalf("expected the routed-to instance only (the security group is plumbing), got %d: %+v", len(got), got)
	}
	if got[0].Name != "nb-demo-web" || got[0].HopsAway != 1 || got[0].Relationship != RelationshipRoutesTo {
		t.Errorf("got %+v, want nb-demo-web at 1 hop via ROUTES_TO", got[0])
	}
}

// Widening downstreamDependencyTypes must not leak into the dependent side: that
// set feeds DependentCount and the FinOps safety band.
func TestAppDependentTypesExcludesComputeInstance(t *testing.T) {
	if appDependentTypes[NodeTypeComputeInstance] {
		t.Fatal("ComputeInstance must stay out of appDependentTypes — it feeds DependentCount and the safety band")
	}
	if !downstreamDependencyTypes[NodeTypeComputeInstance] {
		t.Fatal("ComputeInstance must be nameable as a downstream dependency")
	}
}
