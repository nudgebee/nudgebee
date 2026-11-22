package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

const (
	manualTestTenant  = "11111111-1111-1111-1111-111111111111"
	manualTestAccount = "22222222-2222-2222-2222-222222222222"
	manualTestSub     = "/subscriptions/44444444-4444-4444-4444-444444444444"
)

// TestManualNode_AzureKeyMatchesCollector: every declarable Azure type gets the
// node id the collector writes for the same resource. See the AWS test. The
// collected row is shaped like the real one: a full resource id whose resource
// group is in another case than the one typed, and the region in its display
// spelling.
func TestManualNode_AzureKeyMatchesCollector(t *testing.T) {
	b, ok := core.ManualNodeBuilderFor("Azure")
	if !ok {
		t.Fatal("no Azure manual node builder registered")
	}
	declarable := b.Kinds()
	if len(declarable) < 5 {
		t.Fatalf("expected the common Azure types to be declarable, got %d", len(declarable))
	}
	entries := map[string]sources.ManualNodeEntry{}
	for _, e := range azureManualCandidates() {
		if _, seen := entries[e.SpecificType]; !seen {
			entries[e.SpecificType] = e
		}
	}

	src, err := NewAzureSource(AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource: %v", err)
	}
	req := &core.SourceBuildRequest{TenantID: manualTestTenant, CloudAccountID: manualTestAccount}

	for _, kind := range declarable {
		kind := kind
		t.Run(kind.SpecificType, func(t *testing.T) {
			e := entries[kind.SpecificType]
			row := sources.CloudResourceRow{
				ID:         "33333333-3333-3333-3333-333333333333",
				ResourceID: manualTestSub + "/resourceGroups/RG-Orders/providers/Microsoft.Test/things/orders",
				Name:       "orders", Type: e.RowType, ServiceName: e.ServiceName,
				Account: manualTestAccount, Tenant: manualTestTenant,
				CloudProvider: "Azure", Region: "East US", IsActive: true,
				Meta: []byte("{}"), Tags: []byte("{}"),
			}
			collected := src.createNodeFromResource(&row, req)
			if collected == nil {
				t.Fatalf("collector built no %s node", kind.SpecificType)
			}
			manual, err := b.Build(core.ManualNodeInput{
				TenantID: manualTestTenant, CloudAccountID: manualTestAccount, CloudProvider: "Azure",
				SpecificType: kind.SpecificType, Name: "orders", Region: "eastus", ResourceGroup: "rg-orders",
			})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if manual.UniqueKey != collected.UniqueKey || manual.ID != collected.ID {
				t.Errorf("identity drifted from the collector's\n manual:    %s (%s)\n collector: %s (%s)",
					manual.UniqueKey, manual.ID, collected.UniqueKey, collected.ID)
			}
		})
	}
}

// A resource inside another (a database on a server) is declared by its path
// below the top-level resource and still lands on the collector's node.
func TestManualNode_AzureChildResourceMatchesCollector(t *testing.T) {
	b, _ := core.ManualNodeBuilderFor("Azure")
	src, _ := NewAzureSource(AzureSourceConfig{}, nil)
	row := sources.CloudResourceRow{
		ID:         "33333333-3333-3333-3333-333333333333",
		ResourceID: manualTestSub + "/resourcegroups/rg-orders/providers/microsoft.sql/servers/sqlsrv1/databases/orders",
		Name:       "orders", Type: "databases", ServiceName: "microsoft.sql/servers",
		Account: manualTestAccount, Tenant: manualTestTenant,
		CloudProvider: "Azure", Region: "eastus", IsActive: true,
		Meta: []byte("{}"), Tags: []byte("{}"),
	}
	collected := src.createNodeFromResource(&row, &core.SourceBuildRequest{TenantID: manualTestTenant, CloudAccountID: manualTestAccount})
	manual, err := b.Build(core.ManualNodeInput{
		TenantID: manualTestTenant, CloudAccountID: manualTestAccount, CloudProvider: "Azure",
		SpecificType: collected.SpecificType, Name: "SqlSrv1/databases/orders", Region: "eastus", ResourceGroup: "RG-Orders",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if manual.UniqueKey != collected.UniqueKey {
		t.Errorf("declared %q, collector %q", manual.UniqueKey, collected.UniqueKey)
	}
}

func TestManualNode_AzureRequiresResourceGroup(t *testing.T) {
	b, _ := core.ManualNodeBuilderFor("Azure")
	kind := b.Kinds()[0]
	hasField := false
	for _, f := range kind.KeyFields {
		if f.Name == core.ManualNodeFieldResourceGroup && f.Required {
			hasField = true
		}
	}
	if !hasField {
		t.Errorf("%s does not ask for the resource group its key reads", kind.SpecificType)
	}
	if _, err := b.Build(core.ManualNodeInput{
		TenantID: manualTestTenant, CloudAccountID: manualTestAccount, CloudProvider: "Azure",
		SpecificType: kind.SpecificType, Name: "orders", Region: "eastus",
	}); err == nil {
		t.Error("declaring an Azure node without its resource group must fail")
	}
}
