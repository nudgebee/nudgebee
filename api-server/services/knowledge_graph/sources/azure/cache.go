package azure

import "nudgebee/services/knowledge_graph/core"

// azureCacheForRedisSchema is the fixed concrete-property schema for the
// AzureCacheForRedis specific_type. Names are the node.Properties keys written by
// extractCacheMetadata + extractCommonNetworkIDs; universal base keys are implicit.
var azureCacheForRedisSchema = core.SpecificTypeSchema{
	SpecificType: "AzureCacheForRedis",
	NodeType:     core.NodeTypeCache,
	Properties: []core.PropertyDef{
		{Name: "cache_type", Indexed: true}, // "redis"
		{Name: "dns_name", Indexed: true},   // ~ endpoint (hostName)
		{Name: "port", Indexed: true},       // sslPort
		{Name: "resource_group", Indexed: true},
		{Name: "vnet_id"},
		{Name: "subnet_id"},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureCacheForRedisSchema) }

func (s *AzureSource) extractCacheMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if hostName, ok := metaMap["hostName"].(string); ok && hostName != "" {
		properties["dns_name"] = hostName
	}
	if port, ok := metaMap["sslPort"]; ok {
		properties["port"] = port
	}
	properties["cache_type"] = "redis"
}
