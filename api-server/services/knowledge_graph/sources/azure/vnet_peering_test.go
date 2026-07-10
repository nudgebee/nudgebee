package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestExtractVNetPeeredIDs(t *testing.T) {
	s := &AzureSource{}
	props := map[string]interface{}{"resource_id": "/subscriptions/s1/.../virtualNetworks/hub"}
	// metaMap here is the `properties` sub-object (as classify.go passes propsMap).
	meta := map[string]interface{}{
		"virtualNetworkPeerings": []interface{}{
			map[string]interface{}{"properties": map[string]interface{}{
				"remoteVirtualNetwork": map[string]interface{}{"id": "/subscriptions/s1/.../virtualNetworks/spoke-a"},
			}},
			map[string]interface{}{"properties": map[string]interface{}{
				"remoteVirtualNetwork": map[string]interface{}{"id": "/subscriptions/s1/.../virtualNetworks/spoke-b"},
			}},
		},
	}
	s.extractVNetMetadata(props, meta)

	peered, ok := props["peered_vnet_ids"].([]string)
	if !ok || len(peered) != 2 {
		t.Fatalf("peered_vnet_ids = %v, want 2 remote IDs", props["peered_vnet_ids"])
	}
}

func TestCreateVNetPeeringEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AzureSource{}

	hub := &core.DbNode{ID: "hub-node", NodeType: core.NodeTypeVPC, Properties: map[string]interface{}{
		"resource_id":     "/subscriptions/s1/vnets/hub",
		"peered_vnet_ids": []string{"/subscriptions/s1/vnets/spoke", "/subscriptions/s1/vnets/missing"},
	}}
	spoke := &core.DbNode{ID: "spoke-node", NodeType: core.NodeTypeVPC, Properties: map[string]interface{}{
		"resource_id":     "/subscriptions/s1/vnets/spoke",
		"peered_vnet_ids": []string{"/subscriptions/s1/vnets/hub"},
	}}

	edges := s.createVNetPeeringEdges([]*core.DbNode{hub, spoke}, req)

	// hub→spoke + spoke→hub = 2 edges; hub→missing is dropped (no target node).
	if len(edges) != 2 {
		t.Fatalf("expected 2 peering edges (hub↔spoke), got %d", len(edges))
	}
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		if e.RelationshipType != core.RelationshipAssociatedWith || conn != "vnet_peering" {
			t.Errorf("edge = %s/%s, want ASSOCIATED_WITH/vnet_peering", e.RelationshipType, conn)
		}
	}
}
