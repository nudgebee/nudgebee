package azure

import "nudgebee/services/knowledge_graph/core"

// azureContainerInstanceSchema is the concrete per-specific_type property schema
// for the AzureContainerInstance specific_type (microsoft.containerinstance/containergroups,
// mapped to core.NodeTypeWorkload). The Azure source runs no dedicated container-group
// extractor, so beyond the universal base keys only resource_group is populated.
// Registered to satisfy specific_type coverage.
var azureContainerInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "AzureContainerInstance",
	NodeType:     core.NodeTypeWorkload,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureContainerInstanceSchema) }
