package azure

import "nudgebee/services/knowledge_graph/core"

// azureKeyVaultSchema / azureKeyVaultKeySchema are the concrete per-specific_type
// property schemas for the AzureKeyVault and AzureKeyVaultKey specific_types. The
// Azure source runs no dedicated Key Vault extractor, so beyond the universal base
// keys only resource_group is populated. Registered to satisfy specific_type
// coverage.
var azureKeyVaultSchema = core.SpecificTypeSchema{
	SpecificType: "AzureKeyVault",
	NodeType:     core.NodeTypeSecretVault,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "tenant_id"},
		{Name: "sku_name"},
	},
}

var azureKeyVaultKeySchema = core.SpecificTypeSchema{
	SpecificType: "AzureKeyVaultKey",
	NodeType:     core.NodeTypeEncryptionKey,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "enabled"},
		{Name: "created_on"},
		{Name: "updated_on"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(azureKeyVaultSchema)
	core.RegisterSpecificTypeSchema(azureKeyVaultKeySchema)
}
