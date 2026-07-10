package azure

import "nudgebee/services/knowledge_graph/core"

// azureManagedIdentitySchema is the concrete per-specific_type property schema for
// the AzureManagedIdentity specific_type. The ontology reads only universal base
// keys (name/region/subtype/arn — the Azure resource ID lives in the base `arn`
// property), and the Azure source runs no dedicated managed-identity extractor, so
// beyond the base keys only resource_group is populated. Registered to satisfy
// specific_type coverage.
var azureManagedIdentitySchema = core.SpecificTypeSchema{
	SpecificType: "AzureManagedIdentity",
	NodeType:     core.NodeTypeServiceIdentity,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureManagedIdentitySchema) }
