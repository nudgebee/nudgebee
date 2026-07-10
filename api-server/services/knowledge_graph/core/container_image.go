package core

import "strings"

// Container-image synthesis shared across sources (k8s workloads, AWS Lambda, …).
// A ContainerImage node is a first-class NodeType; any source that has image
// references on its nodes can turn them into deduplicated ContainerImage nodes +
// USES_IMAGE edges via SynthesizeContainerImages. The ContainerImage schema is
// registered once (sources/k8s/container_image.go); every source emits the same
// specific_type "ContainerImage".

// ParseImageRef splits a Docker image reference into its parts:
//
//	[registry[:port]/]repository[:tag][@digest]
//
// A leading path segment is treated as the registry only if it looks like a host
// (contains "." or ":" or is "localhost"); otherwise the registry is implicit Docker
// Hub ("docker.io"). Tag defaults to "latest" when neither a tag nor a digest is present.
func ParseImageRef(ref string) (registry, repository, tag, digest string) {
	s := ref

	if i := strings.Index(s, "@"); i >= 0 {
		digest = s[i+1:]
		s = s[:i]
	}

	name := s
	if i := strings.Index(s, "/"); i >= 0 {
		first := s[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry = first
			name = s[i+1:]
		}
	}

	repository = name
	if i := strings.LastIndex(name, ":"); i >= 0 {
		repository = name[:i]
		tag = name[i+1:]
	}

	if registry == "" {
		registry = "docker.io"
	}
	if tag == "" && digest == "" {
		tag = "latest"
	}
	return registry, repository, tag, digest
}

// SynthesizeContainerImages builds one deduplicated ContainerImage node per distinct
// image reference used by the given nodes, plus a USES_IMAGE edge from each user to the
// image(s) it runs. imageRefsOf returns a node's image references (source-specific:
// k8s reads container_images/primary_image, Lambda reads image_uri). Dedup is within
// the build so a shared image is a single node — enabling "which workloads/functions
// run image X" (vuln / provenance blast-radius) queries.
func SynthesizeContainerImages(users []*DbNode, imageRefsOf func(*DbNode) []string, cloudProvider, tenantID, cloudAccountID, source string) ([]*DbNode, []*DbEdge) {
	imageNodes := make(map[string]*DbNode)
	nodes := make([]*DbNode, 0)
	edges := make([]*DbEdge, 0)

	for _, user := range users {
		emitted := make(map[string]bool)
		for _, ref := range imageRefsOf(user) {
			if ref == "" {
				continue
			}
			registry, repository, tag, digest := ParseImageRef(ref)

			identity := tag
			if digest != "" {
				if identity != "" {
					identity += "@" + digest
				} else {
					identity = digest
				}
			}
			if identity == "" {
				identity = "latest"
			}
			dedupKey := registry + "/" + repository + ":" + identity

			node, ok := imageNodes[dedupKey]
			if !ok {
				props := map[string]interface{}{
					"name":          repository,
					"registry":      registry,
					"tag":           tag,
					"digest":        digest,
					"image_ref":     ref,
					"specific_type": "ContainerImage",
				}
				uniqueKey := BuildUniqueKey(cloudProvider, cloudAccountID, registry, NodeTypeContainerImage, repository, identity)
				node = NewNode(NodeTypeContainerImage, uniqueKey, props, tenantID, cloudAccountID, source)
				imageNodes[dedupKey] = node
				nodes = append(nodes, node)
			}

			if !emitted[node.ID] {
				emitted[node.ID] = true
				edges = append(edges, NewEdge(user.ID, node.ID, RelationshipUsesImage,
					map[string]interface{}{}, tenantID, cloudAccountID, source))
			}
		}
	}

	return nodes, edges
}
