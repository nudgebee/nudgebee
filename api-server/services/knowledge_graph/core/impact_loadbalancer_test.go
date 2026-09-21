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
		// CALLS was deliberately excluded here until the ENI resolver learned to
		// classify amazon-elb and nat_gateway interfaces. Before that, a
		// backend's flow edges pointed back at the balancer's own addresses,
		// those arrived as unresolved ExternalService IP nodes, and an ALB was
		// reported as depending on its own two private IPs. With the addresses
		// collapsing onto the balancer and the gateway, the walk reaches the tier
		// behind the front door instead of stopping at the first instance.
		//
		// If bare IPs reappear in a load balancer's dependency list, the fix is
		// in flow_sources/eni_resolver.go, not here.
		string(RelationshipCalls): false,
	}
	for _, rel := range got {
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

// The reach CALLS buys has to stay bounded. One routing hop plus one calls hop
// reaches 6, 6, 47 and 69 real resources across the load balancers on one
// tenant; the last two are a shared ingress and a busy ALB. "Possible cause to
// check" is read during an incident, so past a screenful it stops being a
// shortlist and starts being a wall.
func TestDownstreamDependencyCapIsAScreenful(t *testing.T) {
	if downstreamDependencyCap < 5 {
		t.Errorf("cap = %d: too small to hold a real tier behind a load balancer", downstreamDependencyCap)
	}
	if downstreamDependencyCap > 30 {
		t.Errorf("cap = %d: past a screenful this is a wall, not a shortlist", downstreamDependencyCap)
	}
}

// A truncated list must be reported as one. DownstreamCount is what was
// returned, not what was found, so a consumer that cannot tell the difference
// presents a bounded slice as the complete answer.
func TestSummarizeDownstream_SortsSoTheKeptSliceIsTheUsefulEnd(t *testing.T) {
	seedID := "alb-1"
	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeLoadBalancer, "alb", "", ""),
		newImpactTestNode("ext-1", NodeTypeExternalService, "10.0.0.9", "", ""),
		newImpactTestNode("ec2-far", NodeTypeComputeInstance, "api", "", ""),
		newImpactTestNode("ec2-near", NodeTypeComputeInstance, "web", "", ""),
	}
	depth := map[string]int{seedID: 0, "ec2-near": 1, "ec2-far": 2, "ext-1": 2}

	got := summarizeDownstream(seedID, nodes, nil, depth, map[string]string{})

	if len(got) != 3 {
		t.Fatalf("expected 3 dependencies, got %d: %+v", len(got), got)
	}
	// Closest named resource first, unresolved external last — so truncating at
	// any length keeps the actionable end.
	if got[0].Name != "web" || got[1].Name != "api" || got[2].Name != "10.0.0.9" {
		t.Errorf("order = %s, %s, %s; want web, api, 10.0.0.9", got[0].Name, got[1].Name, got[2].Name)
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

// An address the flow source could not identify is not a cause an operator can
// investigate. The ENI resolver reclaims the ones that still have an interface
// behind them, but on one tenant 210 of 223 match no interface at all, so this
// list would otherwise carry bare private IPs indefinitely.
func TestSummarizeDownstream_DropsUnresolvedVPCAddresses(t *testing.T) {
	seedID := "alb-1"
	// The flow source writes subtype into Properties; impactNodeAttr reads
	// QueryAttributes first and Properties second, so the node is built the way
	// vpc_flowlogs_flow_source actually builds it.
	unresolved := newImpactTestNode("ext-1", NodeTypeExternalService, "10.0.0.143", "", "")
	unresolved.Properties = map[string]interface{}{"subtype": unresolvedVPCAddressSubtype}
	named := newImpactTestNode("ext-2", NodeTypeExternalService, "api.stripe.com", "", "")

	nodes := []*DbNode{
		newImpactTestNode(seedID, NodeTypeLoadBalancer, "alb", "", ""),
		newImpactTestNode("ec2-1", NodeTypeComputeInstance, "web", "", ""),
		unresolved,
		named,
	}
	depth := map[string]int{seedID: 0, "ec2-1": 1, "ext-1": 2, "ext-2": 2}

	got := summarizeDownstream(seedID, nodes, nil, depth, map[string]string{})

	for _, d := range got {
		if d.Name == "10.0.0.143" {
			t.Error("an unidentified address was named as a possible cause")
		}
	}
	// A resolved external dependency is a real one and must survive — the rule is
	// about addresses we failed to identify, not about external services.
	var sawNamed bool
	for _, d := range got {
		if d.Name == "api.stripe.com" {
			sawNamed = true
		}
	}
	if !sawNamed {
		t.Error("a named external dependency was dropped; the filter is too broad")
	}
	if len(got) != 2 {
		t.Errorf("got %d dependencies, want 2 (web and api.stripe.com): %+v", len(got), got)
	}
}
