package aws

import "nudgebee/services/knowledge_graph/core"

// ecrRepositorySchema / ecrPublicRepositorySchema — concrete schemas for ECR
// repositories. Fields written by
// extractContainerRegistryMetadata; repository_uri drives the ontology.
var ecrRepositorySchema = core.SpecificTypeSchema{
	SpecificType: "ECRRepository",
	NodeType:     core.NodeTypeContainerRegistry,
	Properties: []core.PropertyDef{
		{Name: "repository_uri", Indexed: true},
		{Name: "repository_name"},
		{Name: "registry_id"},
		{Name: "image_tag_mutability"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "created_at"},
	},
}

var ecrPublicRepositorySchema = core.SpecificTypeSchema{
	SpecificType: "ECRPublicRepository",
	NodeType:     core.NodeTypeContainerRegistry,
	Properties: []core.PropertyDef{
		{Name: "repository_uri", Indexed: true},
		{Name: "repository_name"},
		{Name: "registry_id"},
		{Name: "image_tag_mutability"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "created_at"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(ecrRepositorySchema)
	core.RegisterSpecificTypeSchema(ecrPublicRepositorySchema)
}

// extractContainerRegistryMetadata extracts essential fields for ECR repositories
func (s *AWSSource) extractContainerRegistryMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Repository name (critical identifier)
	if repoName, ok := metaMap["RepositoryName"].(string); ok && repoName != "" {
		properties["repository_name"] = repoName
	}

	// Repository URI (needed for ECR->Workload relationship matching)
	if repoURI, ok := metaMap["RepositoryUri"].(string); ok && repoURI != "" {
		properties["repository_uri"] = repoURI
	}

	// Registry ID (AWS account)
	if registryId, ok := metaMap["RegistryId"].(string); ok && registryId != "" {
		properties["registry_id"] = registryId
	}

	// Image tag mutability
	if mutability, ok := metaMap["ImageTagMutability"].(string); ok && mutability != "" {
		properties["image_tag_mutability"] = mutability
	}
}
