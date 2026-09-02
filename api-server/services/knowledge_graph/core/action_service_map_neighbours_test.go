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
