package core

import "testing"

func neighbourTypeSet() map[NodeType]bool {
	set := make(map[NodeType]bool, len(serviceMapNeighbourTypes))
	for _, t := range serviceMapNeighbourTypes {
		set[t] = true
	}
	return set
}

// TestNeighbourTypesIncludeCloudCompute is the regression for a knowledge_graph
// evidence card that showed the alerting resource alone, with no edges, on an
// account whose graph plainly held `api instance --CALLS--> database`. The only
// node that could sit on the far end of that edge is a ComputeInstance, and it
// was filtered out — so the card rendered a single dot.
//
// The cost is not only the card: event correlation walks this evidence, so an
// empty neighbourhood means dependency_distance is always 0 and no cross-tier
// correlation can be produced. Measured before this change: zero topology
// correlations for AWS events fleet-wide over 14 days, against 2,030 for
// Kubernetes.
func TestNeighbourTypesIncludeCloudCompute(t *testing.T) {
	set := neighbourTypeSet()
	for _, nt := range []NodeType{
		NodeTypeComputeInstance,    // an EC2/VM instance IS the app on a VM stack
		NodeTypeLoadBalancer,       // the entry point in front of it
		NodeTypeServerlessFunction, // the app on a serverless stack
	} {
		t.Run(string(nt), func(t *testing.T) {
			if !set[nt] {
				t.Errorf("%q missing: a neighbourhood on a cloud stack cannot show its dependencies without it", nt)
			}
		})
	}
}

// TestNeighbourTypesExcludePlumbing keeps the card readable. A cloud resource is
// attached to a VPC, several subnets and a security group; including them would
// bury the one or two nodes an operator is looking for behind infrastructure
// furniture that carries no dependency information.
func TestNeighbourTypesExcludePlumbing(t *testing.T) {
	set := neighbourTypeSet()
	for _, nt := range []NodeType{
		NodeTypeVPC, NodeTypeSubnet, NodeTypeSecurityGroup,
	} {
		t.Run(string(nt), func(t *testing.T) {
			if set[nt] {
				t.Errorf("%q is infrastructure the resource sits in, not a dependency — it should not appear", nt)
			}
		})
	}
}

// TestNeighbourTypesKeepKubernetes guards against regressing the original
// behaviour while widening it for cloud.
func TestNeighbourTypesKeepKubernetes(t *testing.T) {
	set := neighbourTypeSet()
	for _, nt := range []NodeType{
		NodeTypeService, NodeTypeExternalService, NodeTypeDatabase,
		NodeTypeMessageQueue, NodeTypeCache, NodeTypeStorage,
		NodeTypeWorkload, NodeTypeK8sService,
	} {
		t.Run(string(nt), func(t *testing.T) {
			if !set[nt] {
				t.Errorf("%q was dropped from the neighbourhood types", nt)
			}
		})
	}
}

// TestNeighbourTypesHaveNoDuplicates — a repeated type would widen the SQL IN
// list for no reason and hints at a bad merge.
func TestNeighbourTypesHaveNoDuplicates(t *testing.T) {
	seen := map[NodeType]bool{}
	for _, nt := range serviceMapNeighbourTypes {
		if seen[nt] {
			t.Errorf("%q listed twice", nt)
		}
		seen[nt] = true
	}
}

// TestNeighbourTypesIncludeBackendPool is the regression for a pooled load
// balancer whose service map contained the balancer and nothing else.
//
// This allowlist is not a result filter. It reaches discoverBFS as
// IncludeNodeTypes, which applies it as a per-hop `n.node_type = ANY(...)`
// predicate, so a type left out does not get dropped from the answer — the walk
// stops there. A GCP load balancer reaches its instances only through
// LoadBalancer -> BackendPool -> ComputeInstance, so leaving BackendPool out cut
// every one of them off at the first hop while the graph itself held the full
// chain. Measured on the Rackspace tenant: 21 active BackendPool nodes carrying
// ROUTES_TO edges to their instances, none of them reachable from the balancer's
// evidence. AWS target groups and Azure backend pools map to the same type, so
// the same hole opens there as soon as either materializes the tier.
func TestNeighbourTypesIncludeBackendPool(t *testing.T) {
	if !neighbourTypeSet()[NodeTypeBackendPool] {
		t.Error("NodeTypeBackendPool missing: the filter runs per hop, so omitting a pass-through tier severs the walk rather than tidying the result")
	}
}

// TestServiceMapLevelsReachSecondHop guards the depth the two consumers of this
// evidence are configured for: triage.MaxDependencyDistance is 4 and
// triage.maxIncidentHops is 2, and both walk only what this action writes. At
// one level every distance above 1 is unreachable and those limits are dead
// config — an ALB alarm's evidence held two nodes while the graph held the
// instance calling three more.
func TestServiceMapLevelsReachSecondHop(t *testing.T) {
	if serviceMapLevels < 2 {
		t.Errorf("serviceMapLevels = %d: below 2 the incident-grouping hop limit cannot be reached", serviceMapLevels)
	}
	// GetMultipleNodeNeighbors clamps above 3; asking for more silently gets 3
	// and hides the real setting from anyone reading this constant.
	if serviceMapLevels > 3 {
		t.Errorf("serviceMapLevels = %d: GetMultipleNodeNeighbors clamps to 3, so this is not the depth that runs", serviceMapLevels)
	}
}

// TestServiceMapMaxNodesIsBounded keeps the second level affordable. This block
// is persisted on every event, and a second hop around a hub — a database with a
// hundred callers, a node running every pod — is where that goes wrong. The cap
// has to be large enough that a real dependency chain fits and small enough that
// a hub falls back to one level instead of writing an enormous block.
func TestServiceMapMaxNodesIsBounded(t *testing.T) {
	if serviceMapMaxNodes < 20 {
		t.Errorf("serviceMapMaxNodes = %d: too small, an ordinary two-hop neighbourhood would fall back to one level", serviceMapMaxNodes)
	}
	if serviceMapMaxNodes > 200 {
		t.Errorf("serviceMapMaxNodes = %d: too large to bound what gets written onto every event", serviceMapMaxNodes)
	}
}
