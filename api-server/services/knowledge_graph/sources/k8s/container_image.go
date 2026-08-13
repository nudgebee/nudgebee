package k8s

import (
	"nudgebee/services/knowledge_graph/core"
)

// containerImageSchema — concrete schema for the ContainerImage node,
// synthesized by createContainerImageNodesAndEdges from the container image refs
// already extracted onto workload/pod nodes. Names are the keys written below.
var containerImageSchema = core.SpecificTypeSchema{
	SpecificType: "ContainerImage",
	NodeType:     core.NodeTypeContainerImage,
	Properties: []core.PropertyDef{
		{Name: "registry", Indexed: true},
		{Name: "tag", Indexed: true},
		{Name: "digest", Indexed: true},
		{Name: "image_ref"},
	},
}

func init() { core.RegisterSpecificTypeSchema(containerImageSchema) }

// collectImageRefs returns the container image references carried by a workload/pod
// node — the container_images list, falling back to primary_image. Handles both the
// in-memory []string form and the []interface{} form (post-JSON round-trip).
func collectImageRefs(node *core.DbNode) []string {
	var refs []string
	switch v := node.Properties["container_images"].(type) {
	case []string:
		refs = append(refs, v...)
	case []interface{}:
		for _, x := range v {
			if str, ok := x.(string); ok {
				refs = append(refs, str)
			}
		}
	}
	if len(refs) == 0 {
		if pi, ok := node.Properties["primary_image"].(string); ok && pi != "" {
			refs = append(refs, pi)
		}
	}
	return refs
}

// createContainerImageNodesAndEdges synthesizes deduplicated ContainerImage nodes +
// Workload/Pod → ContainerImage (USES_IMAGE) edges from the image refs on those nodes,
// via the shared core helper.
func (s *K8sSource) createContainerImageNodesAndEdges(imageUsers []*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return core.SynthesizeContainerImages(imageUsers, collectImageRefs, core.CloudProviderK8s, req.TenantID, req.CloudAccountID, "k8s")
}
