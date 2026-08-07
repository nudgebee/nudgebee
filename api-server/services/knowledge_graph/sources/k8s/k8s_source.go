package k8s

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"time"
)

func init() {
	// Register K8s source factory with the global registry
	sources.RegisterSourceFactory("k8s", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewK8sSource(K8sSourceConfig{
			TenantID:       config.TenantID,
			CloudAccountID: config.CloudAccountID,
		}, logger)
	}, "Kubernetes resources source (workloads, pods, services)")
}

// K8sSource implements the Source interface for Kubernetes resources
type K8sSource struct {
	sources.BaseSource
	config  K8sSourceConfig
	logger  *slog.Logger
	enabled bool
}

// K8sSourceConfig holds configuration for K8s source
type K8sSourceConfig struct {
	TenantID        string
	CloudAccountID  string
	Namespace       string   // Filter by namespace
	Cluster         string   // Filter by cluster
	WorkloadKinds   []string // Filter by workload kinds (Deployment, StatefulSet, DaemonSet, Pod)
	IncludeInactive bool     // Include inactive workloads (default: false)
}

// K8sWorkloadRow represents a row from the k8s_workloads table
type K8sWorkloadRow struct {
	ID               string          `db:"id"`
	TenantID         string          `db:"tenant_id"`
	CloudAccountID   string          `db:"cloud_account_id"`
	Kind             string          `db:"kind"`
	Namespace        string          `db:"namespace"`
	Name             string          `db:"name"`
	ClusterName      string          `db:"cluster_name"`
	IsActive         bool            `db:"is_active"`
	Meta             json.RawMessage `db:"meta"`
	Labels           json.RawMessage `db:"labels"`
	CreatedAt        sql.NullTime    `db:"created_at"`
	UpdatedAt        sql.NullTime    `db:"updated_at"`
	LastDeployedTime sql.NullTime    `db:"last_deployed_time"`
}

// K8sNodeRow represents a row from the k8s_nodes table
type K8sNodeRow struct {
	TenantID          string          `db:"tenant_id"`
	CloudAccountID    string          `db:"cloud_account_id"`
	Name              string          `db:"name"`
	IsActive          bool            `db:"is_active"`
	NodeCreationTime  time.Time       `db:"node_creation_time"`
	Conditions        string          `db:"conditions"`
	NodeType          string          `db:"node_type"`
	NodeFlavor        string          `db:"node_flavor"`
	NodeRegion        string          `db:"node_region"`
	NodeZone          string          `db:"node_zone"`
	MemoryCapacity    float64         `db:"memory_capacity"`
	CPUCapacity       float64         `db:"cpu_capacity"`
	MemoryAllocatable float64         `db:"memory_allocatable"`
	CPUAllocatable    float64         `db:"cpu_allocatable"`
	Meta              json.RawMessage `db:"meta"`
	ClusterName       string          `db:"cluster_name"`
}

// NewK8sSource creates a new Kubernetes source
func NewK8sSource(config K8sSourceConfig, logger *slog.Logger) (*K8sSource, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// TenantID and CloudAccountID are optional at creation time
	// They will be provided in the SourceBuildRequest when BuildGraph is called

	// Set default workload kinds if not specified
	if len(config.WorkloadKinds) == 0 {
		config.WorkloadKinds = []string{
			"Deployment",
			"StatefulSet",
			"DaemonSet",
			"ReplicaSet",
			"Pod",
			"Service",
			"Job",
			"CronJob",
			"Ingress",
		}
	}

	return &K8sSource{
		BaseSource: sources.NewBaseSource("k8s"),
		config:     config,
		logger:     logger,
		enabled:    true,
	}, nil
}

// GetName returns the name of the source
func (s *K8sSource) GetName() string {
	return "k8s"
}

// IsEnabled checks if the source is enabled
func (s *K8sSource) IsEnabled() bool {
	return s.enabled
}

// Validate validates the source configuration
func (s *K8sSource) Validate() error {
	// TenantID and CloudAccountID are not required at source creation time
	// They are provided in the SourceBuildRequest when BuildGraph is called
	return nil
}

