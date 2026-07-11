package sources

import (
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestBuildPrivateDNSVNetEdges(t *testing.T) {
	// Zone and VNet live in DIFFERENT subscriptions/accounts — the cross-account
	// case the per-account source could not resolve.
	zoneID := "/subscriptions/sub-a/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/privatelink.blob.core.windows.net"
	vnetID := "/subscriptions/sub-b/resourceGroups/rg2/providers/Microsoft.Network/virtualNetworks/vnet1"

	zone := &core.DbNode{ID: "zone1", NodeType: core.NodeTypeDNSZone, CloudAccountID: "acct-a",
		Properties: map[string]interface{}{"resource_id": strings.ToLower(zoneID)}}
	vnet := &core.DbNode{ID: "vnet1", NodeType: core.NodeTypeVPC, CloudAccountID: "acct-b",
		Properties: map[string]interface{}{"arn": strings.ToLower(vnetID)}}

	index := map[string]*core.DbNode{
		strings.ToLower(zoneID): zone,
		strings.ToLower(vnetID): vnet,
	}

	t.Run("cross-account link resolves; dedup; unknown vnet skipped", func(t *testing.T) {
		links := []dnsVNetLink{
			{ZoneID: zoneID, VnetID: vnetID},                                           // PascalCase, cross-account
			{ZoneID: zoneID, VnetID: vnetID},                                           // duplicate -> one edge
			{ZoneID: zoneID, VnetID: "/subscriptions/sub-c/.../virtualNetworks/ghost"}, // vnet not in graph
			{ZoneID: "", VnetID: vnetID},                                               // malformed
		}
		edges := buildPrivateDNSVNetEdges(links, index, "t1")
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		e := edges[0]
		if e.SourceNodeID != "zone1" || e.DestinationNodeID != "vnet1" || e.RelationshipType != core.RelationshipAssociatedWith {
			t.Errorf("unexpected edge: %s --%s--> %s", e.SourceNodeID, e.RelationshipType, e.DestinationNodeID)
		}
	})

	t.Run("empty links -> no edges", func(t *testing.T) {
		if got := buildPrivateDNSVNetEdges(nil, index, "t1"); len(got) != 0 {
			t.Errorf("expected no edges, got %d", len(got))
		}
	})
}
