package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// memorystoreInstanceSchema is the concrete per-specific_type property schema
// for the MemorystoreInstance specific_type. Memorystore instances are ingested
// via the shared classify.go path and carry only universal base keys plus the raw
// `meta` (the Redis host used for cache-endpoint matching lives in meta.host, read
// by indexCacheNodesByHost, not a hoisted property), so no resource-specific field
// is declared.
var memorystoreInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "MemorystoreInstance",
	NodeType:     core.NodeTypeCache,
}

func init() { core.RegisterSpecificTypeSchema(memorystoreInstanceSchema) }

// createCacheEndpointEdges links each Cloud Run service to the Memorystore Cache
// instance it connects to, matched by the Redis endpoint host captured in the
// service's env (meta.redis_endpoints) against the Cache node's meta.host. This
// recovers app→cache dependencies for services that expose the endpoint as a
// plain env value; services whose endpoint lives in a secret are not connected.
func (s *GCPSource) createCacheEndpointEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	srvNodes, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return edges
	}
	cacheByHost := indexCacheNodesByHost(lookup.ByNodeType[core.NodeTypeCache])
	if len(cacheByHost) == 0 {
		return edges
	}

	seen := make(map[string]bool)
	for _, node := range srvNodes {
		for _, host := range nodeRedisEndpoints(node) {
			cn := cacheByHost[host]
			if cn == nil {
				continue
			}
			dk := node.ID + "|" + cn.ID
			if seen[dk] {
				continue
			}
			seen[dk] = true
			edges = append(edges, s.createEdge(node, cn, core.RelationshipCalls,
				map[string]interface{}{"evidence": "redis_env_endpoint", "endpoint": host}, req))
		}
	}
	s.logger.Info("created GCP cache endpoint edges", "edge_count", len(edges))
	return edges
}

// indexCacheNodesByHost maps each Cache node's Memorystore host (meta.host) to
// the node, for endpoint matching.
func indexCacheNodesByHost(cacheNodes []*core.DbNode) map[string]*core.DbNode {
	byHost := make(map[string]*core.DbNode)
	for _, cn := range cacheNodes {
		meta, _ := cn.Properties["meta"].(map[string]interface{})
		if meta == nil {
			continue
		}
		if host, _ := meta["host"].(string); host != "" {
			byHost[host] = cn
		}
	}
	return byHost
}

// nodeRedisEndpoints returns the non-empty Redis endpoint hosts recorded on a
// ServerlessFunction node's meta.redis_endpoints.
func nodeRedisEndpoints(node *core.DbNode) []string {
	meta, _ := node.Properties["meta"].(map[string]interface{})
	if meta == nil {
		return nil
	}
	raw, _ := meta["redis_endpoints"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, ep := range raw {
		if host, _ := ep.(string); host != "" {
			out = append(out, host)
		}
	}
	return out
}
