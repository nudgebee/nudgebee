package triage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDependencyGraph_AliasBridgesEventKeyMismatch pins the Stage-3 correlation
// fix. In dev we observed zero event_correlations for an otel-demo incident
// even though the knowledge-graph held the fraud-detection → flagd edge. The
// root cause was a key-format mismatch:
//
//   graph canonical key:           "demo:Workload:flagd"       (from node_type)
//   kubernetes_api_server events:  "demo/Deployment/flagd"     (slashes, Deployment)
//   pagerduty/anomaly fallback:    "demo:Deployment:flagd"     (colons, Deployment)
//
// Every graph lookup returned -1, dropping the correlation score to time-only
// (0.30) — below the 0.50 link threshold. The alias layer maps every event-side
// variant back to the canonical Workload key.
func TestDependencyGraph_AliasBridgesEventKeyMismatch(t *testing.T) {
	// Reproduce the actual knowledge-graph shape produced by the collector for
	// the fraud-detection OtelDemoGRPCClientErrorRate alert.
	evidence := map[string]interface{}{
		"type": "knowledge_graph",
		"data": map[string]interface{}{
			"nodes": []interface{}{
				// Workload node wins the alias — it carries CALLS edges.
				map[string]interface{}{
					"id":        "w-fraud",
					"node_type": "Workload",
					"properties": map[string]interface{}{
						"name":      "fraud-detection",
						"namespace": "demo",
						"kind":      "Deployment",
					},
				},
				// Same-named K8sService must NOT steal the alias.
				map[string]interface{}{
					"id":        "svc-flagd",
					"node_type": "K8sService",
					"properties": map[string]interface{}{
						"name":      "flagd",
						"namespace": "demo",
					},
				},
				map[string]interface{}{
					"id":        "w-flagd",
					"node_type": "Workload",
					"properties": map[string]interface{}{
						"name":      "flagd",
						"namespace": "demo",
						"kind":      "Deployment",
					},
				},
			},
			"edges": []interface{}{
				map[string]interface{}{
					"source_node_id":    "w-fraud",
					"dest_node_id":      "w-flagd",
					"relationship_type": "CALLS",
				},
			},
		},
	}

	graph := parseKnowledgeGraphEvidence(evidence)
	require.NotNil(t, graph)

	// Canonical keys still resolve to themselves.
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo:Workload:flagd"))
	assert.Equal(t, "demo:Workload:fraud-detection", graph.resolveKey("demo:Workload:fraud-detection"))

	// k8s_api_server (slash-separated "Deployment") → Workload canonical.
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo/Deployment/flagd"),
		"kubernetes_api_server event key must resolve to the Workload node")
	assert.Equal(t, "demo:Workload:fraud-detection", graph.resolveKey("demo/Deployment/fraud-detection"))

	// pagerduty / anomaly fallback builds colon-separated "Deployment".
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo:Deployment:flagd"))
	assert.Equal(t, "demo:Workload:fraud-detection", graph.resolveKey("demo:Deployment:fraud-detection"))

	// Workload wins over K8sService for the Deployment alias — the Workload is
	// the node that carries CALLS edges, so the event-side "Deployment" key
	// must route through it even though a same-named K8sService exists.
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo:Deployment:flagd"),
		"Workload must keep the Deployment alias even when a same-named K8sService exists")

	// The BFS can now find the edge regardless of the event-side key format.
	dist := graph.getDependencyDistance("demo/Deployment/fraud-detection", "demo:Deployment:flagd")
	assert.Equal(t, 1, dist, "direct CALLS edge must be distance 1 across key variants")

	// Direction semantics preserved: fraud-detection CALLS flagd, so flagd is
	// a downstream of fraud-detection (and fraud-detection is upstream of flagd).
	assert.True(t, graph.isDownstream("demo/Deployment/flagd", "demo/Deployment/fraud-detection"),
		"flagd is reachable downstream from fraud-detection via CALLS")
	assert.True(t, graph.isUpstream("demo/Deployment/fraud-detection", "demo/Deployment/flagd"),
		"fraud-detection is upstream of flagd in the CALLS graph")
}

// TestDependencyGraph_UnknownKeyReturnedUnchanged locks in that the alias layer
// does not invent non-existent routes for keys that don't match any node — the
// resolver returns the input verbatim so lookups still fail predictably.
func TestDependencyGraph_UnknownKeyReturnedUnchanged(t *testing.T) {
	graph := &DependencyGraph{
		Nodes: map[string]*ServiceNode{"demo:Workload:a": {}},
	}
	assert.Equal(t, "other-namespace:Deployment:zzz", graph.resolveKey("other-namespace:Deployment:zzz"))
	assert.Equal(t, "", graph.resolveKey(""))
}

