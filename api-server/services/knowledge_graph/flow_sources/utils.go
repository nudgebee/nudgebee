package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"time"
)

// TimeNow returns the current time
// This is a helper function that can be mocked for testing
func TimeNow() time.Time {
	return time.Now()
}

// DeduplicateEdges removes duplicate edges based on composite key
// This is useful when multiple flow sources might create the same edge
func DeduplicateEdges(edges []*core.DbEdge) []*core.DbEdge {
	edgeMap := make(map[string]*core.DbEdge)

	for _, edge := range edges {
		// Create composite key for deduplication
		compositeKey := edge.SourceNodeID + ":" +
			edge.DestinationNodeID + ":" +
			string(edge.RelationshipType) + ":" +
			edge.CloudAccountID + ":" +
			edge.TenantID

		if existing, exists := edgeMap[compositeKey]; exists {
			// Merge properties if the edge already exists
			for k, v := range edge.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			edgeMap[compositeKey] = edge
		}
	}

	// Convert map back to slice
	deduplicated := make([]*core.DbEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		deduplicated = append(deduplicated, edge)
	}

	return deduplicated
}

// MergeEdgeProperties merges properties from multiple edges
func MergeEdgeProperties(existing map[string]interface{}, new map[string]interface{}) map[string]interface{} {
	if existing == nil {
		existing = make(map[string]interface{})
	}

	for k, v := range new {
		// For numeric values, you might want to aggregate (sum, avg, etc.)
		// For now, just overwrite
		existing[k] = v
	}

	return existing
}

// FilterNodesByType filters nodes by node type
func FilterNodesByType(nodes []*core.DbNode, nodeType core.NodeType) []*core.DbNode {
	filtered := make([]*core.DbNode, 0)
	for _, node := range nodes {
		if node.NodeType == nodeType {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// FilterNodesBySource filters nodes by source
func FilterNodesBySource(nodes []*core.DbNode, source string) []*core.DbNode {
	filtered := make([]*core.DbNode, 0)
	for _, node := range nodes {
		if node.Source == source {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// GroupNodesByType groups nodes by their node type
func GroupNodesByType(nodes []*core.DbNode) map[core.NodeType][]*core.DbNode {
	grouped := make(map[core.NodeType][]*core.DbNode)
	for _, node := range nodes {
		grouped[node.NodeType] = append(grouped[node.NodeType], node)
	}
	return grouped
}

// MergeEdgePropertiesWithSourcePrefix merges properties from a secondary edge into the primary edge.
// Metrics from the secondary edge are prefixed with its source name to preserve data from both sources.
// Also tracks all contributing sources in a "contributing_sources" property.
func MergeEdgePropertiesWithSourcePrefix(primary, secondary *core.DbEdge) {
	if primary.Properties == nil {
		primary.Properties = make(map[string]interface{})
	}

	sourcePrefix := secondary.Source + "_"

	// Merge metrics with source prefix
	for _, metric := range MetricsToMerge {
		if val, ok := secondary.Properties[metric]; ok {
			primary.Properties[sourcePrefix+metric] = val
		}
	}

	// Track contributing sources
	sources := getContributingSources(primary)
	if !containsString(sources, secondary.Source) {
		sources = append(sources, secondary.Source)
	}
	primary.Properties["contributing_sources"] = sources

	primary.UpdatedAt = time.Now()
}

// getContributingSources extracts the contributing_sources slice from edge properties
func getContributingSources(edge *core.DbEdge) []string {
	if edge.Properties == nil {
		return []string{edge.Source}
	}

	if sources, ok := edge.Properties["contributing_sources"].([]string); ok {
		return sources
	}

	// Try to handle []interface{} case (from JSON unmarshaling)
	if sourcesInterface, ok := edge.Properties["contributing_sources"].([]interface{}); ok {
		sources := make([]string, 0, len(sourcesInterface))
		for _, s := range sourcesInterface {
			if str, ok := s.(string); ok {
				sources = append(sources, str)
			}
		}
		if len(sources) > 0 {
			return sources
		}
	}

	return []string{edge.Source}
}

// containsString checks if a string slice contains a specific string
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// DeduplicateEdgesWithPriority deduplicates edges using source priority.
// When multiple sources create the same edge (same source node, dest node, and edge type),
// the source with the highest priority becomes the primary source.
// Properties from lower priority sources are merged with source prefix (e.g., traces_latency_ms).
//
// This ensures:
// 1. Only one edge exists per (source_node, dest_node, edge_type, account, tenant) tuple
// 2. The primary source is determined by priority (eBPF > Traces > Datadog for CALLS)
// 3. Metrics from all sources are preserved with source prefixes
// 4. All contributing sources are tracked in the "contributing_sources" property
func DeduplicateEdgesWithPriority(edges []*core.DbEdge) []*core.DbEdge {
	edgeMap := make(map[string]*core.DbEdge)

	for _, edge := range edges {
		// Create composite key for deduplication
		compositeKey := buildEdgeCompositeKey(edge)

		if existing, exists := edgeMap[compositeKey]; exists {
			existingPriority := GetEdgeSourcePriority(existing.Source, existing.RelationshipType)
			newPriority := GetEdgeSourcePriority(edge.Source, edge.RelationshipType)

			if IsHigherPriority(newPriority, existingPriority) {
				// New edge has higher priority - it becomes primary
				// First, copy contributing sources from existing to new
				MergeEdgePropertiesWithSourcePrefix(edge, existing)
				edgeMap[compositeKey] = edge
			} else {
				// Existing edge has higher or equal priority - merge new into existing
				MergeEdgePropertiesWithSourcePrefix(existing, edge)
			}
		} else {
			// Initialize contributing_sources for new edges
			if edge.Properties == nil {
				edge.Properties = make(map[string]interface{})
			}
			if _, ok := edge.Properties["contributing_sources"]; !ok {
				edge.Properties["contributing_sources"] = []string{edge.Source}
			}
			edgeMap[compositeKey] = edge
		}
	}

	// Convert map back to slice
	result := make([]*core.DbEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		result = append(result, edge)
	}

	return result
}

// buildEdgeCompositeKey builds the composite key for edge deduplication
func buildEdgeCompositeKey(edge *core.DbEdge) string {
	return edge.SourceNodeID + ":" +
		edge.DestinationNodeID + ":" +
		string(edge.RelationshipType) + ":" +
		edge.CloudAccountID + ":" +
		edge.TenantID
}