// GenerateUniqueKey generates a unique key for a K8s node
// Overrides sources.BaseSource.GenerateUniqueKey with K8s-specific logic
// Format: k8s:{cluster}:{region}:{NodeType}:{namespace}:{name}
func (s *K8sSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	// Create key components
	keyComponents := core.NewUniqueKeyComponents("k8s", node.NodeType)

	// Extract name
	name, _ := core.GetNodePropertyString(node, "name")
	keyComponents.Name = name

	// Extract cluster name from properties (still needed for certain node types)
	cluster, _ := core.GetNodePropertyString(node, "cluster")

	// Always use CloudAccountID (UUID) for unique key consistency
	// This ensures keys remain stable even if account names change
	if node.CloudAccountID != "" {
		keyComponents.Account = node.CloudAccountID
	}

	// Extract region/zone (location)
	region, _ := core.GetNodePropertyString(node, "region")
	if region == "" {
		region, _ = core.GetNodePropertyString(node, "zone")
	}
	if region != "" {
		keyComponents.Location = region
	}

	// Extract namespace (hierarchy)
	namespace, _ := core.GetNodePropertyString(node, "namespace")
	if namespace != "" {
		keyComponents.Hierarchy = namespace
	}

	// Handle special cases for cluster-scoped resources
	switch node.NodeType {
	case core.NodeTypeCluster:
		// Cluster has no hierarchy
		keyComponents.Hierarchy = ""
		// Cluster name is the resource name
		if keyComponents.Name == "" {
			keyComponents.Name = cluster
		}

	case core.NodeTypeNamespace:
		// Namespace itself has no parent namespace
		keyComponents.Hierarchy = ""
		// Namespace name is the resource name
		if keyComponents.Name == "" {
			keyComponents.Name = namespace
		}

	case core.NodeTypeNode:
		// K8s worker node is cluster-scoped, not namespaced
		keyComponents.Hierarchy = ""

	case core.NodeTypePV:
		// PersistentVolume is cluster-scoped, not namespaced
		keyComponents.Hierarchy = ""
	}

	// Validate and build
	if err := keyComponents.Validate(); err != nil {
		// Fallback to base implementation
		return s.BaseSource.GenerateUniqueKey(node)
	}

	return keyComponents.Build()
}

