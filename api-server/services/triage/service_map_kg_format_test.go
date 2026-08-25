package triage

import (
	"encoding/json"
	"fmt"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The knowledge_graph evidence has two shapes in the wild: the current writer
// puts nodes/edges at the evidence top level; older events nested them under
// "data". The parser knowing only the old shape meant every current event
// failed to parse and cross-service correlation scored at time-only — these
// tests pin both shapes.

const kgNodesJSON = `[
  {"id": "n1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "checkout", "namespace": "shop"}},
  {"id": "n2", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "payments", "namespace": "shop"}}
]`

const kgEdgesJSON = `[
  {"relationship_type": "CALLS", "source_node_id": "n1", "dest_node_id": "n2",
   "properties": {"contributing_sources": ["traces"]}}
]`

func kgEvidenceEvent(t *testing.T, evidenceJSON string) *models.Event {
	t.Helper()
	var evidences models.Json
	require.NoError(t, evidences.Scan([]uint8(evidenceJSON)))
	return &models.Event{Id: "kg-format-test", Evidences: &evidences}
}

func assertCheckoutCallsPayments(t *testing.T, ev *models.Event) {
	t.Helper()
	graph, err := parseServiceMapFromEvent(ev)
	require.NoError(t, err)
	require.NotNil(t, graph)
	assert.Len(t, graph.Nodes, 2)
	assert.Equal(t, 1, graph.getDependencyDistance("shop:Workload:checkout", "shop:Workload:payments"),
		"checkout CALLS payments must be one hop")
}

func TestParseKnowledgeGraphEvidence_TopLevelNodes(t *testing.T) {
	// Current writer shape: nodes/edges as siblings of "type".
	ev := kgEvidenceEvent(t, fmt.Sprintf(
		`[{"type": "knowledge_graph", "namespace": "shop", "target_service": "checkout", "nodes": %s, "edges": %s}]`,
		kgNodesJSON, kgEdgesJSON))
	assertCheckoutCallsPayments(t, ev)
}

func TestParseKnowledgeGraphEvidence_DataWrappedNodes(t *testing.T) {
	// Legacy shape: nodes/edges under "data" as an object.
	ev := kgEvidenceEvent(t, fmt.Sprintf(
		`[{"type": "knowledge_graph", "data": {"nodes": %s, "edges": %s}}]`,
		kgNodesJSON, kgEdgesJSON))
	assertCheckoutCallsPayments(t, ev)
}

func TestParseKnowledgeGraphEvidence_DataAsJSONString(t *testing.T) {
	// Legacy shape variant: "data" is a JSON-encoded string.
	inner, err := json.Marshal(map[string]json.RawMessage{
		"nodes": json.RawMessage(kgNodesJSON),
		"edges": json.RawMessage(kgEdgesJSON),
	})
	require.NoError(t, err)
	outer, err := json.Marshal([]map[string]any{{"type": "knowledge_graph", "data": string(inner)}})
	require.NoError(t, err)
	ev := kgEvidenceEvent(t, string(outer))
	assertCheckoutCallsPayments(t, ev)
}

func TestParseKnowledgeGraphEvidence_EmptyStaysUnparsed(t *testing.T) {
	// No nodes anywhere — parse must fail cleanly, not return an empty graph
	// (an empty graph would make correlation treat every pair as unrelated
	// with map-coverage confidence it doesn't have).
	ev := kgEvidenceEvent(t, `[{"type": "knowledge_graph", "namespace": "shop"}]`)
	graph, err := parseServiceMapFromEvent(ev)
	assert.Error(t, err)
	assert.Nil(t, graph)
}
