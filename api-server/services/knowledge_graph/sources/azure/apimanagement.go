package azure

import "nudgebee/services/knowledge_graph/core"

// azureAPIManagementSchema is the concrete per-specific_type property schema for
// the AzureAPIManagement specific_type. The Azure source runs no dedicated API
// Management extractor (protocol/endpoint not extracted), so beyond the universal
// base keys only resource_group is populated. Registered to satisfy specific_type
// coverage.
var azureAPIManagementSchema = core.SpecificTypeSchema{
	SpecificType: "AzureAPIManagement",
	NodeType:     core.NodeTypeAPIGateway,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureAPIManagementSchema) }