// BuildGraph builds a knowledge graph from Kubernetes resources
func (s *K8sSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	ctx := reqCtx.GetContext()
	s.logger.Info("building knowledge graph from Kubernetes resources",
		"tenant_id", req.TenantID,
		"cloud_account_id", req.CloudAccountID)

	startTime := time.Now()

	// Fetch K8s workloads from database
	workloads, err := s.fetchK8sWorkloads(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch K8s workloads: %w", err)
	}

	s.logger.Info("fetched K8s workloads", "count", len(workloads))

	// Load runtime datastore classification (the same framework signal the DBMS view
	// uses) so in-cluster databases/caches/queues can be tagged with their engine+role.
	// Non-fatal: on error we proceed with image-name fallback only.
	frameworks, err := GetWorkloadFrameworks(req.CloudAccountID)
	if err != nil {
		s.logger.Warn("failed to load workload frameworks, continuing with image-name classification only", "error", err)
		frameworks = map[string]string{}
	}

	// Fetch K8s nodes from database
	k8sNodes, err := s.fetchK8sNodes(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch K8s nodes: %w", err)
	}

	s.logger.Info("fetched K8s nodes", "count", len(k8sNodes))

	// Fetch K8s services from relay server
	k8sServices, err := s.fetchK8sServicesFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s services from relay, continuing without them", "error", err)
		k8sServices = []K8sServiceFromRelay{}
	}

	s.logger.Info("fetched K8s services from relay", "count", len(k8sServices))

	// Convert K8s nodes to graph nodes and edges first
	k8sNodeGraphNodes, k8sNodeEdges := s.convertK8sNodesToGraph(k8sNodes, req)

	// Build a map of node name to graph node for efficient lookup
	k8sNodeMap := make(map[string]*core.DbNode)
	for _, node := range k8sNodeGraphNodes {
		if name, ok := core.GetNodePropertyString(node, "name"); ok {
			k8sNodeMap[name] = node
		}
	}

	// Convert workloads to nodes and edges (passing k8sNodeMap to avoid duplicates)
	nodes, edges, k8sClusterMap, k8sNAmespaceMap, workloadNodesMap := s.convertWorkloadsToGraph(workloads, &k8sNodeMap, frameworks, req)

	// Convert services to nodes and edges
	serviceNodes, serviceEdges, _, _ := s.convertK8sServicesToGraph(k8sServices, workloads, k8sClusterMap, k8sNAmespaceMap, workloadNodesMap, req)

	// Append K8s node graph nodes and edges
	nodes = append(nodes, k8sNodeGraphNodes...)
	edges = append(edges, k8sNodeEdges...)

	// Append service nodes and edges
	nodes = append(nodes, serviceNodes...)
	edges = append(edges, serviceEdges...)

	// Fetch K8s PVCs from relay server
	k8sPVCs, err := s.fetchK8sPVCsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s PVCs from relay, continuing without them", "error", err)
		k8sPVCs = []K8sPVCFromRelay{}
	}

	s.logger.Info("fetched K8s PVCs from relay", "count", len(k8sPVCs))

	// Fetch K8s PVs from relay server
	k8sPVs, err := s.fetchK8sPVsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s PVs from relay, continuing without them", "error", err)
		k8sPVs = []K8sPVFromRelay{}
	}

	s.logger.Info("fetched K8s PVs from relay", "count", len(k8sPVs))

	// Convert PVs to nodes and edges first (needed for PVC -> PV relationships)
	pvNodes, pvEdges, _, _ := s.convertK8sPVsToGraph(k8sPVs, workloads, k8sClusterMap, k8sNAmespaceMap, req)

	// Append PV nodes and edges
	nodes = append(nodes, pvNodes...)
	edges = append(edges, pvEdges...)

	// Convert PVCs to nodes and edges (includes PVC -> PV relationships and workload -> PVC relationships)
	pvcNodes, pvcEdges, _, _ := s.convertK8sPVCsToGraph(k8sPVCs, workloads, k8sPVs, k8sClusterMap, k8sNAmespaceMap, workloadNodesMap, req)

	// Append PVC nodes and edges
	nodes = append(nodes, pvcNodes...)
	edges = append(edges, pvcEdges...)

	// Fetch K8s ServiceAccounts from relay server. SAs carry the IRSA
	// annotation (eks.amazonaws.com/role-arn) that ties a workload to an IAM
	// role; the cross-account ASSUMES edge is emitted by the phase-3 rules
	// engine via default_relationships.json.
	k8sServiceAccounts, err := s.fetchK8sServiceAccountsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s ServiceAccounts from relay, continuing without them", "error", err)
		k8sServiceAccounts = []K8sServiceAccountFromRelay{}
	}
	s.logger.Info("fetched K8s ServiceAccounts from relay", "count", len(k8sServiceAccounts))

	saNodes, saEdges, saByKey := s.convertK8sServiceAccountsToGraph(k8sServiceAccounts, workloads, k8sNAmespaceMap, req)
	nodes = append(nodes, saNodes...)
	edges = append(edges, saEdges...)

	workloadSAEdges := s.createWorkloadServiceAccountEdges(workloadNodesMap, saByKey, req)
	edges = append(edges, workloadSAEdges...)
	s.logger.Info("emitted ServiceAccount nodes + Workload→SA edges",
		"sa_nodes", len(saNodes), "workload_sa_edges", len(workloadSAEdges))

	// Fetch ConfigMaps + Secrets, emit nodes + BELONGS_TO edges to
	// namespaces, and wire Workload → USES_CONFIG / USES_SECRET edges by
	// walking each workload's pod spec for volume mounts and envFrom refs.
	// Secrets values are NEVER persisted — only key names + type +
	// metadata. Helm release state (`helm.sh/release.v1` type) is dropped
	// at fetch time to avoid drowning the graph in 500+ noise nodes per
	// cluster.
	k8sConfigMaps, err := s.fetchK8sConfigMapsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s ConfigMaps from relay, continuing without them", "error", err)
		k8sConfigMaps = []K8sConfigMapFromRelay{}
	}
	cmNodes, cmEdges, configMapByKey := s.convertK8sConfigMapsToGraph(k8sConfigMaps, workloads, k8sNAmespaceMap, req)
	nodes = append(nodes, cmNodes...)
	edges = append(edges, cmEdges...)

	k8sSecrets, err := s.fetchK8sSecretsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch K8s Secrets from relay, continuing without them", "error", err)
		k8sSecrets = []K8sSecretFromRelay{}
	}
	secNodes, secEdges, secretByKey := s.convertK8sSecretsToGraph(k8sSecrets, workloads, k8sNAmespaceMap, req)
	nodes = append(nodes, secNodes...)
	edges = append(edges, secEdges...)

	// Workload pod-template volume/envFrom refs are NOT in k8s_workloads
	// (the existing meta column strips them for everything except PVCs).
	// One round-trip per workload kind against the relay rebuilds the
	// ConfigMap / Secret reference map.
	workloadSpecRefs := s.fetchWorkloadConfigSecretRefs(ctx, req)
	workloadCMSecEdges := s.createWorkloadConfigSecretEdges(workloads, workloadNodesMap, configMapByKey, secretByKey, workloadSpecRefs, req)
	edges = append(edges, workloadCMSecEdges...)
	s.logger.Info("emitted ConfigMap/Secret nodes + Workload edges",
		"configmap_nodes", len(cmNodes), "secret_nodes", len(secNodes),
		"workloads_with_spec_refs", len(workloadSpecRefs),
		"workload_config_secret_edges", len(workloadCMSecEdges))

	// Fetch + emit Karpenter NodePool / NodeClaim. Karpenter NodePool is a
	// declarative provisioning spec; NodeClaim is a per-node lifecycle record.
	// Both are cluster-scoped CRDs. Cross-account edges (NodePool→ManagedCluster,
	// NodeClaim→ComputeInstance) are wired by phase-3 rules — see
	// karpenter_nodepool_to_eks_cluster + karpenter_nodeclaim_to_aws_ec2_instance.
	//
	// Only probe the relay when the cluster's agent heartbeat reports Karpenter
	// as its autoscaler. On the (common) clusters that don't run Karpenter —
	// GKE/AKS, cluster-autoscaler, no autoscaler — these CRDs are never served,
	// so an unconditional fetch fires four guaranteed-404 get_resource calls per
	// build (nodepools/nodeclaims × v1/v1beta1) that the in-cluster agent logs as
	// ERROR. The heartbeat already tells us whether Karpenter exists; don't ask
	// the cluster a question the backend has already answered.
	karpenterNodePools := []K8sCRDFromRelay{}
	karpenterNodeClaims := []K8sCRDFromRelay{}
	if s.clusterHasKarpenter(req) {
		nodePools, err := s.fetchKarpenterNodePoolsFromRelay(ctx, req)
		if err != nil {
			s.logger.Warn("failed to fetch Karpenter NodePools from relay, continuing without them", "error", err)
		} else {
			karpenterNodePools = nodePools
		}
		nodeClaims, err := s.fetchKarpenterNodeClaimsFromRelay(ctx, req)
		if err != nil {
			s.logger.Warn("failed to fetch Karpenter NodeClaims from relay, continuing without them", "error", err)
		} else {
			karpenterNodeClaims = nodeClaims
		}
	}

	// Resolve the cluster name from any K8s workload we already loaded —
	// Karpenter CRDs live in the same K8s account so they share the
	// cluster identity. Empty when no workloads (smoke-test path) — in
	// that case we still emit the nodes but they won't link to the
	// ManagedCluster via the phase-3 rule.
	karpenterClusterName := ""
	for _, w := range workloads {
		if w.ClusterName != "" {
			karpenterClusterName = w.ClusterName
			break
		}
	}
	// Fallback to K8s Nodes when no workloads carry the cluster name —
	// can happen for a freshly-provisioned cluster or one whose workloads
	// haven't been ingested yet. K8s Nodes are fetched from k8s_nodes and
	// carry the same cluster_name. Without this, Karpenter CRDs wouldn't
	// link back to their cluster on a cold-start build.
	if karpenterClusterName == "" {
		for _, n := range k8sNodes {
			if n.ClusterName != "" {
				karpenterClusterName = n.ClusterName
				break
			}
		}
	}

	// Build a name-keyed lookup over the K8s Node graph nodes we already
	// emitted. Used by the NodeClaim → RUNS_ON → Node intra-source edge.
	// We reuse k8sNodeGraphNodes (the original list) rather than the
	// mutated k8sNodeMap because convertWorkloadsToGraph may have removed
	// entries when reconciling cluster identities.
	k8sNodesByName := make(map[string]*core.DbNode, len(k8sNodeGraphNodes))
	for _, n := range k8sNodeGraphNodes {
		if name, ok := core.GetNodePropertyString(n, "name"); ok {
			k8sNodesByName[name] = n
		}
	}

	npNodes, npEdges, npByName := s.convertKarpenterNodePoolsToGraph(karpenterNodePools, karpenterClusterName, k8sClusterMap, req)
	ncNodes, ncEdges := s.convertKarpenterNodeClaimsToGraph(karpenterNodeClaims, npByName, k8sNodesByName, karpenterClusterName, req)
	nodeToPoolEdges := s.createNodeToKarpenterNodePoolEdges(k8sNodeGraphNodes, npByName, req)
	nodes = append(nodes, npNodes...)
	nodes = append(nodes, ncNodes...)
	edges = append(edges, npEdges...)
	edges = append(edges, ncEdges...)
	edges = append(edges, nodeToPoolEdges...)
	s.logger.Info("emitted Karpenter NodePool + NodeClaim nodes",
		"nodepools", len(npNodes), "nodeclaims", len(ncNodes),
		"nodepool_to_cluster_edges", len(npEdges),
		"nodepool_to_nodeclaim_edges", len(ncEdges),
		"node_to_nodepool_edges", len(nodeToPoolEdges))

	// Find ingress controller nodes and resolve backend services
	ingressControllers := s.findIngressControllerNodes(workloadNodesMap, serviceNodes)
	if len(ingressControllers) > 0 {
		s.logger.Info("Found ingress controller nodes", "count", len(ingressControllers))
		ingressBackendNodes, ingressBackendEdges := s.resolveIngressBackendServices(ctx, reqCtx, ingressControllers, serviceNodes, req)
		nodes = append(nodes, ingressBackendNodes...)
		edges = append(edges, ingressBackendEdges...)
		s.logger.Info("Resolved ingress backend services", "nodes", len(ingressBackendNodes), "edges", len(ingressBackendEdges))
	}

	// Deduplicate
	nodes = core.DeduplicateNodes(nodes)
	edges = core.DeduplicateEdges(edges)

	// Enrich cluster and K8s node resources with cloud account attributes
	if err := s.enrichK8sNodesWithCloudAttributes(reqCtx, req.CloudAccountID, nodes); err != nil {
		s.logger.Warn("failed to enrich K8s nodes with cloud account attributes", "error", err)
		// Don't fail the entire graph build if we can't get attributes
	}

	// Synthesize ContainerImage nodes + Workload/Pod → ContainerImage (USES_IMAGE)
	// edges from the image refs already carried on those nodes. Deduped per build so
	// a shared image is one node ("which workloads run image X"). Runs last, over the
	// accumulated nodes (before the image nodes themselves are appended).
	imageUsers := make([]*core.DbNode, 0)
	for _, n := range nodes {
		if _, ok := n.Properties["container_images"]; ok {
			imageUsers = append(imageUsers, n)
		} else if _, ok := n.Properties["primary_image"]; ok {
			imageUsers = append(imageUsers, n)
		}
	}
	imageNodes, imageEdges := s.createContainerImageNodesAndEdges(imageUsers, req)
	nodes = append(nodes, imageNodes...)
	edges = append(edges, imageEdges...)
	s.logger.Info("emitted ContainerImage nodes + Workload→Image edges",
		"image_nodes", len(imageNodes), "uses_image_edges", len(imageEdges))

	graph := &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
		GeneratedAt:    time.Now(),
	}

	s.logger.Info("successfully built knowledge graph from K8s resources",
		"nodes", len(nodes),
		"edges", len(edges),
		"duration", time.Since(startTime).Seconds())

	return graph, nil
}

