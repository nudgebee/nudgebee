package azure

import "nudgebee/services/knowledge_graph/core"

// azureStorageAccountSchema is the concrete per-specific_type property schema for
// the AzureStorageAccount specific_type. The Azure source runs no dedicated storage
// extractor, so beyond the universal base keys (name/region/subtype/...) only
// resource_group is populated (by extractEssentialMetadataByNodeType). Registered
// to satisfy specific_type coverage; encryption/versioning/size are not extracted
// (declare-but-don't-fake).
var azureStorageAccountSchema = core.SpecificTypeSchema{
	SpecificType: "AzureStorageAccount",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "kind"},
		{Name: "creation_time"},
		{Name: "is_hns_enabled"},
		{Name: "primary_location"},
		{Name: "secondary_location"},
		{Name: "provisioning_state"},
		{Name: "status_of_primary"},
		{Name: "status_of_secondary"},
		{Name: "enable_https_traffic_only"},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureStorageAccountSchema) }
