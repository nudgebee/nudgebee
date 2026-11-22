package triage

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The VPC flow source collects every address it cannot identify into one node
// per VPC, with flow edges to and from each instance that talked to one. Walked
// as a dependency, it put every instance of a VPC two hops from every other.
const vpcHubEvidenceJSON = `{
  "type": "knowledge_graph",
  "nodes": [
    {"id": "a", "node_type": "ComputeInstance", "properties": {"name": "av-scan-1", "resource_id": "i-0000000000000000a"}},
    {"id": "b", "node_type": "ComputeInstance", "properties": {"name": "reporting-1", "resource_id": "i-0000000000000000b"}},
    {"id": "c", "node_type": "ComputeInstance", "properties": {"name": "av-scan-2", "resource_id": "i-0000000000000000c"}},
    {"id": "hub", "node_type": "ExternalService",
     "properties": {"name": "Unidentified addresses (vpc-0123456789abcdef0)", "subtype": "vpc-unresolved-ip"}}
  ],
  "edges": [
    {"relationship_type": "CALLS", "source_node_id": "a", "dest_node_id": "hub", "source": "aws-vpc-flow"},
    {"relationship_type": "CALLS", "source_node_id": "hub", "dest_node_id": "b", "source": "aws-vpc-flow"},
    {"relationship_type": "CALLS", "source_node_id": "a", "dest_node_id": "c", "source": "aws-vpc-flow"}
  ]
}`

func TestParseServiceMap_UnresolvedAddressNodeIsNotAHop(t *testing.T) {
	graph, err := parseServiceMapFromEvent(awsEvidenceEvent(t, "["+vpcHubEvidenceJSON+"]"))
	require.NoError(t, err)
	require.NotNil(t, graph)

	assert.Equal(t, -1, graph.getDependencyDistance("i-0000000000000000a", "i-0000000000000000b"),
		"two instances that only both talked to unidentified addresses are not connected")
	assert.Equal(t, -1, graph.dependsOnDistance("i-0000000000000000a", "i-0000000000000000b", maxIncidentHops))
	assert.Equal(t, 1, graph.getDependencyDistance("i-0000000000000000a", "i-0000000000000000c"),
		"a flow edge between two real instances still counts")
	assert.Equal(t, 1.0, graph.dependencyHopWeight("i-0000000000000000a", "i-0000000000000000c", 1),
		"an edge seen in VPC flow logs is observed, not user-declared")
	for key := range graph.Nodes {
		assert.NotContains(t, key, "Unidentified addresses")
	}
}

func TestUnresolvedVPCAddressSubtype_MatchesKnowledgeGraph(t *testing.T) {
	assert.Equal(t, core.UnresolvedVPCAddressSubtype, unresolvedVPCAddressSubtype)
}