// K8sServiceMetadata represents K8s service metadata from relay response
type K8sServiceMetadata struct {
	Annotations       map[string]interface{} `json:"annotations"`
	CreationTimestamp string                 `json:"creation_timestamp"`
	Labels            map[string]interface{} `json:"labels"`
	Name              string                 `json:"name"`
	Namespace         string                 `json:"namespace"`
	UID               string                 `json:"uid"`
}

// K8sServiceSpec represents K8s service spec from relay response
type K8sServiceSpec struct {
	ClusterIP       string                 `json:"cluster_ip"`
	ClusterIPs      []string               `json:"cluster_i_ps"`
	ExternalIPs     []string               `json:"external_i_ps"`
	IPFamilies      []string               `json:"ip_families"`
	IPFamilyPolicy  string                 `json:"ip_family_policy"`
	Ports           []K8sServicePort       `json:"ports"`
	Selector        map[string]interface{} `json:"selector"`
	SessionAffinity string                 `json:"session_affinity"`
	Type            string                 `json:"type"`
}

// K8sServicePort represents a K8s service port
type K8sServicePort struct {
	Name       string      `json:"name"`
	Port       int         `json:"port"`
	Protocol   string      `json:"protocol"`
	TargetPort interface{} `json:"target_port"`
	NodePort   *int        `json:"node_port"`
}

