package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// elastiCacheClusterSchema — concrete schema for ElastiCache clusters.
// The ontology coalesces the endpoint from
// configuration_endpoint → primary_endpoint → reader_endpoint, so all three are declared.
var elastiCacheClusterSchema = core.SpecificTypeSchema{
	SpecificType: "ElastiCacheCluster",
	NodeType:     core.NodeTypeCache,
	Properties: []core.PropertyDef{
		{Name: "engine", Indexed: true},
		{Name: "engine_version", Indexed: true},
		{Name: "instance_type", Indexed: true},
		{Name: "num_cache_nodes", Indexed: true},
		{Name: "availability_zone", Indexed: true},
		{Name: "configuration_endpoint"},
		{Name: "primary_endpoint"},
		{Name: "reader_endpoint"},
		{Name: "node_group_endpoints"},
		{Name: "cache_node_endpoints"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "cache_cluster_id"},
		{Name: "cache_node_type"},
		{Name: "cache_cluster_status"},
		{Name: "preferred_availability_zone"},
		{Name: "preferred_maintenance_window"},
		{Name: "cache_cluster_create_time"},
		{Name: "cache_subnet_group_name"},
		{Name: "auto_minor_version_upgrade"},
		{Name: "replication_group_id"},
		{Name: "snapshot_retention_limit"},
		{Name: "snapshot_window"},
		{Name: "auth_token_enabled"},
		{Name: "transit_encryption_enabled"},
		{Name: "at_rest_encryption_enabled"},
		{Name: "topic_arn"},
	},
}

func init() { core.RegisterSpecificTypeSchema(elastiCacheClusterSchema) }

// extractCacheMetadata extracts essential fields for cache nodes (ElastiCache)
func (s *AWSSource) extractCacheMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoints (critical for connections). AWS sometimes returns these as a
	// bare string and sometimes as {"Address": "...", "Port": ...} — accept both.
	if addr := sources.ExtractEndpointAddress(metaMap["ConfigurationEndpoint"]); addr != "" {
		properties["configuration_endpoint"] = addr
	}
	if addr := sources.ExtractEndpointAddress(metaMap["PrimaryEndpoint"]); addr != "" {
		properties["primary_endpoint"] = addr
	}
	if addr := sources.ExtractEndpointAddress(metaMap["ReaderEndpoint"]); addr != "" {
		properties["reader_endpoint"] = addr
	}

	// Cluster-mode (replication group) per-shard endpoints. Flatten so the
	// cloud-enrichment endpoint index can match by string membership.
	if nodeGroups, ok := metaMap["NodeGroups"].([]interface{}); ok {
		var ngEndpoints []string
		for _, ng := range nodeGroups {
			ngMap, ok := ng.(map[string]interface{})
			if !ok {
				continue
			}
			if addr := sources.ExtractEndpointAddress(ngMap["PrimaryEndpoint"]); addr != "" {
				ngEndpoints = append(ngEndpoints, addr)
			}
			if addr := sources.ExtractEndpointAddress(ngMap["ReaderEndpoint"]); addr != "" {
				ngEndpoints = append(ngEndpoints, addr)
			}
		}
		if len(ngEndpoints) > 0 {
			properties["node_group_endpoints"] = ngEndpoints
		}
	}

	// Memcached / non-cluster-mode per-node endpoints.
	if cacheNodes, ok := metaMap["CacheNodes"].([]interface{}); ok {
		var cnEndpoints []string
		for _, cn := range cacheNodes {
			cnMap, ok := cn.(map[string]interface{})
			if !ok {
				continue
			}
			if addr := sources.ExtractEndpointAddress(cnMap["Endpoint"]); addr != "" {
				cnEndpoints = append(cnEndpoints, addr)
			}
		}
		if len(cnEndpoints) > 0 {
			properties["cache_node_endpoints"] = cnEndpoints
		}
	}

	// Engine info
	if engine, ok := metaMap["Engine"].(string); ok && engine != "" {
		properties["engine"] = engine
	}
	if engineVersion, ok := metaMap["EngineVersion"].(string); ok && engineVersion != "" {
		properties["engine_version"] = engineVersion
	}

	// Instance type
	if nodeType, ok := metaMap["CacheNodeType"].(string); ok && nodeType != "" {
		properties["instance_type"] = nodeType
	}

	// Number of nodes (important for capacity)
	if numNodes, ok := metaMap["NumCacheNodes"].(float64); ok {
		properties["num_cache_nodes"] = int(numNodes)
	}

	// Preferred Availability Zone (for single-AZ clusters, may be "Multiple" for multi-AZ)
	if preferredAZ, ok := metaMap["PreferredAvailabilityZone"].(string); ok && preferredAZ != "" {
		properties["availability_zone"] = preferredAZ
	}
}

// createElastiCacheEdges creates edges for ElastiCache clusters
func (s *AWSSource) createElastiCacheEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// TODO: Implement ElastiCache edge creation logic based on metadata
		// Will be implemented when metadata is provided
		_ = node
	}

	return edges
}
