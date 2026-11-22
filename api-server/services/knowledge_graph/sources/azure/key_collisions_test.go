package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestMarkKeyCollisions(t *testing.T) {
	node := func(key, resourceID string) *core.DbNode {
		return &core.DbNode{UniqueKey: key, Properties: map[string]interface{}{"resource_id": resourceID}}
	}
	const subnetID = "/subscriptions/s/resourcegroups/rg/providers/microsoft.network/virtualnetworks/v/subnets/a"
	sameResourceA := node("k-subnet", subnetID)
	sameResourceB := node("k-subnet", "/SUBSCRIPTIONS/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v/subnets/a")
	otherA := node("k-zone", "/subscriptions/s/resourcegroups/rg1/providers/microsoft.network/privatednszones/z")
	otherB := node("k-zone", "/subscriptions/s/resourcegroups/rg2/providers/microsoft.network/privatednszones/z")
	alone := node("k-vm", "/subscriptions/s/resourcegroups/rg/providers/microsoft.compute/virtualmachines/vm")
	// Rows that store no Azure id are told apart by their inventory row id.
	noIDA := &core.DbNode{UniqueKey: "k-noid", Properties: map[string]interface{}{"nb_resource_id": "row-1"}}
	noIDB := &core.DbNode{UniqueKey: "k-noid", Properties: map[string]interface{}{"nb_resource_id": "row-2"}}
	// A node with no property map at all, in a colliding group, is stamped
	// rather than panicking.
	noProps := &core.DbNode{UniqueKey: "k-noid"}

	if got := markKeyCollisions([]*core.DbNode{sameResourceA, sameResourceB, otherA, otherB, alone, noIDA, noIDB, noProps}); got != 2 {
		t.Fatalf("collided keys = %d, want 2", got)
	}
	if noProps.Properties["key_collisions"] != 2 {
		t.Errorf("node without properties in a colliding group: key_collisions = %v, want 2", noProps.Properties["key_collisions"])
	}
	if noIDA.Properties["key_collisions"] != 2 {
		t.Errorf("rows without an Azure id sharing a key must be marked, got %v", noIDA.Properties["key_collisions"])
	}
	for _, n := range []*core.DbNode{sameResourceA, sameResourceB, alone} {
		if _, ok := n.Properties["key_collisions"]; ok {
			t.Errorf("%s: one resource under its own key must not be marked as a collision", n.UniqueKey)
		}
	}
	for _, n := range []*core.DbNode{otherA, otherB} {
		if n.Properties["key_collisions"] != 2 {
			t.Errorf("%s: key_collisions = %v, want 2", n.UniqueKey, n.Properties["key_collisions"])
		}
		if ids, _ := n.Properties["collided_resource_ids"].([]string); len(ids) != 2 {
			t.Errorf("%s: collided_resource_ids = %v, want both zone ids", n.UniqueKey, n.Properties["collided_resource_ids"])
		}
	}
}
