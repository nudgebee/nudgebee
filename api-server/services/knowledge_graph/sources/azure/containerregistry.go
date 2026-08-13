package azure

import "nudgebee/services/knowledge_graph/core"

// azureContainerRegistrySchema is the concrete per-specific_type property schema
// for the AzureContainerRegistry specific_type. The Azure source runs no dedicated
// ACR extractor (loginServer/uri not extracted), so beyond the universal base keys
// only resource_group is populated. Registered to satisfy specific_type coverage.
var azureContainerRegistrySchema = core.SpecificTypeSchema{
	SpecificType: "AzureContainerRegistry",
	NodeType:     core.NodeTypeContainerRegistry,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureContainerRegistrySchema) }
