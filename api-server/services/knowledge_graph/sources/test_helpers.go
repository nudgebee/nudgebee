package sources

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// TestHelper functions for integration testing
// These expose internal methods for testing purposes only

// AWSSourceTestHelper provides test helper methods for AWS source
type AWSSourceTestHelper struct {
	source *AWSSource
}

// NewAWSSourceTestHelper creates a new AWS source test helper
func NewAWSSourceTestHelper(source *AWSSource) *AWSSourceTestHelper {
	return &AWSSourceTestHelper{source: source}
}

// ConvertResourcesToGraph exposes the internal convertResourcesToGraph method for testing
func (h *AWSSourceTestHelper) ConvertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}

// ConvertResourcesToGraphWithCache populates the AWS source's per-build meta
// cache (metaByType / metaByTypeAndID) from the rows — exactly as BuildGraph does
// before dispatching — then runs the converter. Edge builders that read the
// cache (IAM roles → RUNS_AS/ASSUMES, S3 notification → PUBLISHES_TO, ENI,
// elastic-ip, route-table, vpc-flow-log) are dead when the cache is empty, so
// the bare ConvertResourcesToGraph cannot exercise them; this helper can.
func (h *AWSSourceTestHelper) ConvertResourcesToGraphWithCache(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	h.source.metaByType = make(map[string][]CloudResourceRow)
	h.source.metaByTypeAndID = make(map[string]map[string]CloudResourceRow)
	for _, row := range resources {
		h.source.metaByType[row.Type] = append(h.source.metaByType[row.Type], row)
		if _, ok := h.source.metaByTypeAndID[row.Type]; !ok {
			h.source.metaByTypeAndID[row.Type] = make(map[string]CloudResourceRow)
		}
		h.source.metaByTypeAndID[row.Type][row.ResourceID] = row
	}
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}

// K8sSourceTestHelper provides test helper methods for K8s source
type K8sSourceTestHelper struct {
	source *K8sSource
}

// NewK8sSourceTestHelper creates a new K8s source test helper
func NewK8sSourceTestHelper(source *K8sSource) *K8sSourceTestHelper {
	return &K8sSourceTestHelper{source: source}
}

// ConvertK8sNodesToGraph exposes the internal convertK8sNodesToGraph method for testing
func (h *K8sSourceTestHelper) ConvertK8sNodesToGraph(k8sNodes []K8sNodeRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertK8sNodesToGraph(k8sNodes, req)
}

// ConvertWorkloadsToGraph exposes the internal convertWorkloadsToGraph method for testing
func (h *K8sSourceTestHelper) ConvertWorkloadsToGraph(workloads []K8sWorkloadRow, k8sNodeMap *map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode, map[string]*core.DbNode) {
	return h.source.convertWorkloadsToGraph(workloads, k8sNodeMap, map[string]string{}, req)
}

// GCPSourceTestHelper provides test helper methods for GCP source
type GCPSourceTestHelper struct {
	source *GCPSource
}

// NewGCPSourceTestHelper creates a new GCP source test helper
func NewGCPSourceTestHelper(source *GCPSource) *GCPSourceTestHelper {
	return &GCPSourceTestHelper{source: source}
}

// ConvertResourcesToGraph exposes the internal convertResourcesToGraph method for testing
func (h *GCPSourceTestHelper) ConvertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}

// AzureSourceTestHelper provides test helper methods for Azure source
type AzureSourceTestHelper struct {
	source *AzureSource
}

// NewAzureSourceTestHelper creates a new Azure source test helper
func NewAzureSourceTestHelper(source *AzureSource) *AzureSourceTestHelper {
	return &AzureSourceTestHelper{source: source}
}

// ConvertResourcesToGraph exposes the internal convertResourcesToGraph method for testing
func (h *AzureSourceTestHelper) ConvertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertResourcesToGraph(reqCtx, resources, req)
}
