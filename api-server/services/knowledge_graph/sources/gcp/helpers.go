package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// createEdge creates a knowledge graph edge for GCP resources
func (s *GCPSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"gcp",
	)
}

// extractGCPShortName extracts the short name from a GCP resource name
// GCP names follow the format: {project}/{path}/{resource-name}
// Returns the last segment after the final /
func extractGCPShortName(name string) string {
	if name == "" {
		return ""
	}

	parts := strings.Split(name, "/")
	lastPart := parts[len(parts)-1]
	if lastPart != "" {
		return lastPart
	}

	// If the last part is empty (trailing slash), try the second to last
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}

	return name
}

// extractGCPProjectID extracts the GCP project ID from a resource name
// GCP resource names typically start with {project-id}/...
func extractGCPProjectID(name string) string {
	if name == "" {
		return ""
	}

	parts := strings.Split(name, "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}

	return ""
}

// extractGCPResourceNameFromURL extracts the resource name from a GCP self-link URL
// e.g., "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default" → "default"
// e.g., "projects/my-project/regions/us-central1/subnetworks/default" → "default"
func extractGCPResourceNameFromURL(url string) string {
	if url == "" {
		return ""
	}

	parts := strings.Split(url, "/")
	lastPart := parts[len(parts)-1]
	if lastPart != "" {
		return lastPart
	}

	// If the last part is empty (trailing slash), try the second to last
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}

	return url
}

// findNodeByNameAndType finds a node by its short name and type in the lookup
func findNodeByNameAndType(lookup *sources.NodeLookup, nodeType core.NodeType, name string) *core.DbNode {
	nodes, ok := lookup.ByNodeType[nodeType]
	if !ok {
		return nil
	}

	for _, node := range nodes {
		nodeName := getNodeName(node)
		shortName := extractGCPShortName(nodeName)

		if shortName == name || nodeName == name {
			return node
		}

		// Also check resource_id and vpc_id properties
		if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID == name {
			return node
		}
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID == name {
			return node
		}
	}

	return nil
}

// getNodeName safely gets the name property from a node
func getNodeName(node *core.DbNode) string {
	if name, ok := node.Properties["name"].(string); ok {
		return name
	}
	return ""
}

// normalizeGCPLabels normalizes GCP labels from Asset Inventory format (array values) to simple strings
// GCP Asset Inventory stores labels as: {"key": ["value"]} but we want: {"key": "value"}
func normalizeGCPLabels(labels map[string]interface{}) map[string]interface{} {
	normalized := make(map[string]interface{}, len(labels))
	for key, value := range labels {
		normalized[key] = extractGCPLabelValueFromInterface(value)
	}
	return normalized
}

// extractGCPLabelValueFromInterface extracts a string value from a label that may be stored as string or array
func extractGCPLabelValueFromInterface(value interface{}) string {
	// Handle string value directly
	if strVal, ok := value.(string); ok {
		return strVal
	}

	// Handle array value (GCP Asset Inventory stores labels as arrays)
	if arrVal, ok := value.([]interface{}); ok && len(arrVal) > 0 {
		if strVal, ok := arrVal[0].(string); ok {
			return strVal
		}
	}

	// Handle []string type (in case JSON unmarshals to this)
	if arrVal, ok := value.([]string); ok && len(arrVal) > 0 {
		return arrVal[0]
	}

	return ""
}

// extractGCPLabelValue extracts a label value from GCP labels map
// GCP labels can be stored as either:
// - string: "value"
// - array: ["value"] (GCP Asset Inventory format)
func extractGCPLabelValue(labels map[string]interface{}, key string) string {
	value, exists := labels[key]
	if !exists {
		return ""
	}
	return extractGCPLabelValueFromInterface(value)
}
