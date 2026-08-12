package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestToEvidenceNodesKeepsOnlyAllowlistedProperties is the regression for an
// evidence block that carried every stored property of every neighbour node:
// kubectl.kubernetes.io/last-applied-configuration alone was 22,539 of 72,671
// bytes on a measured event, echoing each node's applied spec back as an
// annotation string no consumer of this evidence reads.
func TestToEvidenceNodesKeepsOnlyAllowlistedProperties(t *testing.T) {
	nodes := []KgNode{
		{
			ID:           "node-1",
			NodeType:     NodeTypeWorkload,
			SpecificType: "KubernetesDeployment",
			UniqueKey:    "otel-demo/Deployment/accounting",
			Properties: map[string]any{
				"name":      "accounting",
				"namespace": "otel-demo",
				"cluster":   "prod-use1",
				"kind":      "Deployment",
				"phase":     "Running",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"apps/v1","kind":"Deployment"}`,
				},
				"container_images":      []string{"accounting:1.2.3"},
				"total_memory_requests": "512Mi",
			},
			Labels: map[string]string{"app": "accounting"},
		},
	}

	evidenceNodes := toEvidenceNodes(nodes)

	if len(evidenceNodes) != 1 {
		t.Fatalf("toEvidenceNodes() returned %d nodes, want 1", len(evidenceNodes))
	}
	node := evidenceNodes[0]
	if node.ID != "node-1" || node.NodeType != NodeTypeWorkload ||
		node.SpecificType != "KubernetesDeployment" || node.UniqueKey != "otel-demo/Deployment/accounting" {
		t.Errorf("identity fields not carried through: %+v", node)
	}
	for key, want := range map[string]string{
		"name": "accounting", "namespace": "otel-demo",
		"cluster": "prod-use1", "kind": "Deployment", "phase": "Running",
	} {
		if node.Properties[key] != want {
			t.Errorf("properties[%q] = %v, want %q", key, node.Properties[key], want)
		}
	}
	for _, key := range []string{"annotations", "container_images", "total_memory_requests"} {
		if _, present := node.Properties[key]; present {
			t.Errorf("properties[%q] should have been dropped", key)
		}
	}

	// The dropped properties must not survive anywhere else on the wire —
	// Labels and the remaining KgNode fields are gone from the projection too.
	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "last-applied-configuration") {
		t.Errorf("serialized evidence node still carries the annotation: %s", encoded)
	}
}

// TestToEvidenceNodesOmitsAbsentAndNilProperties keeps the projection from
// re-adding bytes as nulls: a cloud node has no namespace/kind, and sources
// occasionally store an explicit nil.
func TestToEvidenceNodesOmitsAbsentAndNilProperties(t *testing.T) {
	nodes := []KgNode{
		{
			ID:       "node-2",
			NodeType: NodeTypeDatabase,
			Properties: map[string]any{
				"name":   "orders-primary",
				"engine": "postgres",
				"region": "us-east-1",
				"phase":  nil,
			},
		},
	}

	properties := toEvidenceNodes(nodes)[0].Properties

	if len(properties) != 3 {
		t.Errorf("properties = %v, want exactly name/engine/region", properties)
	}
	if _, present := properties["phase"]; present {
		t.Error("nil-valued property should be omitted, not emitted as null")
	}
	if _, present := properties["namespace"]; present {
		t.Error("absent property should be omitted")
	}
}

// TestToEvidenceNodesToleratesNilPropertiesAndEmptyInput covers the shapes the
// projection must not panic on. Properties is a free-form map populated by many
// source packages, and a node can reach the evidence block without one.
func TestToEvidenceNodesToleratesNilPropertiesAndEmptyInput(t *testing.T) {
	evidenceNodes := toEvidenceNodes([]KgNode{{ID: "nil-properties", NodeType: NodeTypeService}})

	if len(evidenceNodes) != 1 {
		t.Fatalf("toEvidenceNodes() returned %d nodes, want 1", len(evidenceNodes))
	}
	if evidenceNodes[0].Properties == nil {
		t.Error("Properties should be an empty map, not nil, so consumers can index it")
	}

	if got := toEvidenceNodes(nil); len(got) != 0 {
		t.Errorf("toEvidenceNodes(nil) = %v, want empty", got)
	}
}
