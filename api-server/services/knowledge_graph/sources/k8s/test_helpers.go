package k8s

import (
	"nudgebee/services/knowledge_graph/core"
)

// K8sSourceTestHelper exposes the internal K8s converters for the e2e test package.
type K8sSourceTestHelper struct {
	source *K8sSource
}

// NewK8sSourceTestHelper creates a new K8s source test helper.
func NewK8sSourceTestHelper(source *K8sSource) *K8sSourceTestHelper {
	return &K8sSourceTestHelper{source: source}
}

// ConvertK8sNodesToGraph exposes the internal convertK8sNodesToGraph method for testing.
func (h *K8sSourceTestHelper) ConvertK8sNodesToGraph(k8sNodes []K8sNodeRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return h.source.convertK8sNodesToGraph(k8sNodes, req)
}

// ConvertWorkloadsToGraph exposes the internal convertWorkloadsToGraph method for testing.
func (h *K8sSourceTestHelper) ConvertWorkloadsToGraph(workloads []K8sWorkloadRow, k8sNodeMap *map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode, map[string]*core.DbNode) {
	return h.source.convertWorkloadsToGraph(workloads, k8sNodeMap, map[string]string{}, req)
}
