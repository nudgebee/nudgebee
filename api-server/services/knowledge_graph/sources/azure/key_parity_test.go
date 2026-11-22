package azure

import (
	"encoding/json"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// A subnet is built from its VNet's metadata when it has no inventory row of
// its own, and from that row when it does. A build can see either, so both must
// key the subnet the same way, or the subnet changes node id when its row
// appears.
func TestAzureSubnetKeySameFromVNetMetaAndOwnRow(t *testing.T) {
	const vnetID = "/subscriptions/s/resourceGroups/RG-Net/providers/Microsoft.Network/virtualNetworks/shop-vnet"
	req := &core.SourceBuildRequest{TenantID: "t", CloudAccountID: "a"}
	source, _ := NewAzureSource(AzureSourceConfig{}, nil)
	h := NewAzureSourceTestHelper(source)

	vnetMeta, _ := json.Marshal(map[string]interface{}{"properties": map[string]interface{}{
		"subnets": []interface{}{map[string]interface{}{"id": vnetID + "/subnets/app-subnet", "name": "app-subnet"}},
	}})
	fromMeta, _ := h.ConvertResourcesToGraph(nil, []sources.CloudResourceRow{{
		ID: "1", ResourceID: vnetID, ARN: vnetID, Name: "shop-vnet", Type: "virtualnetworks",
		ServiceName: "microsoft.network/virtualnetworks", CloudProvider: "Azure", Region: "East US",
		Tenant: "t", Account: "a", IsActive: true, Meta: vnetMeta, Tags: json.RawMessage(`{}`),
	}}, req)

	subnetID := "/subscriptions/s/resourcegroups/rg-net/providers/microsoft.network/virtualnetworks/shop-vnet/subnets/app-subnet"
	fromRow, _ := h.ConvertResourcesToGraph(nil, []sources.CloudResourceRow{{
		ID: "2", ResourceID: subnetID, ARN: subnetID, Name: "app-subnet", Type: "subnets",
		ServiceName: "microsoft.network/virtualnetworks/subnets", CloudProvider: "Azure", Region: "eastus",
		Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
	}}, req)

	subnetKey := func(nodes []*core.DbNode) string {
		for _, n := range nodes {
			if n.NodeType == core.NodeTypeSubnet {
				return n.UniqueKey
			}
		}
		return ""
	}
	metaKey, rowKey := subnetKey(fromMeta), subnetKey(fromRow)
	if metaKey == "" || rowKey == "" {
		t.Fatalf("expected a subnet from both paths, got %q and %q", metaKey, rowKey)
	}
	if metaKey != rowKey {
		t.Errorf("subnet keyed differently\n from VNet meta: %s\n from its row:   %s", metaKey, rowKey)
	}
	if want := "azure:a:eastus:Subnet:rg-net:shop-vnet/subnets/app-subnet"; rowKey != want {
		t.Errorf("subnet key = %q, want %q", rowKey, want)
	}
}