// TestDependencyGraph_LegacyServiceMapGetsAliases confirms the alias treatment
// is applied to the legacy service_map evidence path (buildDependencyGraph), not
// just the newer knowledge_graph path.
func TestDependencyGraph_LegacyServiceMapGetsAliases(t *testing.T) {
	nodes := []ServiceNode{
		{
			ID: struct {
				Namespace string `json:"namespace"`
				Kind      string `json:"kind"`
				Name      string `json:"name"`
			}{
				Namespace: "demo",
				Kind:      "Workload",
				Name:      "frontend",
			},
		},
	}
	graph := buildDependencyGraph(nodes)
	assert.Equal(t, "demo:Workload:frontend", graph.resolveKey("demo/Deployment/frontend"))
	assert.Equal(t, "demo:Workload:frontend", graph.resolveKey("demo:Deployment:frontend"))
}

// TestDependencyGraph_LegacyServiceMapHonorsPriority verifies the legacy
// buildDependencyGraph path applies the same node-type priority ordering as
// parseKnowledgeGraphEvidence — so the "Deployment" alias resolves to the
// Workload node even when a K8sService precedes it in the node slice.
// Regression guard for PR #29241 review comment: the legacy path was
// previously registering aliases in slice order without sorting.
func TestDependencyGraph_LegacyServiceMapHonorsPriority(t *testing.T) {
	// Deliberately put the K8sService first — without priority sorting it would
	// grab the "demo:Deployment:flagd" alias.
	nodes := []ServiceNode{
		{ID: struct {
			Namespace string `json:"namespace"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
		}{Namespace: "demo", Kind: "K8sService", Name: "flagd"}},
		{ID: struct {
			Namespace string `json:"namespace"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
		}{Namespace: "demo", Kind: "Workload", Name: "flagd"}},
	}
	graph := buildDependencyGraph(nodes)
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo:Deployment:flagd"),
		"Workload must win the Deployment alias regardless of node slice order")
	assert.Equal(t, "demo:Workload:flagd", graph.resolveKey("demo/Deployment/flagd"))
}

// TestCorrelation_FlagdFraudDetection_LinksAcrossKeyFormats is the end-to-end
// assertion — with the alias layer, a flagd ConfigurationChange event
// (kubernetes_api_server format) and a fraud-detection OtelDemoGRPCClientErrorRate
// event (empty service_key → fallback) must correlate through the graph.
func TestCorrelation_FlagdFraudDetection_LinksAcrossKeyFormats(t *testing.T) {
	now := time.Now()
	flagdEvent := makeEvent("flagd-change", now,
		withServiceKey("demo/Deployment/flagd"), // k8s_api_server format
		withNamespace("demo"),
	)
	fraudEvent := makeEvent("fraud-error", now.Add(6*time.Minute),
		// pagerduty_webhook: empty service_key, fallback will build
		// "demo:Deployment:fraud-detection".
		withServiceKey(""),
		withNamespace("demo"),
		withSubjectName("fraud-detection"),
	)

	// Graph shape matches the real evidence.
	kgJSON := `{
		"nodes": [
			{"id":"wf","node_type":"Workload","properties":{"name":"fraud-detection","namespace":"demo","kind":"Deployment"}},
			{"id":"wg","node_type":"Workload","properties":{"name":"flagd","namespace":"demo","kind":"Deployment"}}
		],
		"edges": [
			{"source_node_id":"wf","dest_node_id":"wg","relationship_type":"CALLS"}
		]
	}`
	var kg map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(kgJSON), &kg))
	graph := parseKnowledgeGraphEvidence(map[string]interface{}{"type": "knowledge_graph", "data": kg})
	require.NotNil(t, graph)

	result := calculateCorrelationScore(flagdEvent, fraudEvent, graph, nil)

	assert.True(t, result.IsCorrelated,
		"flagd config change and fraud-detection error must correlate via the CALLS edge")
	assert.Equal(t, 1, result.DependencyDistance, "direct dependency")
	// time(0.05, 5-10min window) + dependency(0.40, direct) + namespace(0.10) = 0.55
	// correlationType is downstream_impact: flagd is fraud-detection's downstream
	// in the CALLS graph direction, so no causality bonus is awarded under the
	// current semantics. The score still clears the 0.50 threshold.
	assert.GreaterOrEqual(t, result.CorrelationScore, 0.50)
}
