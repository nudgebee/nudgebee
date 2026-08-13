package azure

import "nudgebee/services/knowledge_graph/core"

// createEdge is a helper to create an edge with Azure source
func (s *AzureSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"azure",
	)
}
