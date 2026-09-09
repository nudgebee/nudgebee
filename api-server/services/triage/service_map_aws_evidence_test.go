package triage

import (
	"fmt"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AWS cloud events are the only source that carries BOTH a service_map card and
// a knowledge_graph card, and the collector writes them in that order. Two
// defects compounded from that:
//
//  1. parseServiceMapFromEvent returned on the first match, so it always built
//     the graph from cloud_service_map — which for AWS is a single isolated node
//     with empty Upstreams/Downstreams (35/35 events measured on test).
//  2. the KG edge parser accepted only CALLS, while every AWS edge is ROUTES_TO.
//
// Together they meant no AWS event could ever score dependency_distance > 0, so
// AWS produced zero upstream_dependency / downstream_impact / likely_root_cause
// correlations. These tests pin both halves.

// awsEmptyServiceMapJSON is the cloud_service_map card as the collector writes
// it for an SQS alarm: one node, no upstreams, no downstreams.
const awsEmptyServiceMapJSON = `{"type": "service_map", "data": "{\"data\":[{\"Id\":{\"name\":\"rackspace-eventbridge-queue\",\"kind\":\"queueservice\",\"namespace\":\"us-east-1\"},\"Upstreams\":[],\"Downstreams\":[],\"Status\":\"Unknown\"}]}"}`

// awsKnowledgeGraphJSON is the knowledge_graph card for the same event: the
// queue and its dead-letter queue, joined by a ROUTES_TO edge.
const awsKnowledgeGraphJSON = `{
  "type": "knowledge_graph",
  "namespace": "AWSQueueService",
  "target_service": "rackspace-eventbridge-queue",
  "nodes": [
    {"id": "q1", "node_type": "MessageQueue",
     "properties": {"name": "rackspace-eventbridge-queue", "region": "us-east-1", "namespace": "us-east-1"}},
    {"id": "q2", "node_type": "MessageQueue",
     "properties": {"name": "rackspace-eventbridge-dlq", "region": "us-east-1", "namespace": "us-east-1"}}
  ],
  "edges": [
    {"relationship_type": "ROUTES_TO", "source_node_id": "q1", "dest_node_id": "q2",
     "properties": {"connection_type": "dead_letter_queue", "contributing_sources": ["aws"]}}
  ]
}`

func awsEvidenceEvent(t *testing.T, evidenceJSON string) *models.Event {
	t.Helper()
	var evidences models.Json
	require.NoError(t, evidences.Scan([]uint8(evidenceJSON)))
	return &models.Event{Id: "aws-evidence-test", Evidences: &evidences}
}

func TestParseServiceMapFromEvent_PrefersKnowledgeGraphOverEarlierServiceMap(t *testing.T) {
	// service_map comes FIRST, exactly as the collector orders it. The edgeless
	// service_map must not win over the knowledge_graph that has a real edge.
	ev := awsEvidenceEvent(t, fmt.Sprintf(`[%s, %s]`, awsEmptyServiceMapJSON, awsKnowledgeGraphJSON))

	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)

	assert.Len(t, graph.Nodes, 2, "must have built the graph from the knowledge_graph card, not the single-node service_map")
	assert.Equal(t, 1,
		graph.getDependencyDistance("us-east-1:MessageQueue:rackspace-eventbridge-queue", "us-east-1:MessageQueue:rackspace-eventbridge-dlq"),
		"queue ROUTES_TO its dead-letter queue must be one hop")
}

func TestParseServiceMapFromEvent_RoutesToCountsAsDependency(t *testing.T) {
	// The KG card alone: ROUTES_TO must register as a dependency edge. Before
	// this, only CALLS did, so AWS graphs had nodes but never edges.
	ev := awsEvidenceEvent(t, fmt.Sprintf(`[%s]`, awsKnowledgeGraphJSON))

	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)

	src := "us-east-1:MessageQueue:rackspace-eventbridge-queue"
	dst := "us-east-1:MessageQueue:rackspace-eventbridge-dlq"
	assert.NotEmpty(t, graph.Edges, "ROUTES_TO must produce a dependency edge")
	assert.True(t, graph.isUpstream(src, dst), "the routing source is upstream of its target")
	assert.Equal(t, 1, graph.getDependencyDistance(src, dst))
}

func TestParseServiceMapFromEvent_NonDependencyRelationshipsIgnored(t *testing.T) {
	// EXPOSES / MOUNTS are containment relations, not traffic dependencies.
	// Admitting them would put a hop between a Service and its own Workload.
	ev := awsEvidenceEvent(t, `[{
      "type": "knowledge_graph",
      "nodes": [
        {"id": "s1", "node_type": "K8sService", "properties": {"name": "checkout", "namespace": "shop"}},
        {"id": "w1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "checkout", "namespace": "shop"}}
      ],
      "edges": [{"relationship_type": "EXPOSES", "source_node_id": "s1", "dest_node_id": "w1"}]
    }]`)

	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)
	assert.Empty(t, graph.Edges, "EXPOSES must not create a dependency hop")
}

func TestParseServiceMapFromEvent_ServiceMapStillUsedWhenItIsTheOnlyEvidence(t *testing.T) {
	// Sources that emit only a service_map (traces_dependency_map, the k8s-agent
	// service_map_enricher) must be unaffected by the preference change.
	ev := awsEvidenceEvent(t, `[{"type": "service_map", "data": "{\"data\":[{\"Id\":{\"name\":\"checkout\",\"kind\":\"Deployment\",\"namespace\":\"shop\"},\"Upstreams\":[],\"Downstreams\":[{\"Id\":{\"name\":\"payments\",\"kind\":\"Deployment\",\"namespace\":\"shop\"},\"Status\":0}],\"Status\":\"Healthy\"}]}"}]`)

	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)
	assert.Equal(t, 1, graph.getDependencyDistance("shop:Deployment:checkout", "shop:Deployment:payments"))
}

func TestParseServiceMapFromEvent_MalformedServiceMapDoesNotHideKnowledgeGraph(t *testing.T) {
	// A service_map that fails to unmarshal used to abort the scan with an error
	// even when a usable knowledge_graph card followed it.
	ev := awsEvidenceEvent(t, fmt.Sprintf(
		`[{"type": "service_map", "data": "{not-json"}, %s]`, awsKnowledgeGraphJSON))

	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)
	assert.Len(t, graph.Nodes, 2)
}

func TestParseServiceMapFromEvent_MalformedServiceMapAloneStillErrors(t *testing.T) {
	ev := awsEvidenceEvent(t, `[{"type": "service_map", "data": "{not-json"}]`)

	graph, err := parseServiceMapFromEvent(ev)
	assert.Error(t, err)
	assert.Nil(t, graph)
}
