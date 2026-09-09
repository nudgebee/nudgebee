package triage

import (
	"fmt"
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real k8s events carry service_key in TWO-PART form ("ns/name", no kind) and
// lowercase owner kinds ("deployment"). The graph only registered three-part
// capitalized aliases, so every lookup keyed on a real service_key silently
// missed — no-opping incident topology grouping AND the legacy correlation
// engine's cross-service scoring. These tests pin the real-world key forms.
func aliasTestGraph(t *testing.T) *DependencyGraph {
	t.Helper()
	evJSON := `[{"type": "knowledge_graph", "nodes": [
	  {"id": "n1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "checkout", "namespace": "shop"}},
	  {"id": "n2", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "payments", "namespace": "shop"}}
	], "edges": [
	  {"relationship_type": "CALLS", "source_node_id": "n1", "dest_node_id": "n2"}
	]}]`
	var evidences models.Json
	require.NoError(t, evidences.Scan([]uint8(evJSON)))
	graph, err := parseServiceMapFromEvent(&models.Event{Id: "alias-test", Evidences: &evidences})
	require.NoError(t, err)
	require.NotNil(t, graph)
	return graph
}

func TestResolveKey_TwoPartServiceKey(t *testing.T) {
	g := aliasTestGraph(t)
	// The form events.service_key actually carries.
	assert.Equal(t, 1, g.getDependencyDistance("shop/checkout", "shop/payments"))
	assert.Equal(t, 1, g.getDependencyDistance("shop:checkout", "shop:payments"))
}

func TestResolveKey_LowercaseKind(t *testing.T) {
	g := aliasTestGraph(t)
	assert.Equal(t, 1, g.getDependencyDistance("shop:deployment:checkout", "shop:deployment:payments"))
	assert.Equal(t, 1, g.getDependencyDistance("shop/deployment/checkout", "shop/payments"))
}

func TestGetServiceKeyFromEvent_RealFormResolves(t *testing.T) {
	g := aliasTestGraph(t)
	sk := "shop/checkout"
	kind := "deployment"
	owner := "checkout"
	ns := "shop"
	ev := &models.Event{ServiceKey: &sk, SubjectOwner: &owner, SubjectOwnerKind: &kind, SubjectNamespace: &ns}
	key := getServiceKeyFromEvent(ev)
	require.Equal(t, "shop/checkout", key)
	assert.Equal(t, 1, g.getDependencyDistance(key, "shop/payments"),
		fmt.Sprintf("a real event service_key (%s) must resolve against the graph", key))
}