// K8sServiceLoadBalancerIngress represents a load balancer ingress entry
type K8sServiceLoadBalancerIngress struct {
	Hostname string      `json:"hostname"`
	IP       string      `json:"ip"`
	Ports    interface{} `json:"ports"`
}

// K8sServiceLoadBalancerStatus represents the load balancer status
type K8sServiceLoadBalancerStatus struct {
	Ingress []K8sServiceLoadBalancerIngress `json:"ingress"`
}

// K8sServiceStatus represents K8s service status from relay response
type K8sServiceStatus struct {
	LoadBalancer K8sServiceLoadBalancerStatus `json:"load_balancer"`
}

// K8sServiceFromRelay represents the K8s service structure from relay response
type K8sServiceFromRelay struct {
	Metadata K8sServiceMetadata `json:"metadata"`
	Spec     K8sServiceSpec     `json:"spec"`
	Status   K8sServiceStatus   `json:"status"`
}

// K8sPVCSpec represents K8s PersistentVolumeClaim spec from relay response
type K8sPVCSpec struct {
	AccessModes      []string               `json:"access_modes"`
	StorageClassName string                 `json:"storage_class_name"`
	VolumeName       string                 `json:"volume_name"`
	VolumeMode       string                 `json:"volume_mode"`
	Resources        map[string]interface{} `json:"resources"`
}

