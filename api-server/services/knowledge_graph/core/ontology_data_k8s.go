package core

// Kubernetes ontology field mappings — how each concrete K8s / Karpenter / Helm
// specific_type populates the cross-cloud-normalized ontology_attributes for its
// NodeType. Mirrors ontology_data_aws.go. The canonical field vocabulary is
// fixed per NodeType in ontology_vocab.go and enforced by
// TestOntologyFieldsAreCanonical (every OntologyField below is ⊆ its NodeType's
// canonicalOntologyVocab entry).
//
// NodeField keys are the REAL property keys emitted by the k8s source modules
// (sources/k8s/*.go); the map is keyed by the concrete specific_type those
// modules stamp via properties["specific_type"] (workload.go = "Kubernetes"+Kind,
// node.go, service.go, cluster_namespace.go, pvc.go, karpenter.go, ...).

func init() { registerOntology(k8sOntology) }

// k8sWorkloadFields is the shared field shape for every workload Kind that rolls
// up to NodeTypeWorkload (Deployment / StatefulSet / DaemonSet / ReplicaSet).
// "type" ← subtype (= the K8s Kind), "replicas" ← the spec replica count.
var k8sWorkloadFields = []OntologyField{
	{OntologyField: "name", NodeField: "name", Required: true},
	{OntologyField: "namespace", NodeField: "namespace"},
	{OntologyField: "cluster", NodeField: "cluster"},
	{OntologyField: "type", NodeField: "subtype"},
	{OntologyField: "replicas", NodeField: "replicas"},
}

var k8sOntology = map[string]OntologyNodeSpec{
	// --- Workloads (Deployment/StatefulSet/DaemonSet/ReplicaSet → Workload) ---
	"KubernetesDeployment":  {NodeType: NodeTypeWorkload, Fields: k8sWorkloadFields},
	"KubernetesStatefulSet": {NodeType: NodeTypeWorkload, Fields: k8sWorkloadFields},
	"KubernetesDaemonSet":   {NodeType: NodeTypeWorkload, Fields: k8sWorkloadFields},
	"KubernetesReplicaSet":  {NodeType: NodeTypeWorkload, Fields: k8sWorkloadFields},

	// --- Batch workloads: distinct ontology types (no replicas/type in vocab) ---
	"KubernetesJob": {NodeType: NodeTypeJob, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},
	"KubernetesCronJob": {NodeType: NodeTypeCronJob, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},

	// --- Pod ---
	"KubernetesPod": {NodeType: NodeTypePod, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},

	// --- Node (type ← node_flavor = instance flavor; capacity_* are node.go keys) ---
	"KubernetesNode": {NodeType: NodeTypeNode, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "type", NodeField: "node_flavor"},
		{OntologyField: "region", NodeField: "node_region"},
		{OntologyField: "capacity_cpu", NodeField: "cpu_capacity"},
		{OntologyField: "capacity_memory", NodeField: "memory_capacity"},
	}},

	// --- Service ---
	"KubernetesService": {NodeType: NodeTypeK8sService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "type", NodeField: "service_type"},
		{OntologyField: "cluster_ip", NodeField: "cluster_ip"},
	}},

	// --- Namespace / Cluster ---
	"KubernetesNamespace": {NodeType: NodeTypeNamespace, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},
	// Cluster: minimal spec — createClusterNode sets only name (no version).
	"KubernetesCluster": {NodeType: NodeTypeCluster, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
	}},

	// --- Storage (state ← phase; capacity ← the .status.capacity map) ---
	"KubernetesPersistentVolumeClaim": {NodeType: NodeTypePVC, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "storage_class", NodeField: "storage_class"},
		{OntologyField: "capacity", NodeField: "capacity"},
		{OntologyField: "state", NodeField: "phase"},
	}},
	"KubernetesPersistentVolume": {NodeType: NodeTypePV, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "storage_class", NodeField: "storage_class"},
		{OntologyField: "capacity", NodeField: "capacity"},
		{OntologyField: "state", NodeField: "phase"},
	}},

	// --- Config / Secret / ServiceAccount ---
	"KubernetesConfigMap": {NodeType: NodeTypeConfigMap, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},
	"KubernetesSecret": {NodeType: NodeTypeK8sSecret, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "type", NodeField: "secret_type"},
	}},
	"KubernetesServiceAccount": {NodeType: NodeTypeK8sServiceAccount, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
	}},

	// --- Ingress (host ← first rule host, pinned by createIngressNode) ---
	"KubernetesIngress": {NodeType: NodeTypeIngress, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "namespace", NodeField: "namespace"},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "host", NodeField: "host"},
	}},

	// --- Karpenter: NodePool → ComputeInstancePool, NodeClaim → CustomResource ---
	// ComputeInstancePool vocab has no "cluster"; only name + state (Ready cond) apply.
	"KarpenterNodePool": {NodeType: NodeTypeComputeInstancePool, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "state", NodeField: "status"},
	}},
	"KarpenterNodeClaim": {NodeType: NodeTypeCRD, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "cluster", NodeField: "cluster"},
		{OntologyField: "type", NodeField: "crd_kind"},
	}},

	// --- Helm / Repository / Image ---
	"HelmChart": {NodeType: NodeTypeHelmChart, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "version", NodeField: "version"},
	}},
	// HelmRelease: minimal — no source module emits this node yet.
	"HelmRelease": {NodeType: NodeTypeHelmRelease, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
	}},
	"GitRepository": {NodeType: NodeTypeRepository, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "url", NodeField: "url"},
	}},
	// ContainerImage: emitted by sources/k8s/container_image.go (createContainerImageNodesAndEdges).
	"ContainerImage": {NodeType: NodeTypeContainerImage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "registry", NodeField: "registry"},
		{OntologyField: "tag", NodeField: "tag"},
		{OntologyField: "digest", NodeField: "digest"},
	}},
}
