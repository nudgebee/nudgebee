package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// GCPSourceTestHelper exposes the internal GCP converter for the e2e test package.
type GCPSourceTestHelper struct {
	source *GCPSource
}

// NewGCPSourceTestHelper creates a new GCP source test helper.
func NewGCPSourceTestHelper(source *GCPSource) *GCPSourceTestHelper {
	return &GCPSourceTestHelper{source: source}
}

// ConvertResourcesToGraph exposes the internal convertResourcesToGraph method for testing.
func (h *GCPSourceTestHelper) ConvertResourcesToGraph(reqCtx *security.RequestContext, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}
