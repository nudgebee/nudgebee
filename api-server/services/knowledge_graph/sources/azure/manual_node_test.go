package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// TestManualNode_AzureKeyMatchesCollector: every declarable Azure type gets the
// node id the collector writes for the same resource. See the AWS test.
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
	const tenant, account = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	req := &core.SourceBuildRequest{TenantID: tenant, CloudAccountID: account}

	for _, kind := range declarable {
		kind := kind
		t.Run(kind.SpecificType, func(t *testing.T) {
			e := entries[kind.SpecificType]
			row := sources.CloudResourceRow{
				ID: "33333333-3333-3333-3333-333333333333", ResourceID: "orders", Name: "orders",
				Type: e.RowType, ServiceName: e.ServiceName, Account: account, Tenant: tenant,
				CloudProvider: "Azure", Region: "eastus", IsActive: true,
				Meta: []byte("{}"), Tags: []byte("{}"),
			}
			collected := src.createNodeFromResource(&row, req)
			if collected == nil {
				t.Fatalf("collector built no %s node", kind.SpecificType)
			}
			manual, err := b.Build(core.ManualNodeInput{
				TenantID: tenant, CloudAccountID: account, CloudProvider: "Azure",
				SpecificType: kind.SpecificType, Name: "orders", Region: "eastus",
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