// K8sPVCStatus represents K8s PersistentVolumeClaim status from relay response
type K8sPVCStatus struct {
	Phase       string                 `json:"phase"` // Pending, Bound, Lost
	AccessModes []string               `json:"access_modes"`
	Capacity    map[string]interface{} `json:"capacity"`
}

// K8sPVCFromRelay represents the K8s PersistentVolumeClaim structure from relay response
type K8sPVCFromRelay struct {
	Metadata K8sServiceMetadata `json:"metadata"` // Reuse metadata structure
	Spec     K8sPVCSpec         `json:"spec"`
	Status   K8sPVCStatus       `json:"status"`
}

// K8sPVSpec represents K8s PersistentVolume spec from relay response
type K8sPVSpec struct {
	AccessModes                   []string               `json:"access_modes"`
	Capacity                      map[string]interface{} `json:"capacity"`
	StorageClassName              string                 `json:"storage_class_name"`
	VolumeMode                    string                 `json:"volume_mode"`
	PersistentVolumeReclaimPolicy string                 `json:"persistent_volume_reclaim_policy"`
	// Cloud-specific volume sources
	AWSElasticBlockStore map[string]interface{} `json:"aws_elastic_block_store"`
	AzureDisk            map[string]interface{} `json:"azure_disk"`
	GCEPersistentDisk    map[string]interface{} `json:"gce_persistent_disk"`
	// CSI driver — modern GKE/EKS/AKS use this instead of the cloud-native
	// volume-source fields above. VolumeHandle carries the underlying disk
	// identifier (e.g. "projects/.../disks/pvc-<uuid>" for pd.csi.storage.gke.io).
	CSI *K8sPVCSI `json:"csi,omitempty"`
}

// K8sPVCSI represents the .spec.csi sub-object on a PersistentVolume.
type K8sPVCSI struct {
	Driver           string                 `json:"driver"`
	VolumeHandle     string                 `json:"volume_handle"`
	FSType           string                 `json:"fs_type"`
	VolumeAttributes map[string]interface{} `json:"volume_attributes"`
}

