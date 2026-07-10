package azure

import "nudgebee/services/knowledge_graph/core"

// azureMonitorSchema / azureLogAnalyticsSchema are the concrete per-specific_type
// property schemas for the AzureMonitor and AzureLogAnalytics
// specific_types. Both observability service_names are filtered out before ingest
// and the Azure source runs no dedicated extractor, so beyond the universal base
// keys only resource_group is populated. Registered to satisfy specific_type
// coverage; retention_days is not extracted (declare-but-don't-fake).
var azureMonitorSchema = core.SpecificTypeSchema{
	SpecificType: "AzureMonitor",
	NodeType:     core.NodeTypeMonitoringService,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "description"},
		{Name: "severity"},
		{Name: "enabled"},
		{Name: "window_size"},
		{Name: "evaluation_frequency"},
		{Name: "last_updated_time"},
	},
}

var azureLogAnalyticsSchema = core.SpecificTypeSchema{
	SpecificType: "AzureLogAnalytics",
	NodeType:     core.NodeTypeLogAggregator,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(azureMonitorSchema)
	core.RegisterSpecificTypeSchema(azureLogAnalyticsSchema)
}
