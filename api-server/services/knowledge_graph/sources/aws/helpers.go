package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// metaFromCache reads the raw `cloud_resourses.meta` JSON for a node from
// the per-build cache (s.metaByTypeAndID). createNodeFromResource
// intentionally drops raw meta to keep node payloads small (see comment
// near line 732), so edge builders that need it must read from the cache
// instead. Each caller commits to one cacheType, so a grep for the type
// string tells you exactly where it's used — no hidden fallback chain.
func (s *AWSSource) metaFromCache(node *core.DbNode, cacheType string) (map[string]interface{}, bool) {
	resourceID, _ := node.Properties["resource_id"].(string)
	if resourceID == "" {
		return nil, false
	}
	byID, ok := s.metaByTypeAndID[cacheType]
	if !ok {
		return nil, false
	}
	row, ok := byID[resourceID]
	if !ok || len(row.Meta) == 0 {
		return nil, false
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(row.Meta, &meta); err != nil {
		return nil, false
	}
	return meta, true
}

// createEdge is a helper to create an edge with standard fields
func (s *AWSSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"aws",
	)
}

// getStringProperty safely extracts a string property from a node
func getStringProperty(node *core.DbNode, key string) (string, bool) {
	if val, ok := node.Properties[key].(string); ok && val != "" {
		return val, true
	}
	return "", false
}

// getMetadataMap safely extracts the meta map from node properties
func getMetadataMap(node *core.DbNode) (map[string]interface{}, bool) {
	if meta, ok := node.Properties["meta"].(map[string]interface{}); ok {
		return meta, true
	}
	return nil, false
}

// unmarshalMetaInto unmarshals a sources.CloudResourceRow's Meta JSON into the given target struct.
func unmarshalMetaInto(row sources.CloudResourceRow, target interface{}) error {
	if len(row.Meta) == 0 {
		return fmt.Errorf("empty meta for resource %s", row.ResourceID)
	}
	return json.Unmarshal(row.Meta, target)
}

// getMapKeys returns the keys of a map for logging purposes
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// extractLabelValue extracts a string value from a labels map.
// The tags column stores values as string arrays (e.g. {"key": ["value"]}),
// but some callers may store plain strings. Both formats are handled.
func extractLabelValue(labels map[string]interface{}, key string) string {
	val, ok := labels[key]
	if !ok {
		return ""
	}
	// Plain string
	if s, ok := val.(string); ok {
		return s
	}
	// Array of strings (tags column format)
	if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}