// K8sPVStatus represents K8s PersistentVolume status from relay response
type K8sPVStatus struct {
	Phase   string `json:"phase"` // Available, Bound, Released, Failed
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

// K8sPVFromRelay represents the K8s PersistentVolume structure from relay response
type K8sPVFromRelay struct {
	Metadata K8sServiceMetadata `json:"metadata"` // Reuse metadata structure
	Spec     K8sPVSpec          `json:"spec"`
	Status   K8sPVStatus        `json:"status"`
}

// K8sServiceAccountFromRelay represents a K8s ServiceAccount as returned by the
// relay's generic get_resource action. We only care about metadata (name,
// namespace, annotations) — the rest of the SA spec (secrets, imagePullSecrets)
// is not currently surfaced in the KG.
type K8sServiceAccountFromRelay struct {
	Metadata K8sServiceMetadata `json:"metadata"`
}

// K8sConfigMapFromRelay represents the K8s ConfigMap structure from a relay
// `get_resource` response. We store the key names but never the values:
// ConfigMaps can carry sizeable blobs (whole JSON / YAML configs) and the
// per-tenant graph is supposed to model topology, not act as a cluster-state
// archive. Callers can fetch the full data live via the relay when needed.
type K8sConfigMapFromRelay struct {
	Metadata K8sServiceMetadata     `json:"metadata"`
	Data     map[string]interface{} `json:"data"`
	// BinaryData holds non-UTF-8 entries; we record the keys only.
	BinaryData map[string]interface{} `json:"binary_data"`
}

// K8sSecretFromRelay represents the K8s Secret structure from a relay
// `get_resource` response. We deliberately drop `Data` (base64-encoded
// secret material) — the KG records the key names and the secret Type
// only. Values stay on the cluster.
type K8sSecretFromRelay struct {
	Metadata K8sServiceMetadata     `json:"metadata"`
	Type     string                 `json:"type"`
	Data     map[string]interface{} `json:"data"`
}

// helmReleaseSecretType is the Type Helm stamps on the per-release Secret
// it stores in `kube-system` / the install namespace. These dominate the
// secret count in any Helm-managed cluster (591 of 813 in the surveyed
// production tenant) and don't represent a credential the workload uses —
// they're release state. Filtered out at the source so they don't pollute
// the graph or the SaveNodes batch.
const helmReleaseSecretType = "helm.sh/release.v1"

// helmManagedByLabel — Helm stamps this on every resource it owns. Used
// for the optional `managed_by` query_attribute on ConfigMap nodes.
const helmManagedByLabel = "app.kubernetes.io/managed-by"

// workloadSpecRefs holds the de-duplicated ConfigMap and Secret names
// referenced by one workload's pod-template spec. Keyed by
// "kind/namespace/name" in WorkloadSpecRefsMap.
type workloadSpecRefs struct {
	ConfigMaps []string
	Secrets    []string
	// SecretRefKinds maps a Secret name from Secrets to the sorted set of
	// forms the spec consumed it in — "volume", "env", "env_from". See
	// refsFromPodSpec.
	SecretRefKinds map[string][]string
}

// K8sCRDMetadata captures the operator-CRD metadata shape we care about
// across Karpenter NodePool / NodeClaim and any future CR we choose to
// emit. ResourceVersion + OwnerReferences let us link a NodeClaim back to
// its owning NodePool without a second relay round-trip.
type K8sCRDMetadata struct {
	Name              string                   `json:"name"`
	Namespace         string                   `json:"namespace,omitempty"`
	Labels            map[string]interface{}   `json:"labels,omitempty"`
	Annotations       map[string]interface{}   `json:"annotations,omitempty"`
	OwnerReferences   []map[string]interface{} `json:"ownerReferences,omitempty"`
	CreationTimestamp string                   `json:"creationTimestamp,omitempty"`
	ResourceVersion   string                   `json:"resourceVersion,omitempty"`
	UID               string                   `json:"uid,omitempty"`
}

// K8sCRDFromRelay is the wire shape we deserialize Karpenter CRDs into.
// Spec/Status are kept as untyped maps because the K8s API surface is wide
// and we only need a few well-known paths (.spec.limits, .status.providerID,
// .status.conditions) that are simpler to walk than to model.
type K8sCRDFromRelay struct {
	Metadata K8sCRDMetadata         `json:"metadata"`
	Spec     map[string]interface{} `json:"spec,omitempty"`
	Status   map[string]interface{} `json:"status,omitempty"`
}

// Karpenter resource-fetch constants. We try v1 first and fall back to
// v1beta1 because EKS clusters running Karpenter ≤0.37 still serve the
// beta API (v1 graduated in Karpenter 1.0). When the requested
// group/version isn't served the relay's get_resource action surfaces
// either an inner ACTION_UNEXPECTED_ERROR or an empty result — both are
// safe to retry against v1beta1.
const (
	karpenterGroup             = "karpenter.sh"
	karpenterAPIVersionV1      = "v1"
	karpenterAPIVersionV1Beta1 = "v1beta1"
)

// IRSAAnnotation is the kubelet-recognized annotation on a ServiceAccount that
// binds it to an AWS IAM Role via STS AssumeRoleWithWebIdentity (IRSA). When
// present, the ARN is hoisted to properties.role_arn so the phase-3
// cross-account matcher can join SA → ServiceIdentity by ARN.
const IRSAAnnotation = "eks.amazonaws.com/role-arn"

// GKEWorkloadIdentityAnnotation marks a K8s ServiceAccount as bound to a GCP
// IAM ServiceAccount via GKE Workload Identity (the GCP analog of IRSA). The
// annotation value is the GCP SA email; it's hoisted to
// properties.gcp_service_account_email so the phase-3 cross-account matcher
// can join SA → ServiceIdentity by email (rule:
// k8s_serviceaccount_to_gcp_iam_sa_wi). Issue #31101 gap #10/#11.
const GKEWorkloadIdentityAnnotation = "iam.gke.io/gcp-service-account"

// karpenterNodePoolLabel is the Pod/Workload label Karpenter stamps on
// resources scheduled by a specific NodePool. Used by the phase-3 rule
// k8s_workload_to_karpenter_nodepool to wire Workload → RUNS_ON → NodePool.
const karpenterNodePoolLabel = "karpenter.sh/nodepool"

// karpenterProvisionedBy is the discriminator value we stamp on
// ComputeInstancePool + CustomResource nodes that originate from
// Karpenter, so queries can distinguish them from EKS NodeGroup pools or
// other CRDs sharing the same NodeType.
const karpenterProvisionedBy = "karpenter"

// SetEnabled enables or disables the source
func (s *K8sSource) SetEnabled(enabled bool) {
	s.enabled = enabled
}

// ConvertToKnowledgeGraph converts the graph from this source to KnowledgeGraph format
func (s *K8sSource) ConvertToKnowledgeGraph(graph *core.Graph) core.KnowledgeGraph {
	return core.ConvertGraphToKnowledgeGraph(graph)
}

// ConvertEdgesToKgEdges converts DbEdges to KgEdges for this source
func (s *K8sSource) ConvertEdgesToKgEdges(dbEdges []*core.DbEdge) []core.KgEdge {
	return core.ConvertDbEdgesToKgEdges(dbEdges)
}

// =====================================================
// Ingress Backend Service Resolution
// =====================================================

// ingressControllerMap holds ingress controllers indexed by class
type ingressControllerMap struct {
	byClass           map[string]*core.DbNode
	defaultController *core.DbNode
}

// ingressBackendProcessor handles processing of ingress resources
type ingressBackendProcessor struct {
	source           *K8sSource
	k8sAccountID     string
	tenantID         string
	controllerMap    *ingressControllerMap
	uniqueBackends   map[string]*core.DbNode
	existingServices map[string]*core.DbNode // lookup map for existing K8s services by namespace:name
	nodes            []*core.DbNode
	edges            []*core.DbEdge
	req              *core.SourceBuildRequest
}

// K8s Ingress types for parsing kubectl output
type k8sIngressBackend struct {
	Service struct {
		Name string `json:"name"`
		Port struct {
			Number int `json:"number"`
		} `json:"port"`
	} `json:"service"`
}

type k8sIngressPath struct {
	Path    string            `json:"path"`
	Backend k8sIngressBackend `json:"backend"`
}

type k8sIngressRule struct {
	Host string `json:"host"`
	HTTP struct {
		Paths []k8sIngressPath `json:"paths"`
	} `json:"http"`
}

type k8sIngressSpec struct {
	IngressClassName string           `json:"ingressClassName"`
	Rules            []k8sIngressRule `json:"rules"`
}

type k8sIngressResource struct {
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
	} `json:"metadata"`
	Spec   k8sIngressSpec   `json:"spec"`
	Status k8sIngressStatus `json:"status,omitempty"`
}

type k8sIngressStatus struct {
	LoadBalancer struct {
		Ingress []struct {
			Hostname string `json:"hostname,omitempty"`
			IP       string `json:"ip,omitempty"`
		} `json:"ingress,omitempty"`
	} `json:"loadBalancer,omitempty"`
}

type k8sIngressList struct {
	Items []k8sIngressResource `json:"items"`
}
