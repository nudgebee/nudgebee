package azure

import "nudgebee/services/knowledge_graph/core"

// azureFunctionAppSchema is the concrete per-specific_type property schema for
// the AzureFunctionApp specific_type. The Azure source runs no dedicated
// function-app extractor (runtime/memory unavailable), so beyond the universal
// base keys only resource_group is populated. Registered to satisfy specific_type
// coverage.
var azureFunctionAppSchema = core.SpecificTypeSchema{
	SpecificType: "AzureFunctionApp",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor). `state`
		// is a universal base key and therefore not repeated here.
		{Name: "kind"},
		{Name: "default_host_name"},
		{Name: "https_only"},
		{Name: "is_container"},
		{Name: "deployment_type"},
		{Name: "image_uri"},
		{Name: "image_digest"},
		{Name: "architecture_normalized"},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureFunctionAppSchema) }
