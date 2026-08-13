package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// AzureSourceTestHelper exposes the internal Azure converter for the e2e test package.
type AzureSourceTestHelper struct {
	source *AzureSource
}

// NewAzureSourceTestHelper creates a new Azure source test helper.
func NewAzureSourceTestHelper(source *AzureSource) *AzureSourceTestHelper {
	return &AzureSourceTestHelper{source: source}
}

// ConvertResourcesToGraph exposes the internal convertResourcesToGraph method for testing.
func (h *AzureSourceTestHelper) ConvertResourcesToGraph(reqCtx *security.RequestContext, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}
