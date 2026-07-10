package k8s

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasSchema is the coverage guardrail for the K8s source: every
// concrete specific_type label declared in the k8sOntology map
// (core/ontology_data_k8s.go) must have a registered concrete-property
// schema (core.SpecificTypeSchema) co-located in this package. A newly-added
// K8s / Karpenter / Helm specific_type therefore cannot ship without also
// declaring its concrete schema. The list is hardcoded to match the
// k8sOntology keys exactly so drift in either direction fails loudly —
// including HelmRelease / ContainerImage, which have no emitter yet but are
// declared for registry parity.
func TestSpecificTypeHasSchema(t *testing.T) {
	labels := []string{
		"KubernetesDeployment",
		"KubernetesStatefulSet",
		"KubernetesDaemonSet",
		"KubernetesReplicaSet",
		"KubernetesJob",
		"KubernetesCronJob",
		"KubernetesPod",
		"KubernetesNode",
		"KubernetesService",
		"KubernetesNamespace",
		"KubernetesCluster",
		"KubernetesPersistentVolumeClaim",
		"KubernetesPersistentVolume",
		"KubernetesConfigMap",
		"KubernetesSecret",
		"KubernetesServiceAccount",
		"KubernetesIngress",
		"KarpenterNodePool",
		"KarpenterNodeClaim",
		"HelmChart",
		"HelmRelease",
		"GitRepository",
		"ContainerImage",
	}
	for _, label := range labels {
		if _, ok := core.LookupSpecificTypeSchema(label); !ok {
			t.Errorf("K8s specific_type %q has no concrete schema — declare a core.SpecificTypeSchema and RegisterSpecificTypeSchema in its sources/k8s/*.go file", label)
		}
	}
}
