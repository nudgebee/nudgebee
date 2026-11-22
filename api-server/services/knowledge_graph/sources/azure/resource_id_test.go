package azure

import "testing"

func TestAzureKeyParts(t *testing.T) {
	const sub = "/subscriptions/00000000-0000-0000-0000-000000000001"
	tests := []struct {
		name, resourceID, rowName string
		wantRG, wantKeyName       string
	}{
		{"top-level resource keeps its own name", sub + "/resourceGroups/RG-Shop/providers/Microsoft.Compute/virtualMachines/web-01", "web-01", "rg-shop", "web-01"},
		{"resource group matched in any case", sub + "/resourcegroups/rg-shop/providers/microsoft.network/virtualnetworks/Shop-VNet", "Shop-VNet", "rg-shop", "Shop-VNet"},
		{"sql database carries its server and type", sub + "/resourcegroups/rg-shop/providers/microsoft.sql/servers/sqlsrv1/databases/orders", "Orders", "rg-shop", "sqlsrv1/databases/Orders"},
		{"blob service carries its storage account and type", sub + "/resourceGroups/rg-shop/providers/Microsoft.Storage/storageAccounts/ShopStore/blobServices/default", "default", "rg-shop", "shopstore/blobservices/default"},
		{"file service of the same account is a different key", sub + "/resourceGroups/rg-shop/providers/Microsoft.Storage/storageAccounts/ShopStore/fileServices/default", "default", "rg-shop", "shopstore/fileservices/default"},
		{"subnet carries its vnet", sub + "/resourcegroups/rg-shop/providers/microsoft.network/virtualnetworks/shop-vnet/subnets/app-subnet", "app-subnet", "rg-shop", "shop-vnet/subnets/app-subnet"},
		{"row name that already holds the parent", sub + "/resourcegroups/rg-shop/providers/microsoft.sql/servers/sqlsrv1/databases/orders", "sqlsrv1/orders", "rg-shop", "sqlsrv1/databases/orders"},
		{"three levels, row name with the full path", sub + "/resourcegroups/rg/providers/microsoft.a/b/n1/c/n2/d/n3", "n1/n2/n3", "rg", "n1/c/n2/d/n3"},
		{"three levels, row name with part of the path", sub + "/resourcegroups/rg/providers/microsoft.a/b/n1/c/n2/d/n3", "n2/n3", "rg", "n1/c/n2/d/n3"},
		{"three levels, row name alone", sub + "/resourcegroups/rg/providers/microsoft.a/b/n1/c/n2/d/n3", "n3", "rg", "n1/c/n2/d/n3"},
		{"extension resource counts the resource it extends", sub + "/resourceGroups/rg-shop/providers/Microsoft.Compute/virtualMachines/vm1/providers/Microsoft.Insights/diagnosticSettings/default", "default", "rg-shop", "vm1/diagnosticsettings/default"},
		{"subscription-level resource has no resource group", sub + "/providers/microsoft.insights/pricings/", "pricing-a", "", "pricing-a"},
		{"tenant-level child keeps its parent and type", "/providers/microsoft.capacity/reservationorders/order-1/reservations/res-1", "res-1", "", "order-1/reservations/res-1"},
		{"name taken from the id when the row has none", sub + "/resourcegroups/rg-shop/providers/microsoft.web/sites/shop-web", "", "rg-shop", "shop-web"},
		{"declared node typed as a path, no id", "", "SqlSrv1/Databases/Orders", "", "sqlsrv1/databases/Orders"},
		{"declared top-level node, no id", "", "web-01", "", "web-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rg, keyName := azureKeyParts(tt.resourceID, tt.rowName)
			if rg != tt.wantRG || keyName != tt.wantKeyName {
				t.Errorf("azureKeyParts(%q, %q) = (%q, %q), want (%q, %q)", tt.resourceID, tt.rowName, rg, keyName, tt.wantRG, tt.wantKeyName)
			}
		})
	}
}

func TestNormalizeAzureRegion(t *testing.T) {
	for in, want := range map[string]string{
		"South Central US": "southcentralus",
		"southcentralus":   "southcentralus",
		"UK South":         "uksouth",
		"CentralIndia":     "centralindia",
		"global":           "global",
		"":                 "",
	} {
		if got := normalizeAzureRegion(in); got != want {
			t.Errorf("normalizeAzureRegion(%q) = %q, want %q", in, got, want)
		}
	}
}
