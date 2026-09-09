package triage

import (
	"encoding/json"
	"testing"

	kgcore "nudgebee/services/knowledge_graph/core"
)

// This is a contract test between two packages that were silently disagreeing.
//
// knowledge_graph/core writes the knowledge_graph evidence card, projecting node
// properties through an allowlist. triage reads that card to build the
// dependency graph correlation scores against, and joins an event to a node by
// the event's subject - a provider id like i-0dcee3621b8456783, while the node
// is named from its Name tag. resource_id and arn are the only bridge.
//
// Both halves of that join were written and tested, and neither test covered the
// allowlist sitting between them, which did not carry either key. Correlation
// therefore received the correct topology - order, payment, inventory, database,
// with real flow-derived edges - and still scored every pair at distance 0,
// because it could not tell which of those nodes the event was about.
//
// Asserting the allowlist directly would not have caught it either: the bug was
// that nobody checked the two ends agreed. So this test drives the real writer
// and the real reader, and fails if either end stops carrying identity.
func TestKgEvidenceCarriesIdentityForCorrelation(t *testing.T) {
	// Shaped like the live nodes on the dev account: named by Name tag, with the
	// instance id and ARN in properties.
	nodes := []kgcore.KgNode{
		{
			ID:        "25771ecf-d1f5-55ba-b376-28c550755252",
			NodeType:  "ComputeInstance",
			UniqueKey: "aws:acct:us-east-1:ComputeInstance:vpc-0222c53209254e33c:nudgebee-scenario-services-order",
			Properties: map[string]any{
				"name":        "nudgebee-scenario-services-order",
				"region":      "us-east-1",
				"status":      "Active",
				"resource_id": "i-0dcee3621b8456783",
				"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0dcee3621b8456783",
			},
		},
		{
			ID:        "0ff0c3a8-d5cd-5316-b638-9bbdf8835d39",
			NodeType:  "ComputeInstance",
			UniqueKey: "aws:acct:us-east-1:ComputeInstance:vpc-0222c53209254e33c:nudgebee-scenario-services-payment",
			Properties: map[string]any{
				"name":        "nudgebee-scenario-services-payment",
				"region":      "us-east-1",
				"status":      "Active",
				"resource_id": "i-0b079820a95b1517a",
				"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0b079820a95b1517a",
			},
		},
	}

	// The writer's own projection - not a hand-built copy of what it emits.
	evidenceNodes := kgcore.ToEvidenceNodes(nodes)

	nodesJSON, err := json.Marshal(evidenceNodes)
	if err != nil {
		t.Fatalf("marshalling projected nodes: %v", err)
	}
	var nodesAny []interface{}
	if err := json.Unmarshal(nodesJSON, &nodesAny); err != nil {
		t.Fatalf("re-reading projected nodes: %v", err)
	}

	evidence := map[string]interface{}{
		"type":  "knowledge_graph",
		"nodes": nodesAny,
		"edges": []interface{}{
			map[string]interface{}{
				"source_node_id":    "0ff0c3a8-d5cd-5316-b638-9bbdf8835d39",
				"dest_node_id":      "25771ecf-d1f5-55ba-b376-28c550755252",
				"relationship_type": "CALLS",
			},
		},
		"additional_info": map[string]interface{}{"action_name": "knowledge_graph_service_map"},
	}

	graph := parseKnowledgeGraphEvidence(evidence)
	if graph == nil {
		t.Fatal("no dependency graph parsed from the writer's own output")
	}

	// The join correlation actually performs: event subject -> graph node. Assert
	// the exact canonical key rather than "changed from the input": resolving to
	// the wrong instance is a worse failure than not resolving at all, and an
	// inequality check passes for both. The ARN entry is the CloudWatch case -
	// alarm events carry one in service_key.
	for subject, want := range map[string]string{
		"i-0dcee3621b8456783": ":ComputeInstance:nudgebee-scenario-services-order",
		"i-0b079820a95b1517a": ":ComputeInstance:nudgebee-scenario-services-payment",
		"arn:aws:ec2:us-east-1:864186153326:instance/i-0dcee3621b8456783": ":ComputeInstance:nudgebee-scenario-services-order",
	} {
		got := graph.resolveKey(subject)
		if got == subject {
			t.Errorf("subject %s did not resolve to a graph node at all.\n"+
				"The topology is present but no event can be matched to it, so every "+
				"pair scores dependency_distance 0. Check that resource_id and arn "+
				"survive the evidence property allowlist in knowledge_graph/core.", subject)
			continue
		}
		if got != want {
			t.Errorf("subject %s resolved to %q, want %q - correlation would score "+
				"this event against the wrong resource's dependencies", subject, got, want)
		}
	}
}

// Guard the projection itself, so a failure points at the allowlist rather than
// leaving the reader end under suspicion.
func TestEvidenceProjectionKeepsIdentityProperties(t *testing.T) {
	got := kgcore.ToEvidenceNodes([]kgcore.KgNode{{
		ID:       "n1",
		NodeType: "ComputeInstance",
		Properties: map[string]any{
			"name":        "orders-api",
			"resource_id": "i-0dcee3621b8456783",
			"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0dcee3621b8456783",
		},
	}})
	if len(got) != 1 {
		t.Fatalf("projected %d nodes, want 1", len(got))
	}
	for _, key := range []string{"resource_id", "arn"} {
		if _, ok := got[0].Properties[key]; !ok {
			t.Errorf("%q was dropped by the evidence allowlist; correlation cannot "+
				"join an event to this node without it", key)
		}
	}
}
