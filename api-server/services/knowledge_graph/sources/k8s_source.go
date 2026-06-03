package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	annotationkeys "nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/lib/pq"
)

func init() {
	// Register K8s source factory with the global registry
	RegisterSourceFactory("k8s", func(config SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewK8sSource(K8sSourceConfig{
			TenantID:       config.TenantID,
			CloudAccountID: config.CloudAccountID,
		}, logger)
	}, "Kubernetes resources source (workloads, pods, services)")
}

// K8sSource implements the Source interface for Kubernetes resources
type K8sSource struct {
	BaseSource
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
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
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
		BaseSource: NewBaseSource("k8s"),
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
// Overrides BaseSource.GenerateUniqueKey with K8s-specific logic
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
	nodes, edges, k8sClusterMap, k8sNAmespaceMap, workloadNodesMap := s.convertWorkloadsToGraph(workloads, &k8sNodeMap, req)

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

	// Fetch + emit Karpenter NodePool / NodeClaim. Karpenter NodePool is a
	// declarative provisioning spec; NodeClaim is a per-node lifecycle record.
	// Both are cluster-scoped CRDs. Cross-account edges (NodePool→ManagedCluster,
	// NodeClaim→ComputeInstance) are wired by phase-3 rules — see
	// karpenter_nodepool_to_eks_cluster + karpenter_nodeclaim_to_aws_ec2_instance.
	karpenterNodePools, err := s.fetchKarpenterNodePoolsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch Karpenter NodePools from relay, continuing without them", "error", err)
		karpenterNodePools = []K8sCRDFromRelay{}
	}
	karpenterNodeClaims, err := s.fetchKarpenterNodeClaimsFromRelay(ctx, req)
	if err != nil {
		s.logger.Warn("failed to fetch Karpenter NodeClaims from relay, continuing without them", "error", err)
		karpenterNodeClaims = []K8sCRDFromRelay{}
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

// fetchK8sWorkloads queries K8s workloads from the k8s_workloads table
func (s *K8sSource) fetchK8sWorkloads(ctx context.Context, req *core.SourceBuildRequest) ([]K8sWorkloadRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Build query
	query := `
		SELECT
			w.cloud_resource_id as id, w.tenant_id, w.cloud_account_id, w.kind, w.namespace, w.name,
			ca.account_name as cluster_name, w.is_active, w.meta, w.labels, w.creation_time as created_at, w.creation_time as updated_at,
			w.last_deployed_time
		FROM k8s_workloads w
		LEFT JOIN cloud_accounts ca ON w.cloud_account_id = ca.id
		WHERE w.tenant_id = $1
	`

	args := []interface{}{req.TenantID}
	argIndex := 2

	// Filter by cloud account
	if req.CloudAccountID != "" {
		query += fmt.Sprintf(" AND w.cloud_account_id = $%d", argIndex)
		args = append(args, req.CloudAccountID)
		argIndex++
	}

	// Filter by namespace if specified
	if s.config.Namespace != "" {
		query += fmt.Sprintf(" AND w.namespace = $%d", argIndex)
		args = append(args, s.config.Namespace)
		argIndex++
	}

	// Filter by cluster if specified
	if s.config.Cluster != "" {
		query += fmt.Sprintf(" AND ca.account_name = $%d", argIndex)
		args = append(args, s.config.Cluster)
		argIndex++
	}

	// Filter by workload kinds if specified
	if len(s.config.WorkloadKinds) > 0 {
		query += fmt.Sprintf(" AND w.kind = ANY($%d)", argIndex)
		args = append(args, pq.Array(s.config.WorkloadKinds))
	}

	// Filter by active status
	if !s.config.IncludeInactive {
		query += " AND w.is_active = true"
	}

	query += " ORDER BY w.kind, w.namespace, w.name"

	var workloads []K8sWorkloadRow
	err = dbManager.Db.Select(&workloads, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query k8s_workloads: %w", err)
	}

	s.logger.Info("queried K8s workloads from database",
		"count", len(workloads),
		"tenant_id", req.TenantID)

	return workloads, nil
}

// fetchK8sNodes queries K8s nodes from the k8s_nodes table
func (s *K8sSource) fetchK8sNodes(ctx context.Context, req *core.SourceBuildRequest) ([]K8sNodeRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Build query
	query := `
		SELECT
			n.tenant_id, n.cloud_account_id, n.name, n.is_active, n.node_creation_time,
			n.conditions, n.node_type, n.node_flavor, n.node_region, n.node_zone,
			n.memory_capacity, n.cpu_capacity, n.memory_allocatable, n.cpu_allocatable,
			n.meta, ca.account_name as cluster_name
		FROM k8s_nodes n
		LEFT JOIN cloud_accounts ca ON n.cloud_account_id = ca.id
		WHERE n.tenant_id = $1
	`

	args := []interface{}{req.TenantID}
	argIndex := 2

	// Filter by cloud account
	if req.CloudAccountID != "" {
		query += fmt.Sprintf(" AND n.cloud_account_id = $%d", argIndex)
		args = append(args, req.CloudAccountID)
		argIndex++
	}

	// Filter by cluster if specified
	if s.config.Cluster != "" {
		query += fmt.Sprintf(" AND ca.account_name = $%d", argIndex)
		args = append(args, s.config.Cluster)
	}

	// Filter by active status
	if !s.config.IncludeInactive {
		query += " AND n.is_active = true"
	}

	query += " ORDER BY n.name"

	var nodes []K8sNodeRow
	err = dbManager.Db.Select(&nodes, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query k8s_nodes: %w", err)
	}

	s.logger.Info("queried K8s nodes from database",
		"count", len(nodes),
		"tenant_id", req.TenantID)

	return nodes, nil
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

// fetchK8sServicesFromRelay fetches K8s services from the relay server
func (s *K8sSource) fetchK8sServicesFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sServiceFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay service fetch: cloud_account_id is empty")
		return []K8sServiceFromRelay{}, nil
	}

	s.logger.Info("fetching K8s services from relay server",
		"account_id", req.CloudAccountID)

	// Execute relay request
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "services",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Warn("failed to execute relay request for K8s services", "error", err)
		return nil, fmt.Errorf("failed to execute relay request: %w", err)
	}

	// Parse the nested response structure
	services, err := s.parseK8sServicesResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse K8s services response", "error", err)
		return nil, fmt.Errorf("failed to parse services response: %w", err)
	}

	s.logger.Info("successfully fetched K8s services from relay",
		"count", len(services),
		"account_id", req.CloudAccountID)

	return services, nil
}

// parseK8sServicesResponse parses the nested relay response to extract K8s services
func (s *K8sSource) parseK8sServicesResponse(response map[string]interface{}) ([]K8sServiceFromRelay, error) {
	// Navigate: response -> data
	dataAny, ok := response["data"]
	if !ok {
		return nil, fmt.Errorf("response missing 'data' field")
	}

	data, ok := dataAny.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'data' is not a map")
	}

	// Navigate: data -> findings
	findingsAny, ok := data["findings"]
	if !ok {
		return nil, fmt.Errorf("data missing 'findings' field")
	}

	findings, ok := findingsAny.([]interface{})
	if !ok || len(findings) == 0 {
		s.logger.Warn("no findings in relay response")
		return []K8sServiceFromRelay{}, nil
	}

	// Get first finding
	finding, ok := findings[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first finding is not a map")
	}

	// Navigate: finding -> evidence
	evidenceAny, ok := finding["evidence"]
	if !ok {
		return nil, fmt.Errorf("finding missing 'evidence' field")
	}

	evidence, ok := evidenceAny.([]interface{})
	if !ok || len(evidence) == 0 {
		s.logger.Warn("no evidence in relay response")
		return []K8sServiceFromRelay{}, nil
	}

	// Get first evidence
	evidenceItem, ok := evidence[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first evidence is not a map")
	}

	// Navigate: evidence -> data (JSON string)
	evidenceDataAny, ok := evidenceItem["data"]
	if !ok {
		return nil, fmt.Errorf("evidence missing 'data' field")
	}

	evidenceDataStr, ok := evidenceDataAny.(string)
	if !ok {
		return nil, fmt.Errorf("evidence data is not a string")
	}

	// Parse the JSON string to get the array
	var dataArray []map[string]interface{}
	if err := json.Unmarshal([]byte(evidenceDataStr), &dataArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evidence data: %w", err)
	}

	if len(dataArray) == 0 {
		s.logger.Warn("empty data array in evidence")
		return []K8sServiceFromRelay{}, nil
	}

	// Get the first element which contains the actual services data
	firstData := dataArray[0]

	// Navigate: firstData -> data (array of services)
	servicesDataAny, ok := firstData["data"]
	if !ok {
		return nil, fmt.Errorf("first data element missing 'data' field")
	}

	// Parse as array of services
	var servicesData []interface{}
	switch v := servicesDataAny.(type) {
	case string:
		// If it's a string, unmarshal it
		if err := json.Unmarshal([]byte(v), &servicesData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal services data string: %w", err)
		}
	case []interface{}:
		// If it's already an array, use it directly
		servicesData = v
	default:
		return nil, fmt.Errorf("services data is neither string nor array")
	}

	// Convert to K8sServiceFromRelay structs
	services := make([]K8sServiceFromRelay, 0, len(servicesData))
	for _, svcAny := range servicesData {
		svcMap, ok := svcAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid service entry")
			continue
		}

		// Marshal and unmarshal to convert to struct
		svcBytes, err := json.Marshal(svcMap)
		if err != nil {
			s.logger.Warn("failed to marshal service", "error", err)
			continue
		}

		var service K8sServiceFromRelay
		if err := json.Unmarshal(svcBytes, &service); err != nil {
			s.logger.Warn("failed to unmarshal service", "error", err)
			continue
		}

		services = append(services, service)
	}

	return services, nil
}

// fetchK8sPVCsFromRelay fetches K8s PersistentVolumeClaims from the relay server
func (s *K8sSource) fetchK8sPVCsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sPVCFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay PVC fetch: cloud_account_id is empty")
		return []K8sPVCFromRelay{}, nil
	}

	s.logger.Info("fetching K8s PVCs from relay server",
		"resource_type", "persistentvolumeclaims",
		"account_id", req.CloudAccountID)

	// Execute relay request
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "persistentvolumeclaims",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for PVCs", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for PVCs: %w", err)
	}

	// Parse response
	pvcs, err := s.parseK8sPVCsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse PVCs response", "error", err)
		return nil, fmt.Errorf("failed to parse PVCs response: %w", err)
	}

	s.logger.Info("successfully fetched K8s PVCs from relay", "count", len(pvcs))
	return pvcs, nil
}

// parseRelayDataArray extracts and parses the data array from a relay response,
// handling potentially nested JSON structures.
// Returns the parsed array and any error encountered during navigation or parsing.
func (s *K8sSource) parseRelayDataArray(response map[string]interface{}, resourceType string) ([]interface{}, error) {
	// Navigate: response -> data -> findings -> evidence -> data (JSON string) -> array
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("response.data is not a map")
	}

	findings, ok := data["findings"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("response.data.findings is not an array")
	}

	if len(findings) == 0 {
		s.logger.Info("no findings in response", "resource_type", resourceType)
		return []interface{}{}, nil
	}

	// Get first finding
	firstFinding, ok := findings[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("first finding is not a map")
	}

	// Evidence is an array containing maps
	evidenceArray, ok := firstFinding["evidence"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("finding.evidence is not an array")
	}

	if len(evidenceArray) == 0 {
		return nil, fmt.Errorf("finding.evidence array is empty")
	}

	// Get the first evidence item
	evidenceItem, ok := evidenceArray[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("evidence item is not a map")
	}

	dataStr, ok := evidenceItem["data"].(string)
	if !ok {
		return nil, fmt.Errorf("evidence.data is not a string")
	}

	// Parse the data JSON string
	var dataArray []interface{}
	if err := json.Unmarshal([]byte(dataStr), &dataArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evidence.data: %w", err)
	}

	// Handle nested structure: dataArray might contain a wrapper with type:"json" and data field
	var actualDataArray []interface{}
	if len(dataArray) > 0 {
		if wrapperMap, ok := dataArray[0].(map[string]interface{}); ok {
			if dataType, exists := wrapperMap["type"]; exists && dataType == "json" {
				// Extract nested data field
				if nestedDataStr, ok := wrapperMap["data"].(string); ok {
					if err := json.Unmarshal([]byte(nestedDataStr), &actualDataArray); err != nil {
						return nil, fmt.Errorf("failed to unmarshal nested evidence.data: %w", err)
					}
				} else {
					return nil, fmt.Errorf("nested data field is not a string")
				}
			} else {
				// No wrapper, use dataArray as-is
				actualDataArray = dataArray
			}
		} else {
			// Not a wrapper map, use dataArray as-is
			actualDataArray = dataArray
		}
	}

	return actualDataArray, nil
}

// parseK8sPVCsResponse parses the relay response for K8s PersistentVolumeClaims
func (s *K8sSource) parseK8sPVCsResponse(response map[string]interface{}) ([]K8sPVCFromRelay, error) {
	pvcs := make([]K8sPVCFromRelay, 0)

	// Extract data array using shared helper
	actualDataArray, err := s.parseRelayDataArray(response, "PVCs")
	if err != nil {
		return pvcs, err
	}

	// Return early if no data
	if len(actualDataArray) == 0 {
		return pvcs, nil
	}

	// Parse each PVC
	for _, pvcAny := range actualDataArray {
		pvcMap, ok := pvcAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid PVC entry")
			continue
		}

		pvcBytes, err := json.Marshal(pvcMap)
		if err != nil {
			s.logger.Warn("failed to marshal PVC", "error", err)
			continue
		}

		var pvc K8sPVCFromRelay
		if err := json.Unmarshal(pvcBytes, &pvc); err != nil {
			s.logger.Warn("failed to unmarshal PVC", "error", err)
			continue
		}

		pvcs = append(pvcs, pvc)
	}

	return pvcs, nil
}

// fetchK8sPVsFromRelay fetches K8s PersistentVolumes from the relay server
func (s *K8sSource) fetchK8sPVsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sPVFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay PV fetch: cloud_account_id is empty")
		return []K8sPVFromRelay{}, nil
	}

	s.logger.Info("fetching K8s PVs from relay server",
		"resource_type", "persistentvolumes",
		"account_id", req.CloudAccountID)

	// Execute relay request
	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "persistentvolumes",
				"all_namespaces": false, // PVs are cluster-scoped
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for PVs", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for PVs: %w", err)
	}

	// Parse response
	pvs, err := s.parseK8sPVsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse PVs response", "error", err)
		return nil, fmt.Errorf("failed to parse PVs response: %w", err)
	}

	s.logger.Info("successfully fetched K8s PVs from relay", "count", len(pvs))
	return pvs, nil
}

// parseK8sPVsResponse parses the relay response for K8s PersistentVolumes
func (s *K8sSource) parseK8sPVsResponse(response map[string]interface{}) ([]K8sPVFromRelay, error) {
	pvs := make([]K8sPVFromRelay, 0)

	// Extract data array using shared helper
	actualDataArray, err := s.parseRelayDataArray(response, "PVs")
	if err != nil {
		return pvs, err
	}

	// Return early if no data
	if len(actualDataArray) == 0 {
		return pvs, nil
	}

	// Parse each PV
	for _, pvAny := range actualDataArray {
		pvMap, ok := pvAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid PV entry")
			continue
		}

		pvBytes, err := json.Marshal(pvMap)
		if err != nil {
			s.logger.Warn("failed to marshal PV", "error", err)
			continue
		}

		var pv K8sPVFromRelay
		if err := json.Unmarshal(pvBytes, &pv); err != nil {
			s.logger.Warn("failed to unmarshal PV", "error", err)
			continue
		}

		pvs = append(pvs, pv)
	}

	return pvs, nil
}

// fetchK8sServiceAccountsFromRelay fetches K8s ServiceAccounts via the relay's
// generic get_resource action. Same shape as fetchK8sPVCsFromRelay — only the
// resource_type differs. Powers the IRSA chain (SA carries the
// eks.amazonaws.com/role-arn annotation that ties a workload to an IAM role).
func (s *K8sSource) fetchK8sServiceAccountsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sServiceAccountFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay ServiceAccount fetch: cloud_account_id is empty")
		return []K8sServiceAccountFromRelay{}, nil
	}

	s.logger.Info("fetching K8s ServiceAccounts from relay server",
		"resource_type", "serviceaccounts",
		"account_id", req.CloudAccountID)

	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":          "",
				"version":        "v1",
				"resource_type":  "serviceaccounts",
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Error("failed to execute relay request for ServiceAccounts", "error", err)
		return nil, fmt.Errorf("failed to execute relay request for ServiceAccounts: %w", err)
	}

	sas, err := s.parseK8sServiceAccountsResponse(relayResponse)
	if err != nil {
		s.logger.Error("failed to parse ServiceAccounts response", "error", err)
		return nil, fmt.Errorf("failed to parse ServiceAccounts response: %w", err)
	}

	s.logger.Info("successfully fetched K8s ServiceAccounts from relay", "count", len(sas))
	return sas, nil
}

// parseK8sServiceAccountsResponse parses the relay response for K8s ServiceAccounts.
func (s *K8sSource) parseK8sServiceAccountsResponse(response map[string]interface{}) ([]K8sServiceAccountFromRelay, error) {
	sas := make([]K8sServiceAccountFromRelay, 0)

	actualDataArray, err := s.parseRelayDataArray(response, "ServiceAccounts")
	if err != nil {
		return sas, err
	}

	if len(actualDataArray) == 0 {
		return sas, nil
	}

	for _, saAny := range actualDataArray {
		saMap, ok := saAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid ServiceAccount entry")
			continue
		}

		saBytes, err := json.Marshal(saMap)
		if err != nil {
			s.logger.Warn("failed to marshal ServiceAccount", "error", err)
			continue
		}

		var sa K8sServiceAccountFromRelay
		if err := json.Unmarshal(saBytes, &sa); err != nil {
			s.logger.Warn("failed to unmarshal ServiceAccount", "error", err)
			continue
		}

		sas = append(sas, sa)
	}

	return sas, nil
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

// fetchKarpenterNodePoolsFromRelay fetches Karpenter NodePool CRs via the
// relay's generic get_resource action. NodePool is cluster-scoped (not
// namespace-scoped), so we pass all_namespaces=false. Soft-fails on relay
// error — Karpenter isn't required infrastructure for the KG to build.
func (s *K8sSource) fetchKarpenterNodePoolsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sCRDFromRelay, error) {
	return s.fetchKarpenterCRDFromRelay(ctx, req, "nodepools")
}

// fetchKarpenterNodeClaimsFromRelay fetches Karpenter NodeClaim CRs.
// NodeClaim is also cluster-scoped.
func (s *K8sSource) fetchKarpenterNodeClaimsFromRelay(ctx context.Context, req *core.SourceBuildRequest) ([]K8sCRDFromRelay, error) {
	return s.fetchKarpenterCRDFromRelay(ctx, req, "nodeclaims")
}

// fetchKarpenterCRDFromRelay is the shared fetch helper for Karpenter
// CRDs. Tries the v1 API first, falls back to v1beta1 if v1 returns an
// error or empty result — supports both Karpenter 1.x clusters (v1) and
// older clusters running Karpenter 0.32-0.37 (v1beta1).
func (s *K8sSource) fetchKarpenterCRDFromRelay(ctx context.Context, req *core.SourceBuildRequest, resourceType string) ([]K8sCRDFromRelay, error) {
	if req.CloudAccountID == "" {
		s.logger.Warn("skipping relay Karpenter fetch: cloud_account_id is empty",
			"resource_type", resourceType)
		return []K8sCRDFromRelay{}, nil
	}

	// Primary attempt: v1 (Karpenter 1.x).
	crds, v1Err := s.fetchKarpenterCRDForVersion(req, resourceType, karpenterAPIVersionV1)
	if v1Err == nil && len(crds) > 0 {
		return crds, nil
	}

	// Fallback: v1beta1 (Karpenter 0.32 – 0.37). Same fetch shape, different
	// API version. We treat both error and empty as triggers because the
	// relay's response when the requested version isn't served can be either
	// an inner ACTION_UNEXPECTED_ERROR or an empty findings list depending
	// on the agent's K8s client version.
	if v1Err != nil {
		s.logger.Info("Karpenter v1 fetch failed, falling back to v1beta1",
			"resource_type", resourceType, "v1_error", v1Err)
	} else {
		s.logger.Info("Karpenter v1 fetch returned empty, falling back to v1beta1",
			"resource_type", resourceType)
	}
	crdsBeta, betaErr := s.fetchKarpenterCRDForVersion(req, resourceType, karpenterAPIVersionV1Beta1)
	if betaErr != nil {
		// Both versions failed — return the v1 error if we had one (more
		// likely to surface the real issue, eg auth), else the v1beta1 error.
		if v1Err != nil {
			return nil, fmt.Errorf("failed to fetch %s on both v1 and v1beta1 (v1: %w; v1beta1: %v)", resourceType, v1Err, betaErr)
		}
		return nil, fmt.Errorf("failed to fetch %s on v1beta1 (v1 returned empty): %w", resourceType, betaErr)
	}
	return crdsBeta, nil
}

// fetchKarpenterCRDForVersion executes a single get_resource call for a
// specific (group, version, resource_type). Same shape as
// fetchK8sServiceAccountsFromRelay.
func (s *K8sSource) fetchKarpenterCRDForVersion(req *core.SourceBuildRequest, resourceType, version string) ([]K8sCRDFromRelay, error) {
	s.logger.Info("fetching Karpenter CRDs from relay server",
		"resource_type", resourceType,
		"group", karpenterGroup,
		"version", version,
		"account_id", req.CloudAccountID)

	relayRequest := relay.RelayExecuteRequest{
		NoSinks: false,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:  req.CloudAccountID,
			ActionName: "get_resource",
			ActionParams: map[string]interface{}{
				"group":         karpenterGroup,
				"version":       version,
				"resource_type": resourceType,
				// NodePool / NodeClaim are cluster-scoped, but the relay agent's
				// get_resource action requires all_namespaces=true to enumerate
				// CRDs regardless of scope — it dispatches to a generic kubectl-get
				// path that needs the flag set. With all_namespaces=false the
				// agent returns ACTION_UNEXPECTED_ERROR (status_code 500 inside
				// an HTTP 200 envelope). Verified via direct relay probe 2026-05-26.
				"all_namespaces": true,
			},
		},
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		s.logger.Warn("failed to execute relay request for Karpenter CRD",
			"resource_type", resourceType, "version", version, "error", err)
		return nil, fmt.Errorf("failed to execute relay request for %s (%s): %w", resourceType, version, err)
	}

	crds, err := s.parseKarpenterCRDResponse(relayResponse, resourceType)
	if err != nil {
		s.logger.Warn("failed to parse Karpenter CRD response",
			"resource_type", resourceType, "version", version, "error", err)
		return nil, fmt.Errorf("failed to parse %s response (%s): %w", resourceType, version, err)
	}

	s.logger.Info("successfully fetched Karpenter CRDs from relay",
		"resource_type", resourceType, "version", version, "count", len(crds))
	return crds, nil
}

// parseKarpenterCRDResponse parses a Karpenter CRD list response into
// K8sCRDFromRelay records. Mirrors parseK8sServiceAccountsResponse.
func (s *K8sSource) parseKarpenterCRDResponse(response map[string]interface{}, resourceType string) ([]K8sCRDFromRelay, error) {
	crds := make([]K8sCRDFromRelay, 0)

	actualDataArray, err := s.parseRelayDataArray(response, resourceType)
	if err != nil {
		return crds, err
	}

	if len(actualDataArray) == 0 {
		return crds, nil
	}

	for _, crdAny := range actualDataArray {
		crdMap, ok := crdAny.(map[string]interface{})
		if !ok {
			s.logger.Warn("skipping invalid CRD entry", "resource_type", resourceType)
			continue
		}

		crdBytes, err := json.Marshal(crdMap)
		if err != nil {
			s.logger.Warn("failed to marshal CRD", "resource_type", resourceType, "error", err)
			continue
		}

		var crd K8sCRDFromRelay
		if err := json.Unmarshal(crdBytes, &crd); err != nil {
			s.logger.Warn("failed to unmarshal CRD", "resource_type", resourceType, "error", err)
			continue
		}

		crds = append(crds, crd)
	}

	return crds, nil
}

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

// createK8sServiceAccountNode builds a ServiceAccount DbNode from a relay-fetched
// SA. Cloud-identity-binding annotations are hoisted to top-level properties
// so phase-3 cross-account matchers don't have to walk the annotation map.
func (s *K8sSource) createK8sServiceAccountNode(sa *K8sServiceAccountFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":      sa.Metadata.Name,
		"namespace": sa.Metadata.Namespace,
		"cluster":   clusterName,
	}
	if len(sa.Metadata.Labels) > 0 {
		properties["labels"] = sa.Metadata.Labels
	}
	if len(sa.Metadata.Annotations) > 0 {
		properties["annotations"] = sa.Metadata.Annotations
		if roleArnAny, ok := sa.Metadata.Annotations[IRSAAnnotation]; ok {
			if roleArn, ok := roleArnAny.(string); ok && roleArn != "" {
				properties["role_arn"] = roleArn
			}
		}
		if gcpSAAny, ok := sa.Metadata.Annotations[GKEWorkloadIdentityAnnotation]; ok {
			if gcpSA, ok := gcpSAAny.(string); ok && gcpSA != "" {
				properties["gcp_service_account_email"] = gcpSA
			}
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeK8sServiceAccount,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeK8sServiceAccount, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertK8sServiceAccountsToGraph emits ServiceAccount nodes + BELONGS_TO edges
// to their owning namespace. Returns the SA lookup map keyed by
// "namespace/name" — used by createWorkloadServiceAccountEdges to wire
// Workload → USES_SERVICE_ACCOUNT → ServiceAccount.
//
// The cross-account SA → ASSUMES → ServiceIdentity edge is NOT emitted here —
// that hop is wired by the phase-3 cross-account rules engine via
// default_relationships.json (rule: k8s_serviceaccount_to_aws_iam_role_irsa),
// because per-source lookup.byARN cannot see the AWS source's ServiceIdentity
// nodes at this stage.
func (s *K8sSource) convertK8sServiceAccountsToGraph(sas []K8sServiceAccountFromRelay, workloads []K8sWorkloadRow, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(sas))
	edges := make([]*core.DbEdge, 0)
	saByKey := make(map[string]*core.DbNode, len(sas)) // "namespace/name" -> SA node

	for i := range sas {
		sa := &sas[i]
		clusterName := s.getClusterNameForResource(sa.Metadata.Namespace, workloads)
		saNode := s.createK8sServiceAccountNode(sa, clusterName, req)
		nodes = append(nodes, saNode)
		saByKey[fmt.Sprintf("%s/%s", sa.Metadata.Namespace, sa.Metadata.Name)] = saNode

		// Link SA to its namespace if we have one.
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, sa.Metadata.Namespace)
		if nsNode, ok := namespaceNodes[namespaceKey]; ok {
			edges = append(edges, core.NewEdge(
				saNode.ID, nsNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{"connection_type": "namespace_membership"},
				req.TenantID, req.CloudAccountID, "k8s",
			))
		}
	}

	return nodes, edges, saByKey
}

// createWorkloadServiceAccountEdges emits Workload → USES_SERVICE_ACCOUNT →
// ServiceAccount edges for every workload whose properties.service_account_name
// resolves to a known SA in the per-build lookup. SAs absent from the lookup
// (eg implicit `default` SAs that kubectl didn't return) are skipped silently.
func (s *K8sSource) createWorkloadServiceAccountEdges(workloadNodes map[string]*core.DbNode, saByKey map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	if len(saByKey) == 0 || len(workloadNodes) == 0 {
		return nil
	}
	edges := make([]*core.DbEdge, 0)
	for _, w := range workloadNodes {
		if w == nil {
			continue
		}
		saName, _ := core.GetNodePropertyString(w, "service_account_name")
		if saName == "" {
			continue
		}
		namespace, _ := core.GetNodePropertyString(w, "namespace")
		if namespace == "" {
			continue
		}
		saNode, ok := saByKey[fmt.Sprintf("%s/%s", namespace, saName)]
		if !ok {
			continue
		}
		edges = append(edges, core.NewEdge(
			w.ID, saNode.ID,
			core.RelationshipUsesServiceAccount,
			map[string]interface{}{
				"connection_type":      "service_account",
				"service_account_name": saName,
			},
			req.TenantID, req.CloudAccountID, "k8s",
		))
	}
	return edges
}

// karpenterNodePoolLabel is the Pod/Workload label Karpenter stamps on
// resources scheduled by a specific NodePool. Used by the phase-3 rule
// k8s_workload_to_karpenter_nodepool to wire Workload → RUNS_ON → NodePool.
const karpenterNodePoolLabel = "karpenter.sh/nodepool"

// karpenterProvisionedBy is the discriminator value we stamp on
// ComputeInstancePool + CustomResource nodes that originate from
// Karpenter, so queries can distinguish them from EKS NodeGroup pools or
// other CRDs sharing the same NodeType.
const karpenterProvisionedBy = "karpenter"

// createKarpenterNodePoolNode builds a ComputeInstancePool DbNode from a
// fetched Karpenter NodePool CR. NodePool is cluster-scoped so namespace
// is left empty; the unique-key hierarchy resolves to "" via the existing
// GenerateUniqueKey rule for cluster-scoped resources.
func (s *K8sSource) createKarpenterNodePoolNode(np *K8sCRDFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":           np.Metadata.Name,
		"cluster":        clusterName,
		"provisioned_by": karpenterProvisionedBy,
		"subtype":        "KarpenterNodePool",
		"crd_kind":       "NodePool",
		"crd_group":      karpenterGroup,
	}
	if len(np.Metadata.Labels) > 0 {
		properties["labels"] = np.Metadata.Labels
	}
	if len(np.Metadata.Annotations) > 0 {
		properties["annotations"] = np.Metadata.Annotations
	}
	// Capacity limits — surface them so capacity-planning queries work.
	if spec := np.Spec; spec != nil {
		if limits, ok := spec["limits"].(map[string]interface{}); ok {
			if cpu, ok := limits["cpu"]; ok {
				properties["cpu_limit"] = cpu
			}
			if mem, ok := limits["memory"]; ok {
				properties["memory_limit"] = mem
			}
		}
		// disruption.consolidationPolicy / consolidateAfter — useful operationally.
		if disruption, ok := spec["disruption"].(map[string]interface{}); ok {
			if p, ok := disruption["consolidationPolicy"].(string); ok {
				properties["consolidation_policy"] = p
			}
		}
	}
	// Status condition — Ready=True is the most common gate.
	if status := np.Status; status != nil {
		if conditions, ok := status["conditions"].([]interface{}); ok {
			for _, c := range conditions {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if v, ok := cm["status"].(string); ok {
							properties["status"] = v
						}
					}
				}
			}
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeComputeInstancePool,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeComputeInstancePool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createKarpenterNodeClaimNode builds a CustomResource DbNode for a
// Karpenter NodeClaim. NodeClaim is cluster-scoped (no namespace).
// `provider_id` is hoisted from .status.providerID so the phase-3
// cross-account rule can match it to a ComputeInstance.
func (s *K8sSource) createKarpenterNodeClaimNode(nc *K8sCRDFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":           nc.Metadata.Name,
		"cluster":        clusterName,
		"provisioned_by": karpenterProvisionedBy,
		"crd_kind":       "NodeClaim",
		"crd_group":      karpenterGroup,
	}
	if len(nc.Metadata.Labels) > 0 {
		properties["labels"] = nc.Metadata.Labels
	}
	if status := nc.Status; status != nil {
		if pid, ok := status["providerID"].(string); ok && pid != "" {
			properties["provider_id"] = pid
		}
		if nn, ok := status["nodeName"].(string); ok && nn != "" {
			properties["node_name"] = nn
		}
		// Ready / Launched / Registered conditions.
		if conditions, ok := status["conditions"].([]interface{}); ok {
			for _, c := range conditions {
				if cm, ok := c.(map[string]interface{}); ok {
					if t, _ := cm["type"].(string); t == "Ready" {
						if v, ok := cm["status"].(string); ok {
							properties["status"] = v
						}
					}
				}
			}
		}
	}
	if spec := nc.Spec; spec != nil {
		// .spec.requirements is a list of label-selector terms; we hoist
		// the well-known capacity-type one (spot vs on-demand) because
		// it's the field operators ask about most.
		if reqs, ok := spec["requirements"].([]interface{}); ok {
			for _, r := range reqs {
				if rm, ok := r.(map[string]interface{}); ok {
					if k, _ := rm["key"].(string); k == "karpenter.sh/capacity-type" {
						if vals, ok := rm["values"].([]interface{}); ok && len(vals) > 0 {
							if v, ok := vals[0].(string); ok {
								properties["capacity_type"] = v
							}
						}
					}
				}
			}
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeCRD,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeCRD, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertKarpenterNodePoolsToGraph emits ComputeInstancePool nodes for
// the Karpenter NodePools, plus an intra-source NodePool → BELONGS_TO →
// Cluster edge. The Cluster node is in turn linked to the AWS-side
// ManagedCluster by the existing phase-3 rule k8s_cluster_to_eks_cluster_by_name,
// so the full chain ComputeInstancePool → Cluster → ManagedCluster is
// reachable in 2 hops without us guessing the EKS cluster name from
// Karpenter metadata (which doesn't carry it). Returns the NodePool
// lookup map keyed by name — used by convertKarpenterNodeClaimsToGraph
// to wire the MANAGES edge.
func (s *K8sSource) convertKarpenterNodePoolsToGraph(nps []K8sCRDFromRelay, clusterName string, clusterNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0, len(nps))
	edges := make([]*core.DbEdge, 0, len(nps))
	byName := make(map[string]*core.DbNode, len(nps))
	for i := range nps {
		np := &nps[i]
		npNode := s.createKarpenterNodePoolNode(np, clusterName, req)
		nodes = append(nodes, npNode)
		byName[np.Metadata.Name] = npNode

		// NodePool → BELONGS_TO → Cluster (intra-source). The Cluster node
		// is keyed by Nudgebee's account_name (eg "k8s-prod"), which is the
		// same value we stamp on the NodePool's properties.cluster.
		if clusterName != "" {
			if clusterNode, ok := clusterNodes[clusterName]; ok {
				edges = append(edges, core.NewEdge(
					npNode.ID, clusterNode.ID,
					core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "karpenter_nodepool_in_cluster"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}
	}
	return nodes, edges, byName
}

// createNodeToKarpenterNodePoolEdges wires Node → BELONGS_TO → NodePool by
// reading the `karpenter.sh/nodepool` label that Karpenter stamps on every
// node it provisions. This is intra-source (both endpoints from k8s_source)
// rather than a phase-3 rule because the cross_account_relationships matcher
// resolves dotted property paths by splitting on `.` and walking sub-maps —
// so a path like `labels.karpenter.sh/nodepool` is read as
// `labels → karpenter → sh/nodepool` and misses the actual `karpenter.sh/nodepool`
// label key (which is a single string with dots in it). Wiring this in Go
// sidesteps that limitation without changing the matcher.
func (s *K8sSource) createNodeToKarpenterNodePoolEdges(k8sNodes []*core.DbNode, nodePoolsByName map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	if len(nodePoolsByName) == 0 || len(k8sNodes) == 0 {
		return nil
	}
	edges := make([]*core.DbEdge, 0)
	for _, n := range k8sNodes {
		if n == nil {
			continue
		}
		labels, ok := n.Properties["labels"].(map[string]interface{})
		if !ok {
			continue
		}
		poolName, ok := labels[karpenterNodePoolLabel].(string)
		if !ok || poolName == "" {
			continue
		}
		npNode, exists := nodePoolsByName[poolName]
		if !exists {
			continue
		}
		edges = append(edges, core.NewEdge(
			n.ID, npNode.ID,
			core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "karpenter_node_provisioned_by",
				"nodepool":        poolName,
			},
			req.TenantID, req.CloudAccountID, "k8s",
		))
	}
	return edges
}

// convertKarpenterNodeClaimsToGraph emits CustomResource nodes for the
// NodeClaims plus the two intra-source edges:
//   - NodePool → MANAGES → NodeClaim (via NodeClaim's ownerReferences)
//   - NodeClaim → RUNS_ON → Node (via .status.nodeName matched to existing
//     K8s Node graph nodes by name)
//
// The cross-account NodeClaim → ComputeInstance HOSTED_ON edge is wired
// by the phase-3 rule karpenter_nodeclaim_to_aws_ec2_instance via
// .status.providerID.
func (s *K8sSource) convertKarpenterNodeClaimsToGraph(ncs []K8sCRDFromRelay, nodePoolsByName map[string]*core.DbNode, k8sNodesByName map[string]*core.DbNode, clusterName string, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	nodes := make([]*core.DbNode, 0, len(ncs))
	edges := make([]*core.DbEdge, 0)

	for i := range ncs {
		nc := &ncs[i]
		ncNode := s.createKarpenterNodeClaimNode(nc, clusterName, req)
		nodes = append(nodes, ncNode)

		// NodePool → MANAGES → NodeClaim. NodeClaim's ownerReferences[].name
		// holds the NodePool name. We accept any ownerRef whose kind is
		// "NodePool" — Karpenter is the only operator currently emitting
		// that kind.
		for _, owner := range nc.Metadata.OwnerReferences {
			ownerKind, _ := owner["kind"].(string)
			ownerName, _ := owner["name"].(string)
			if ownerKind != "NodePool" || ownerName == "" {
				continue
			}
			if npNode, ok := nodePoolsByName[ownerName]; ok {
				edges = append(edges, core.NewEdge(
					npNode.ID, ncNode.ID,
					core.RelationshipManages,
					map[string]interface{}{"connection_type": "karpenter_nodepool_manages_nodeclaim"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}

		// NodeClaim → RUNS_ON → Node. .status.nodeName is the K8s Node the
		// claim materialized into. May be empty for pending/failed claims.
		if nodeName, _ := core.GetNodePropertyString(ncNode, "node_name"); nodeName != "" {
			if nodeGraphNode, ok := k8sNodesByName[nodeName]; ok {
				edges = append(edges, core.NewEdge(
					ncNode.ID, nodeGraphNode.ID,
					core.RelationshipRunsOn,
					map[string]interface{}{"connection_type": "karpenter_nodeclaim_materialized_node"},
					req.TenantID, req.CloudAccountID, "k8s",
				))
			}
		}
	}

	return nodes, edges
}

// convertK8sServicesToGraph converts K8s services from relay to knowledge graph nodes and edges
func (s *K8sSource) convertK8sServicesToGraph(services []K8sServiceFromRelay, workloads []K8sWorkloadRow, clusterNodes, namespaceNodes map[string]*core.DbNode, workloadNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of workloads for matching services to pods
	workloadMap := make(map[string]*K8sWorkloadRow)
	for i := range workloads {
		key := fmt.Sprintf("%s/%s", workloads[i].Namespace, workloads[i].Name)
		workloadMap[key] = &workloads[i]
	}

	for _, service := range services {
		// Determine cluster name - we'll get this from the first matching workload
		// or use a default value
		clusterName := s.getClusterNameForService(&service, workloads)

		// Create service node
		serviceNode := s.createNodeFromK8sService(&service, clusterName, req)
		nodes = append(nodes, serviceNode)

		// Create or get namespace node
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, service.Metadata.Namespace)
		var namespaceNode *core.DbNode
		if existingNs, exists := namespaceNodes[namespaceKey]; exists {
			namespaceNode = existingNs
		} else {
			namespaceNode = s.createNamespaceNode(service.Metadata.Namespace, clusterName, req)
			namespaceNodes[namespaceKey] = namespaceNode
			nodes = append(nodes, namespaceNode)
		}

		// Link service to namespace
		edge := core.NewEdge(
			serviceNode.ID,
			namespaceNode.ID,
			core.RelationshipRunsOn,
			map[string]interface{}{
				"connection_type": "namespace",
			},
			req.TenantID,
			req.CloudAccountID,
			"k8s",
		)
		edges = append(edges, edge)

		// Create or get cluster node
		if clusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[clusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(clusterName, req)
				clusterNodes[clusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link namespace to cluster (if not already linked)
			nsToClusterEdge := core.NewEdge(
				namespaceNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, nsToClusterEdge)
		}

		// Match service to workloads based on selector
		if len(service.Spec.Selector) > 0 {
			matchedWorkloads := s.matchServiceToWorkloads(&service, workloads)
			for _, workload := range matchedWorkloads {
				// Find the actual workload node - use the same key format as convertWorkloadsToGraph
				workloadKey := fmt.Sprintf("%s/%s/%s/%s",
					workload.ClusterName,
					workload.Kind,
					workload.Namespace,
					workload.Name)

				workloadNode, workloadExists := workloadNodes[workloadKey]
				if !workloadExists {
					continue
				}

				// Create edge from service to workload (service exposes workload)
				edge := core.NewEdge(
					workloadNode.ID,
					serviceNode.ID,
					core.RelationshipExposes,
					map[string]interface{}{
						"connection_type": "service_selector",
						"selector":        service.Spec.Selector,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	return nodes, edges, clusterNodes, namespaceNodes
}

// convertK8sPVCsToGraph converts K8s PersistentVolumeClaims to knowledge graph nodes and edges
func (s *K8sSource) convertK8sPVCsToGraph(pvcs []K8sPVCFromRelay, workloads []K8sWorkloadRow, pvs []K8sPVFromRelay, clusterNodes, namespaceNodes map[string]*core.DbNode, workloadNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of PVCs for quick lookup
	pvcNodes := make(map[string]*core.DbNode) // key: "cluster/namespace/name" -> node
	// Build a map of PVs for binding relationships
	pvMap := make(map[string]*K8sPVFromRelay) // key: "pv_name" -> PV

	for _, pv := range pvs {
		pvMap[pv.Metadata.Name] = &pv
	}

	for _, pvc := range pvcs {
		// Determine cluster name from workloads
		clusterName := s.getClusterNameForResource(pvc.Metadata.Namespace, workloads)

		// Create PVC node
		pvcNode := s.createNodeFromK8sPVC(&pvc, clusterName, req)
		nodes = append(nodes, pvcNode)

		// Store in map for relationship creation
		pvcKey := fmt.Sprintf("%s/%s/%s", clusterName, pvc.Metadata.Namespace, pvc.Metadata.Name)
		pvcNodes[pvcKey] = pvcNode

		// Create or get namespace node
		namespaceKey := fmt.Sprintf("%s/%s", clusterName, pvc.Metadata.Namespace)
		var namespaceNode *core.DbNode
		if existingNs, exists := namespaceNodes[namespaceKey]; exists {
			namespaceNode = existingNs
		} else {
			namespaceNode = s.createNamespaceNode(pvc.Metadata.Namespace, clusterName, req)
			namespaceNodes[namespaceKey] = namespaceNode
			nodes = append(nodes, namespaceNode)
		}

		// Link PVC to namespace
		edge := core.NewEdge(
			pvcNode.ID,
			namespaceNode.ID,
			core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "namespace",
			},
			req.TenantID,
			req.CloudAccountID,
			"k8s",
		)
		edges = append(edges, edge)

		// Create or get cluster node
		if clusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[clusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(clusterName, req)
				clusterNodes[clusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link namespace to cluster (if not already linked)
			nsToClusterEdge := core.NewEdge(
				namespaceNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, nsToClusterEdge)
		}

		// Create PVC -> PV relationship if PVC is bound
		if pvc.Spec.VolumeName != "" && pvc.Status.Phase == "Bound" {
			// Find the PV from the map and create edge
			if pv, exists := pvMap[pvc.Spec.VolumeName]; exists {
				// Create PV node to get the proper unique key
				pvNode := s.createNodeFromK8sPV(pv, clusterName, req)
				edge := core.NewEdge(
					pvcNode.ID,
					pvNode.ID,
					core.RelationshipIsBoundTo,
					map[string]interface{}{
						"connection_type": "volume_binding",
						"volume_name":     pvc.Spec.VolumeName,
						"phase":           pvc.Status.Phase,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	// Create relationships between workloads and PVCs
	for _, workload := range workloads {
		// Find the actual workload node - include cluster to match the key format
		workloadKey := fmt.Sprintf("%s/%s/%s/%s", workload.ClusterName, workload.Kind, workload.Namespace, workload.Name)
		workloadNode, workloadExists := workloadNodes[workloadKey]
		if !workloadExists {
			continue
		}

		// Extract PVC references from workload metadata (for direct PVC mounts)
		pvcRefs := s.extractPVCReferences(&workload)
		for _, pvcName := range pvcRefs {
			// Find the PVC node
			pvcKey := fmt.Sprintf("%s/%s/%s", workload.ClusterName, workload.Namespace, pvcName)
			if pvcNode, exists := pvcNodes[pvcKey]; exists {
				// Create edge from workload to PVC (workload mounts PVC)
				edge := core.NewEdge(
					workloadNode.ID,
					pvcNode.ID,
					core.RelationshipMounts,
					map[string]interface{}{
						"connection_type": "pvc_mount",
						"pvc_name":        pvcName,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)
			}
		}
	}

	// Create relationships between StatefulSets and their dynamically created PVCs
	// StatefulSet PVCs follow the naming pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
	statefulSetEdges := s.matchStatefulSetPVCs(pvcs, workloads, workloadNodes, pvcNodes, req)
	edges = append(edges, statefulSetEdges...)

	return nodes, edges, clusterNodes, namespaceNodes
}

// matchStatefulSetPVCs matches PVCs to StatefulSets based on the naming convention
// StatefulSet PVCs follow the pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
func (s *K8sSource) matchStatefulSetPVCs(pvcs []K8sPVCFromRelay, workloads []K8sWorkloadRow, workloadNodes map[string]*core.DbNode, pvcNodes map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build a map of StatefulSets by namespace for quick lookup
	statefulSets := make(map[string][]K8sWorkloadRow) // namespace -> []StatefulSet
	for _, workload := range workloads {
		if workload.Kind == "StatefulSet" {
			statefulSets[workload.Namespace] = append(statefulSets[workload.Namespace], workload)
		}
	}

	// Track which PVCs have already been matched to avoid duplicates
	matchedPVCs := make(map[string]bool)

	for _, pvc := range pvcs {
		pvcName := pvc.Metadata.Name
		pvcNamespace := pvc.Metadata.Namespace

		// Skip if no StatefulSets in this namespace
		stsInNamespace, exists := statefulSets[pvcNamespace]
		if !exists {
			continue
		}

		// Try to match this PVC to a StatefulSet
		for _, sts := range stsInNamespace {
			// Check if PVC name matches the StatefulSet naming pattern
			// Pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
			if s.isPVCForStatefulSet(pvcName, sts.Name) {
				// Get the cluster name for this PVC
				clusterName := s.getClusterNameForResource(pvcNamespace, workloads)

				// Find the workload node
				workloadKey := fmt.Sprintf("%s/%s/%s/%s", sts.ClusterName, sts.Kind, sts.Namespace, sts.Name)
				workloadNode, workloadExists := workloadNodes[workloadKey]
				if !workloadExists {
					continue
				}

				// Find the PVC node
				pvcKey := fmt.Sprintf("%s/%s/%s", clusterName, pvcNamespace, pvcName)
				pvcNode, pvcExists := pvcNodes[pvcKey]
				if !pvcExists {
					continue
				}

				// Avoid duplicate edges
				edgeKey := fmt.Sprintf("%s->%s", workloadNode.ID, pvcNode.ID)
				if matchedPVCs[edgeKey] {
					continue
				}
				matchedPVCs[edgeKey] = true

				// Create edge from StatefulSet to PVC
				edge := core.NewEdge(
					workloadNode.ID,
					pvcNode.ID,
					core.RelationshipMounts,
					map[string]interface{}{
						"connection_type": "statefulset_pvc",
						"pvc_name":        pvcName,
						"statefulset":     sts.Name,
					},
					req.TenantID,
					req.CloudAccountID,
					"k8s",
				)
				edges = append(edges, edge)

				s.logger.Debug("matched StatefulSet PVC",
					"statefulset", sts.Name,
					"pvc", pvcName,
					"namespace", pvcNamespace)

				break // PVC can only belong to one StatefulSet
			}
		}
	}

	return edges
}

// isPVCForStatefulSet checks if a PVC name matches the StatefulSet naming pattern
// Pattern: <volumeClaimTemplateName>-<statefulsetName>-<ordinal>
// Example: data-my-statefulset-0, data-my-statefulset-1
func (s *K8sSource) isPVCForStatefulSet(pvcName, statefulSetName string) bool {
	// The PVC name should contain the StatefulSet name followed by a dash and ordinal
	// Pattern: <prefix>-<statefulsetName>-<ordinal>

	// Check if the PVC name contains the StatefulSet name
	if !strings.Contains(pvcName, statefulSetName) {
		return false
	}

	// Find the position of the StatefulSet name in the PVC name
	stsIdx := strings.Index(pvcName, statefulSetName)
	if stsIdx <= 0 {
		// StatefulSet name should not be at the beginning (there should be a volume template prefix)
		return false
	}

	// Check that there's a dash before the StatefulSet name (separating volume template name)
	if pvcName[stsIdx-1] != '-' {
		return false
	}

	// Check what comes after the StatefulSet name
	afterSts := pvcName[stsIdx+len(statefulSetName):]

	// Should be "-<ordinal>" pattern (e.g., "-0", "-1", "-2")
	if len(afterSts) < 2 {
		return false
	}

	if afterSts[0] != '-' {
		return false
	}

	// Check if the rest is a valid ordinal (digits only)
	ordinal := afterSts[1:]
	for _, c := range ordinal {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

// convertK8sPVsToGraph converts K8s PersistentVolumes to knowledge graph nodes and edges
func (s *K8sSource) convertK8sPVsToGraph(pvs []K8sPVFromRelay, workloads []K8sWorkloadRow, clusterNodes, namespaceNodes map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	for _, pv := range pvs {
		// Determine cluster name from workloads (or use first available cluster)
		clusterName := ""
		if len(workloads) > 0 {
			clusterName = workloads[0].ClusterName
		}

		// Create PV node
		pvNode := s.createNodeFromK8sPV(&pv, clusterName, req)
		nodes = append(nodes, pvNode)

		// Create or get cluster node
		if clusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[clusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(clusterName, req)
				clusterNodes[clusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link PV to cluster
			edge := core.NewEdge(
				pvNode.ID,
				clusterNode.ID,
				core.RelationshipBelongsTo,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, edge)
		}
	}

	return nodes, edges, clusterNodes, namespaceNodes
}

// getClusterNameForService determines the cluster name for a service by looking at workloads
func (s *K8sSource) getClusterNameForService(service *K8sServiceFromRelay, workloads []K8sWorkloadRow) string {
	return clusterNameForNamespace(service.Metadata.Namespace, workloads)
}

// getClusterNameForResource determines the cluster name for a resource by looking at workloads in the same namespace
func (s *K8sSource) getClusterNameForResource(namespace string, workloads []K8sWorkloadRow) string {
	return clusterNameForNamespace(namespace, workloads)
}

// clusterNameForNamespace resolves a cluster name for a namespace-scoped resource.
// First-choice: any workload in the same namespace (carries the matching cluster
// label). Fallback: any workload in this account that has a cluster name set.
// Without the fallback, namespaces that hold no workloads of their own (only
// Services, PVCs, ServiceAccounts) drop their Namespace→Cluster edge and end up
// as orphan nodes (clickhouse, envoy-gateway-system, splunk, mongodb in
// production).
func clusterNameForNamespace(namespace string, workloads []K8sWorkloadRow) string {
	for _, workload := range workloads {
		if workload.Namespace == namespace && workload.ClusterName != "" {
			return workload.ClusterName
		}
	}
	for _, workload := range workloads {
		if workload.ClusterName != "" {
			return workload.ClusterName
		}
	}
	return ""
}

// extractPVCReferences extracts PVC names referenced by a workload from its metadata
func (s *K8sSource) extractPVCReferences(workload *K8sWorkloadRow) []string {
	pvcNames := make([]string, 0)
	seenNames := make(map[string]bool)

	// Parse workload metadata
	var metaMap map[string]interface{}
	if len(workload.Meta) > 0 {
		if err := json.Unmarshal(workload.Meta, &metaMap); err != nil {
			return pvcNames
		}

		// If volumes is under config.volumes
		if config, ok := metaMap["config"].(map[string]interface{}); ok {
			if volumes, ok := config["volumes"].([]interface{}); ok {
				for _, volume := range volumes {
					if volumeMap, ok := volume.(map[string]interface{}); ok {
						if pvc, ok := volumeMap["persistent_volume_claim"].(map[string]interface{}); ok {
							if claimName, ok := pvc["claim_name"].(string); ok && claimName != "" {
								if !seenNames[claimName] {
									pvcNames = append(pvcNames, claimName)
									seenNames[claimName] = true
								}
							}
						}
					}
				}
			}
		}
	}

	return pvcNames
}

// createNodeFromK8sService creates a knowledge graph node from a K8s service
func (s *K8sSource) createNodeFromK8sService(service *K8sServiceFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = service.Metadata.Name
	properties["namespace"] = service.Metadata.Namespace
	properties["cluster"] = clusterName
	properties["uid"] = service.Metadata.UID
	properties["cluster_ip"] = service.Spec.ClusterIP
	properties["service_type"] = service.Spec.Type
	properties["session_affinity"] = service.Spec.SessionAffinity

	// Add labels if present
	if len(service.Metadata.Labels) > 0 {
		properties["labels"] = service.Metadata.Labels
	}

	// Add annotations if present
	if len(service.Metadata.Annotations) > 0 {
		properties["annotations"] = service.Metadata.Annotations
	}

	// Add selector if present
	if len(service.Spec.Selector) > 0 {
		properties["selector"] = service.Spec.Selector
	}

	// Add ports and extract node_ports for cross-source matching
	if len(service.Spec.Ports) > 0 {
		ports := make([]map[string]interface{}, 0, len(service.Spec.Ports))
		nodePorts := make([]int, 0)
		for _, port := range service.Spec.Ports {
			portMap := map[string]interface{}{
				"name":     port.Name,
				"port":     port.Port,
				"protocol": port.Protocol,
			}
			if port.TargetPort != nil {
				portMap["target_port"] = port.TargetPort
			}
			if port.NodePort != nil {
				portMap["node_port"] = *port.NodePort
				nodePorts = append(nodePorts, *port.NodePort)
			}
			ports = append(ports, portMap)
		}
		properties["ports"] = ports
		// Add node_ports as top-level property for ALB -> NodePort service matching
		if len(nodePorts) > 0 {
			properties["node_ports"] = nodePorts
		}
	}

	// Add external IPs if present
	if len(service.Spec.ExternalIPs) > 0 {
		properties["external_ips"] = service.Spec.ExternalIPs
	}

	// Add cluster IPs if present
	if len(service.Spec.ClusterIPs) > 0 {
		properties["cluster_ips"] = service.Spec.ClusterIPs
	}

	// Add creation timestamp
	if service.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = service.Metadata.CreationTimestamp
	}

	// Extract load balancer hostname from status (for services of type LoadBalancer)
	if service.Spec.Type == "LoadBalancer" {
		if len(service.Status.LoadBalancer.Ingress) > 0 {
			firstIngress := service.Status.LoadBalancer.Ingress[0]
			// AWS ELBs use "hostname" field
			if firstIngress.Hostname != "" {
				properties["load_balancer_hostname"] = firstIngress.Hostname
			}
			// Some cloud providers use "ip" field instead
			if firstIngress.IP != "" {
				properties["load_balancer_ip"] = firstIngress.IP
			}
		}
	}

	// Add subtype property for K8s service
	properties["subtype"] = service.Spec.Type

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeK8sService,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeK8sService, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createNodeFromK8sPVC creates a knowledge graph node from a K8s PersistentVolumeClaim
func (s *K8sSource) createNodeFromK8sPVC(pvc *K8sPVCFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = pvc.Metadata.Name
	properties["namespace"] = pvc.Metadata.Namespace
	properties["cluster"] = clusterName
	properties["uid"] = pvc.Metadata.UID
	properties["phase"] = pvc.Status.Phase
	properties["storage_class"] = pvc.Spec.StorageClassName
	properties["volume_name"] = pvc.Spec.VolumeName
	properties["volume_mode"] = pvc.Spec.VolumeMode

	// Add access modes
	if len(pvc.Spec.AccessModes) > 0 {
		properties["access_modes"] = pvc.Spec.AccessModes
	}

	// Add capacity if available
	if len(pvc.Status.Capacity) > 0 {
		properties["capacity"] = pvc.Status.Capacity
	}

	// Add requested resources
	if len(pvc.Spec.Resources) > 0 {
		properties["resources"] = pvc.Spec.Resources
	}

	// Add labels if present
	if len(pvc.Metadata.Labels) > 0 {
		properties["labels"] = pvc.Metadata.Labels
	}

	// Add annotations if present
	if len(pvc.Metadata.Annotations) > 0 {
		properties["annotations"] = pvc.Metadata.Annotations
	}

	// Add creation timestamp
	if pvc.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = pvc.Metadata.CreationTimestamp
	}

	// Add subtype property for PVC
	properties["subtype"] = "PersistentVolumeClaim"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypePVC,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypePVC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createNodeFromK8sPV creates a knowledge graph node from a K8s PersistentVolume
func (s *K8sSource) createNodeFromK8sPV(pv *K8sPVFromRelay, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = pv.Metadata.Name
	properties["cluster"] = clusterName
	properties["uid"] = pv.Metadata.UID
	properties["phase"] = pv.Status.Phase
	properties["storage_class"] = pv.Spec.StorageClassName
	properties["volume_mode"] = pv.Spec.VolumeMode
	properties["reclaim_policy"] = pv.Spec.PersistentVolumeReclaimPolicy

	// Add access modes
	if len(pv.Spec.AccessModes) > 0 {
		properties["access_modes"] = pv.Spec.AccessModes
	}

	// Add capacity
	if len(pv.Spec.Capacity) > 0 {
		properties["capacity"] = pv.Spec.Capacity
	}

	// Add cloud-specific volume source information.
	// Order matters: CSI is the modern path (every GKE/EKS PV created in
	// the last ~3 years lives here); legacy in-tree fields are kept for
	// older clusters. See issue #31101 gap #7 for the CSI extraction.
	if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle != "" {
		properties["volume_source"] = "csi"
		properties["csi_driver"] = pv.Spec.CSI.Driver
		properties["csi_volume_handle"] = pv.Spec.CSI.VolumeHandle
		// Extract just the underlying disk name from the volume_handle so
		// cross-account rules can match on a stable short name without
		// each rule needing to know the cloud-specific URL format.
		// Examples:
		//   GKE pd.csi:    projects/<proj>/zones/<zone>/disks/<name>
		//   EKS ebs.csi:   vol-<id>
		//   AKS disk.csi:  /subscriptions/.../disks/<name>
		properties["volume_name"] = extractCSIVolumeName(pv.Spec.CSI.VolumeHandle)
	} else if len(pv.Spec.AWSElasticBlockStore) > 0 {
		properties["volume_source"] = "aws_ebs"
		properties["aws_ebs"] = pv.Spec.AWSElasticBlockStore
	} else if len(pv.Spec.AzureDisk) > 0 {
		properties["volume_source"] = "azure_disk"
		properties["azure_disk"] = pv.Spec.AzureDisk
	} else if len(pv.Spec.GCEPersistentDisk) > 0 {
		properties["volume_source"] = "gce_pd"
		properties["gce_pd"] = pv.Spec.GCEPersistentDisk
	}

	// Add labels if present
	if len(pv.Metadata.Labels) > 0 {
		properties["labels"] = pv.Metadata.Labels
	}

	// Add annotations if present
	if len(pv.Metadata.Annotations) > 0 {
		properties["annotations"] = pv.Metadata.Annotations
	}

	// Add creation timestamp
	if pv.Metadata.CreationTimestamp != "" {
		properties["creation_timestamp"] = pv.Metadata.CreationTimestamp
	}

	// Add status message if present
	if pv.Status.Message != "" {
		properties["status_message"] = pv.Status.Message
	}

	// Add subtype property for PV
	properties["subtype"] = "PersistentVolume"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypePV,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypePV, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// matchServiceToWorkloads finds workloads that match the service selector
func (s *K8sSource) matchServiceToWorkloads(service *K8sServiceFromRelay, workloads []K8sWorkloadRow) []K8sWorkloadRow {
	matched := make([]K8sWorkloadRow, 0)

	// If no selector, no matches
	if len(service.Spec.Selector) == 0 {
		return matched
	}

	for _, workload := range workloads {
		// Only match workloads in the same namespace
		if workload.Namespace != service.Metadata.Namespace {
			continue
		}

		// Extract labels from workload using multiple sources
		workloadLabels := s.extractWorkloadLabels(&workload)

		// Check if all service selector labels match workload labels
		if s.labelsMatch(service.Spec.Selector, workloadLabels) {
			matched = append(matched, workload)
		}
	}

	return matched
}

// extractWorkloadLabels extracts labels from a workload, checking multiple sources
// Priority: 1) dedicated labels column, 2) meta.config.labels, 3) meta.job_data.labels
func (s *K8sSource) extractWorkloadLabels(workload *K8sWorkloadRow) map[string]interface{} {
	workloadLabels := make(map[string]interface{})

	// Priority 1: Try the dedicated labels column first (most reliable)
	if len(workload.Labels) > 0 {
		var labelsMap map[string]interface{}
		if err := json.Unmarshal(workload.Labels, &labelsMap); err == nil && len(labelsMap) > 0 {
			return labelsMap
		}
	}

	// Priority 2 & 3: Parse meta field for labels at different paths
	if len(workload.Meta) > 0 {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(workload.Meta, &metaMap); err != nil {
			return workloadLabels
		}

		// Priority 2: Try meta.config.labels (used by Deployments, DaemonSets, StatefulSets)
		if config, ok := metaMap["config"].(map[string]interface{}); ok {
			if labels, ok := config["labels"].(map[string]interface{}); ok && len(labels) > 0 {
				return labels
			}
		}

		// Priority 3: Try meta.job_data.labels (used by CronJobs, Jobs)
		if jobData, ok := metaMap["job_data"].(map[string]interface{}); ok {
			if labels, ok := jobData["labels"].(map[string]interface{}); ok && len(labels) > 0 {
				return labels
			}
		}
	}

	return workloadLabels
}

// labelsMatch checks if all selector labels match the workload labels
func (s *K8sSource) labelsMatch(selector map[string]interface{}, labels map[string]interface{}) bool {
	if len(selector) == 0 {
		return false
	}

	for key, selectorValue := range selector {
		labelValue, exists := labels[key]
		if !exists {
			return false
		}

		// Convert both to strings for comparison
		selectorStr := fmt.Sprintf("%v", selectorValue)
		labelStr := fmt.Sprintf("%v", labelValue)

		if selectorStr != labelStr {
			return false
		}
	}

	return true
}

// convertK8sNodesToGraph converts K8s nodes to knowledge graph nodes and edges
func (s *K8sSource) convertK8sNodesToGraph(k8sNodes []K8sNodeRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Track cluster nodes
	clusterNodes := make(map[string]*core.DbNode) // cluster_name -> Cluster node

	for _, k8sNode := range k8sNodes {
		// Create node from K8s node data
		nodeGraphNode := s.createNodeFromK8sNode(&k8sNode, req)
		nodes = append(nodes, nodeGraphNode)

		// Create or get cluster node
		if k8sNode.ClusterName != "" {
			var clusterNode *core.DbNode
			if existingCluster, exists := clusterNodes[k8sNode.ClusterName]; exists {
				clusterNode = existingCluster
			} else {
				clusterNode = s.createClusterNode(k8sNode.ClusterName, req)
				clusterNodes[k8sNode.ClusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}

			// Link K8s node to cluster
			edge := core.NewEdge(
				nodeGraphNode.ID,
				clusterNode.ID,
				core.RelationshipRunsOn,
				map[string]interface{}{
					"connection_type": "cluster",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, edge)
		}
	}

	return nodes, edges
}

// createNodeFromK8sNode creates a knowledge graph node from a K8s node
func (s *K8sSource) createNodeFromK8sNode(k8sNode *K8sNodeRow, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = k8sNode.Name
	properties["cluster"] = k8sNode.ClusterName
	properties["is_active"] = k8sNode.IsActive
	properties["node_type"] = k8sNode.NodeType
	properties["node_flavor"] = k8sNode.NodeFlavor
	properties["node_region"] = k8sNode.NodeRegion
	properties["node_zone"] = k8sNode.NodeZone
	properties["memory_capacity"] = s.formatBytesToHumanReadable(k8sNode.MemoryCapacity)
	properties["cpu_capacity"] = k8sNode.CPUCapacity
	properties["memory_allocatable"] = s.formatBytesToHumanReadable(k8sNode.MemoryAllocatable)
	properties["cpu_allocatable"] = k8sNode.CPUAllocatable
	properties["node_creation_time"] = k8sNode.NodeCreationTime
	properties["conditions"] = k8sNode.Conditions

	// Parse and add meta fields
	if len(k8sNode.Meta) > 0 && string(k8sNode.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(k8sNode.Meta, &metaMap); err == nil {
			// properties["meta"] = metaMap

			// Extract node info from meta
			if nodeInfo, ok := metaMap["node_info"].(map[string]interface{}); ok {
				// Extract labels
				if labels, ok := nodeInfo["labels"].(map[string]interface{}); ok {
					properties["labels"] = labels
				}
			}

			// Extract pods running on this node
			if pods, ok := metaMap["pods"].(string); ok && pods != "" {
				// Split comma-separated pod names
				podList := strings.Split(pods, ",")
				properties["pods"] = podList
				properties["pod_count"] = len(podList)
			}

			// Extract taints
			if taints, ok := metaMap["taints"].(string); ok && taints != "" {
				properties["taints"] = taints
			}

			// Extract spec.providerID
			if spec, ok := metaMap["spec"].(map[string]interface{}); ok {
				if providerID, ok := spec["providerID"].(string); ok && providerID != "" {
					// Add spec as a nested object with providerID
					properties["providerID"] = providerID
				}
			}

			// Extract InternalIP for flow-source IP resolvers. The k8s-collector
			// serializes Node.Status.Addresses as a flat []string under
			// meta.node_info.addresses, dropping the (type,address) shape that
			// the K8s API uses, so we have to recover the InternalIP via
			// a cloud-provider annotation (most reliable, present on EKS/GKE)
			// or fall back to the first RFC1918 entry.
			if ni, ok := metaMap["node_info"].(map[string]interface{}); ok {
				// Preferred path: the AWS/EKS node-IP annotation.
				if ann, ok := ni["annotations"].(map[string]interface{}); ok {
					if ip, _ := ann["alpha.kubernetes.io/provided-node-ip"].(string); ip != "" {
						properties["internal_ip"] = ip
					}
				}
				// Fallback: first RFC1918 IPv4 in the addresses list.
				if _, has := properties["internal_ip"]; !has {
					if addrs, ok := ni["addresses"].([]interface{}); ok {
						for _, a := range addrs {
							ip, ok := a.(string)
							if !ok || ip == "" {
								continue
							}
							if isRFC1918IPv4(ip) {
								properties["internal_ip"] = ip
								break
							}
						}
					}
				}
			}
		}
	}

	// Add subtype property for K8s node
	if nodeType, ok := properties["node_type"].(string); ok {
		properties["subtype"] = nodeType
	} else {
		properties["subtype"] = "Node"
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNode,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeNode, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// convertWorkloadsToGraph converts K8s workloads to knowledge graph nodes and edges
func (s *K8sSource) convertWorkloadsToGraph(workloads []K8sWorkloadRow, k8sNodeMap *map[string]*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode, map[string]*core.DbNode) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Track infrastructure nodes
	clusterNodes := make(map[string]*core.DbNode)    // cluster_name -> Cluster node
	namespaceNodes := make(map[string]*core.DbNode)  // namespace -> Namespace node
	workloadToNode := make(map[string]*core.DbNode)  // workload key -> workload node
	repositoryNodes := make(map[string]*core.DbNode) // repo_url -> Repository node

	// Initialize clusterNodes from k8sNodeMap
	for key, k8sNode := range *k8sNodeMap {
		if k8sNode.NodeType == core.NodeTypeCluster {
			clusterName, _ := core.GetNodePropertyString(k8sNode, "name")
			clusterNodes[clusterName] = k8sNode
			delete(*k8sNodeMap, key)
		}
	}

	// First pass: Create all workload nodes
	for _, workload := range workloads {
		workloadNode := s.createNodeFromWorkload(&workload, req)
		nodes = append(nodes, workloadNode)

		// Track workload node - include cluster to avoid collisions
		workloadKey := fmt.Sprintf("%s/%s/%s/%s", workload.ClusterName, workload.Kind, workload.Namespace, workload.Name)
		workloadToNode[workloadKey] = workloadNode

		// Track cluster node
		if workload.ClusterName != "" {
			if _, exists := clusterNodes[workload.ClusterName]; !exists {
				clusterNode := s.createClusterNode(workload.ClusterName, req)
				clusterNodes[workload.ClusterName] = clusterNode
				nodes = append(nodes, clusterNode)
			}
		}

		// Track namespace node
		namespaceKey := fmt.Sprintf("%s/%s", workload.ClusterName, workload.Namespace)
		if _, exists := namespaceNodes[namespaceKey]; !exists {
			namespaceNode := s.createNamespaceNode(workload.Namespace, workload.ClusterName, req)
			namespaceNodes[namespaceKey] = namespaceNode
			nodes = append(nodes, namespaceNode)
		}
	}

	// Track Helm chart nodes
	helmChartNodes := make(map[string]*core.DbNode) // chart_key -> HelmChart node

	// Second pass: Create relationships
	for _, workload := range workloads {
		workloadKey := fmt.Sprintf("%s/%s/%s/%s", workload.ClusterName, workload.Kind, workload.Namespace, workload.Name)
		workloadNode := workloadToNode[workloadKey]

		// Check for git repository annotations and create repository connection
		if len(workload.Meta) > 0 {
			var metaMap map[string]interface{}
			if err := json.Unmarshal(workload.Meta, &metaMap); err == nil {
				s.createRepositoryConnection(&workload, workloadNode, metaMap, &repositoryNodes, &nodes, &edges, req)

				// Check for Helm labels and create Helm chart nodes if workload is Helm-managed
				s.createHelmConnection(&workload, workloadNode, metaMap, &helmChartNodes, &repositoryNodes, &nodes, &edges, req)
			}
		}

		// Link workload to namespace
		namespaceKey := fmt.Sprintf("%s/%s", workload.ClusterName, workload.Namespace)
		if namespaceNode, exists := namespaceNodes[namespaceKey]; exists {
			edge := core.NewEdge(
				workloadNode.ID,
				namespaceNode.ID,
				core.RelationshipRunsOn,
				map[string]interface{}{
					"connection_type": "namespace",
				},
				req.TenantID,
				req.CloudAccountID,
				"k8s",
			)
			edges = append(edges, edge)
		}

		// Link namespace to cluster
		if clusterNode, exists := clusterNodes[workload.ClusterName]; exists {
			namespaceKey := fmt.Sprintf("%s/%s", workload.ClusterName, workload.Namespace)
			if namespaceNode, exists := namespaceNodes[namespaceKey]; exists {
				// Check if edge already exists
				edgeExists := false
				for _, e := range edges {
					if e.SourceNodeID == namespaceNode.ID && e.DestinationNodeID == clusterNode.ID {
						edgeExists = true
						break
					}
				}

				if !edgeExists {
					edge := core.NewEdge(
						namespaceNode.ID,
						clusterNode.ID,
						core.RelationshipRunsOn,
						map[string]interface{}{
							"connection_type": "cluster",
						},
						req.TenantID,
						req.CloudAccountID,
						"k8s",
					)
					edges = append(edges, edge)
				}
			}
		}

		// Parse meta to find relationships (e.g., Pod -> Node)
		if len(workload.Meta) > 0 {
			var metaMap map[string]interface{}
			if err := json.Unmarshal(workload.Meta, &metaMap); err == nil {
				// Extract node name for Pod
				if workload.Kind == "Pod" {
					if spec, ok := metaMap["spec"].(map[string]interface{}); ok {
						if nodeName, ok := spec["nodeName"].(string); ok && nodeName != "" {
							var k8sNode *core.DbNode

							// First, check if node exists in k8sNodeMap (from k8s_nodes table)
							if existingNode, exists := (*k8sNodeMap)[nodeName]; exists {
								k8sNode = existingNode
							} else {
								// Check if we already created a minimal node for this
								nodeKey := fmt.Sprintf("Node/%s", nodeName)
								if existingNode, exists := workloadToNode[nodeKey]; exists {
									k8sNode = existingNode
								} else {
									// Create a minimal node resource as fallback
									k8sNode = s.createK8sNodeResource(nodeName, workload.ClusterName, req)
									nodes = append(nodes, k8sNode)
									workloadToNode[nodeKey] = k8sNode

									// Link Node to Cluster
									if clusterNode, exists := clusterNodes[workload.ClusterName]; exists {
										edge := core.NewEdge(
											k8sNode.ID,
											clusterNode.ID,
											core.RelationshipRunsOn,
											map[string]interface{}{
												"connection_type": "cluster",
											},
											req.TenantID,
											req.CloudAccountID,
											"k8s",
										)
										edges = append(edges, edge)
									}
								}
							}

							// Link Pod to Node
							edge := core.NewEdge(
								workloadNode.ID,
								k8sNode.ID,
								core.RelationshipRunsOn,
								map[string]interface{}{
									"connection_type": "node",
								},
								req.TenantID,
								req.CloudAccountID,
								"k8s",
							)
							edges = append(edges, edge)
						}
					}
				}
			}
		}
	}

	return nodes, edges, clusterNodes, namespaceNodes, workloadToNode
}

// createNodeFromWorkload creates a knowledge graph node from a K8s workload
func (s *K8sSource) createNodeFromWorkload(workload *K8sWorkloadRow, req *core.SourceBuildRequest) *core.DbNode {
	// Determine node type based on workload kind
	var nodeType core.NodeType
	kind := strings.ToLower(workload.Kind)

	switch kind {
	case "pod":
		nodeType = core.NodeTypePod
	case "service":
		nodeType = core.NodeTypeK8sService
	default:
		// For Deployments, StatefulSets, DaemonSets, etc., use Workload type
		nodeType = core.NodeTypeWorkload
	}

	// Build unique key

	// Build properties
	properties := make(map[string]interface{})
	properties["name"] = workload.Name
	properties["kind"] = workload.Kind
	properties["namespace"] = workload.Namespace
	properties["cluster"] = workload.ClusterName
	properties["is_active"] = workload.IsActive
	properties["k8s_workload_id"] = workload.ID
	properties["nb_resource_id"] = workload.ID

	// Event-derived last deploy time (maintained by the
	// fn_k8s_workloads_deploy_time_trigger Postgres trigger). Distinct from
	// the K8s-conditions-derived last_status_update_time below.
	if workload.LastDeployedTime.Valid {
		properties["last_deployed_time"] = workload.LastDeployedTime.Time.Format(time.RFC3339)
	}

	// Parse and add meta fields
	if len(workload.Meta) > 0 && string(workload.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(workload.Meta, &metaMap); err == nil {
			// properties["meta"] = metaMap

			// Extract creation and deployment timestamps from Kubernetes metadata
			s.extractK8sTimestamps(properties, metaMap)

			// Extract commonly used fields
			s.extractK8sMetadata(properties, metaMap, workload.Kind)
		}
	}

	// Add subtype property for K8s workload
	properties["subtype"] = workload.Kind

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// extractK8sTimestamps extracts creation and deployment timestamps from Kubernetes metadata
func (s *K8sSource) extractK8sTimestamps(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract deployment revision from annotations
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if annotations, ok := config["annotations"].(map[string]interface{}); ok {
			if revision, ok := annotations["deployment.kubernetes.io/revision"].(string); ok && revision != "" {
				properties["deployment_revision"] = revision
			}
		}
	}

	// Extract timestamps from status_info.conditions
	if statusInfo, ok := metaMap["status_info"].(map[string]interface{}); ok {
		// Get observed generation
		if observedGeneration, ok := statusInfo["observedGeneration"].(float64); ok {
			properties["observed_generation"] = int(observedGeneration)
		}

		// Extract most recent timestamps from conditions
		if conditions, ok := statusInfo["conditions"].([]interface{}); ok && len(conditions) > 0 {
			var createdAt, lastDeployedTime string

			for _, cond := range conditions {
				if condMap, ok := cond.(map[string]interface{}); ok {
					// lastTransitionTime represents when the condition first occurred (creation/initial deployment)
					if lastTransitionTime, ok := condMap["lastTransitionTime"].(string); ok && lastTransitionTime != "" {
						if createdAt == "" || lastTransitionTime < createdAt {
							createdAt = lastTransitionTime
						}
					}

					// lastUpdateTime represents the most recent update to this condition (latest deployment)
					if lastUpdateTime, ok := condMap["lastUpdateTime"].(string); ok && lastUpdateTime != "" {
						if lastDeployedTime == "" || lastUpdateTime > lastDeployedTime {
							lastDeployedTime = lastUpdateTime
						}
					}
				}
			}

			// Set the timestamps. last_status_update_time reflects the most
			// recent K8s condition update (any condition), distinct from
			// last_deployed_time which is sourced from configuration_change
			// events and set above in createNodeFromWorkload.
			if createdAt != "" {
				properties["created_at"] = createdAt
			}
			if lastDeployedTime != "" {
				properties["last_status_update_time"] = lastDeployedTime
			}

			// If we only have one of them, use it for both
			if createdAt == "" && lastDeployedTime != "" {
				properties["created_at"] = lastDeployedTime
			}
			if lastDeployedTime == "" && createdAt != "" {
				properties["last_status_update_time"] = createdAt
			}
		}
	}
}

// extractK8sMetadata extracts only essential K8s metadata fields based on workload kind
// This prevents storing large metadata blobs and keeps only what's needed
func (s *K8sSource) extractK8sMetadata(properties map[string]interface{}, metaMap map[string]interface{}, kind string) {
	// Extract common fields for all workloads
	s.extractCommonK8sFields(properties, metaMap)

	// Extract kind-specific fields
	s.extractKindSpecificFields(properties, metaMap, kind)

	// Extract resource usage (CPU/memory) - important for capacity planning
	s.extractResourceUsage(properties, metaMap)

	// Extract status information
	s.extractK8sStatus(properties, metaMap)
}

// extractCommonK8sFields extracts fields common to all K8s workloads
func (s *K8sSource) extractCommonK8sFields(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract labels (important for selectors and filtering) - try both config and metadata
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if labels, ok := config["labels"].(map[string]interface{}); ok {
			properties["labels"] = labels
		}
		if annotations, ok := config["annotations"].(map[string]interface{}); ok {
			properties["annotations"] = annotations
		}
	}

	// Fallback to metadata if not found in config
	if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
		if _, hasLabels := properties["labels"]; !hasLabels {
			if labels, ok := metadata["labels"].(map[string]interface{}); ok {
				properties["labels"] = labels
			}
		}
		if _, hasAnnotations := properties["annotations"]; !hasAnnotations {
			if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
				properties["annotations"] = annotations
			}
		}
	}
}

// extractServiceAccountName resolves the serviceAccountName for a workload from
// the k8s collector's flattened meta shape. The collector hoists this field to
// `meta.config.service_account` for every workload Kind (Pod / Deployment /
// StatefulSet / DaemonSet / ReplicaSet / Job / CronJob), so we read it from one
// place regardless of Kind. Falls back to the native K8s API paths
// (`spec.template.spec.serviceAccountName`, `spec.serviceAccountName`) in case
// extractCSIVolumeName returns the underlying disk/volume name from a CSI
// `spec.csi.volume_handle`. The whole handle is also kept on the node as
// `csi_volume_handle`; this helper produces a stable short identifier that
// matches the cloud-side resource's `name` for cross-account rules.
//
// Examples:
//
//	"projects/p/zones/z/disks/pvc-abc"  -> "pvc-abc"   (GKE pd.csi.storage.gke.io)
//	"vol-0123456789abcdef0"             -> "vol-0123…" (EKS ebs.csi.aws.com)
//	"/subscriptions/…/disks/disk-name"  -> "disk-name" (AKS disk.csi.azure.com)
//	"pvc-abc"                           -> "pvc-abc"   (no path separator)
func extractCSIVolumeName(volumeHandle string) string {
	if volumeHandle == "" {
		return ""
	}
	if idx := strings.LastIndex(volumeHandle, "/"); idx >= 0 && idx < len(volumeHandle)-1 {
		return volumeHandle[idx+1:]
	}
	return volumeHandle
}

// a different collector version uses the raw shape.
//
// Returns "" when no SA can be resolved — in that case
// createWorkloadServiceAccountEdges skips the Workload→SA edge.
func extractServiceAccountName(metaMap map[string]interface{}, kind string) string {
	if metaMap == nil {
		return ""
	}
	// Preferred path: collector-flattened `config.service_account`.
	if cfg, ok := metaMap["config"].(map[string]interface{}); ok {
		if san, ok := cfg["service_account"].(string); ok && san != "" {
			return san
		}
	}
	// Fallback paths: raw K8s API shape, in case collector format changes.
	spec, _ := metaMap["spec"].(map[string]interface{})
	if spec == nil {
		return ""
	}
	var podSpec map[string]interface{}
	switch kind {
	case "Pod":
		podSpec = spec
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		if tmpl, ok := spec["template"].(map[string]interface{}); ok {
			podSpec, _ = tmpl["spec"].(map[string]interface{})
		}
	case "CronJob":
		if jt, ok := spec["jobTemplate"].(map[string]interface{}); ok {
			if jtSpec, ok := jt["spec"].(map[string]interface{}); ok {
				if tmpl, ok := jtSpec["template"].(map[string]interface{}); ok {
					podSpec, _ = tmpl["spec"].(map[string]interface{})
				}
			}
		}
	}
	if podSpec == nil {
		return ""
	}
	if san, ok := podSpec["serviceAccountName"].(string); ok && san != "" {
		return san
	}
	if san, ok := podSpec["serviceAccount"].(string); ok && san != "" {
		return san
	}
	return ""
}

// extractKindSpecificFields extracts essential fields based on workload kind
func (s *K8sSource) extractKindSpecificFields(properties map[string]interface{}, metaMap map[string]interface{}, kind string) {
	// serviceAccountName is read from the collector-flattened path
	// (meta.config.service_account) regardless of Kind, so resolve it before
	// the per-Kind switch.
	if san := extractServiceAccountName(metaMap, kind); san != "" {
		properties["service_account_name"] = san
	}

	spec, hasSpec := metaMap["spec"].(map[string]interface{})
	if !hasSpec {
		return
	}

	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		// Replica count (important for scaling)
		if replicas, ok := spec["replicas"].(float64); ok {
			properties["replicas"] = int(replicas)
		}
		// Strategy (important for deployments)
		if strategy, ok := spec["strategy"].(map[string]interface{}); ok {
			if strategyType, ok := strategy["type"].(string); ok {
				properties["strategy_type"] = strategyType
			}
		}

	case "Pod":
		// Node assignment (important for scheduling)
		if nodeName, ok := spec["nodeName"].(string); ok {
			properties["node_name"] = nodeName
		}
		// Restart policy
		if restartPolicy, ok := spec["restartPolicy"].(string); ok {
			properties["restart_policy"] = restartPolicy
		}

	case "Service", "K8sService":
		// Service type and IPs (critical for networking)
		if clusterIP, ok := spec["clusterIP"].(string); ok {
			properties["cluster_ip"] = clusterIP
		}
		if serviceType, ok := spec["type"].(string); ok {
			properties["service_type"] = serviceType
		}
		// Ports (important for connectivity)
		if ports, ok := spec["ports"].([]interface{}); ok && len(ports) > 0 {
			properties["service_ports"] = ports
		}

	case "Ingress":
		// Ingress class (important for routing)
		if ingressClass, ok := spec["ingressClassName"].(string); ok {
			properties["ingress_class"] = ingressClass
		}

		// Extract load balancer hostname from status (for connecting to AWS ELB)
		if status, ok := metaMap["status"].(map[string]interface{}); ok {
			if loadBalancer, ok := status["loadBalancer"].(map[string]interface{}); ok {
				if ingresses, ok := loadBalancer["ingress"].([]interface{}); ok && len(ingresses) > 0 {
					if firstIngress, ok := ingresses[0].(map[string]interface{}); ok {
						// AWS ELBs use "hostname" field
						if hostname, ok := firstIngress["hostname"].(string); ok && hostname != "" {
							properties["load_balancer_hostname"] = hostname
						}
						// Some ingress controllers use "ip" field instead
						if ip, ok := firstIngress["ip"].(string); ok && ip != "" {
							properties["load_balancer_ip"] = ip
						}
					}
				}
			}
		}

		// Extract ingress rules (hosts and paths)
		if rules, ok := spec["rules"].([]interface{}); ok && len(rules) > 0 {
			hosts := make([]string, 0)
			for _, rule := range rules {
				if ruleMap, ok := rule.(map[string]interface{}); ok {
					if host, ok := ruleMap["host"].(string); ok && host != "" {
						hosts = append(hosts, host)
					}
				}
			}
			if len(hosts) > 0 {
				properties["ingress_hosts"] = hosts
			}
		}

	case "PersistentVolumeClaim":
		// Storage request (important for capacity)
		if resources, ok := spec["resources"].(map[string]interface{}); ok {
			if requests, ok := resources["requests"].(map[string]interface{}); ok {
				if storage, ok := requests["storage"].(string); ok {
					properties["storage_request"] = storage
				}
			}
		}
		// Storage class (important for provisioning)
		if storageClass, ok := spec["storageClassName"].(string); ok {
			properties["storage_class"] = storageClass
		}

	case "ConfigMap", "Secret":
		// Just count of keys (don't store actual data)
		if data, ok := spec["data"].(map[string]interface{}); ok {
			properties["key_count"] = len(data)
		}
	}
}

// extractResourceUsage extracts CPU and memory usage/limits (aggregated, not per-container)
func (s *K8sSource) extractResourceUsage(properties map[string]interface{}, metaMap map[string]interface{}) {
	var totalCPURequests, totalMemoryRequests, totalCPULimits, totalMemoryLimits float64
	var containerNames []string
	var containerImages []string
	var primaryImage string

	// Try config first
	var containers []interface{}
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if containersList, ok := config["containers"].([]interface{}); ok {
			containers = containersList
		}
	}

	// Process containers - only extract aggregated resources, not full container details
	if len(containers) > 0 {
		for i, container := range containers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				// Collect container names only
				if name, ok := containerMap["name"].(string); ok {
					containerNames = append(containerNames, name)
				}

				// Collect all container images (needed for ECR relationship matching)
				if image, ok := containerMap["image"].(string); ok && image != "" {
					containerImages = append(containerImages, image)
					// Get first container's image as primary
					if i == 0 {
						primaryImage = image
					}
				}

				// Aggregate resource requests/limits
				if resources, ok := containerMap["resources"].(map[string]interface{}); ok {
					if requests, ok := resources["requests"].(map[string]interface{}); ok {
						if cpu, ok := requests["cpu"].(string); ok {
							totalCPURequests += s.parseResourceValue(cpu)
						}
						if memory, ok := requests["memory"].(string); ok {
							totalMemoryRequests += s.parseResourceValue(memory)
						}
					}
					if limits, ok := resources["limits"].(map[string]interface{}); ok {
						if cpu, ok := limits["cpu"].(string); ok {
							totalCPULimits += s.parseResourceValue(cpu)
						}
						if memory, ok := limits["memory"].(string); ok {
							totalMemoryLimits += s.parseResourceValue(memory)
						}
					}
				}
			}
		}

		// Store only aggregated data, not full container objects
		properties["container_count"] = len(containers)
		if len(containerNames) > 0 {
			properties["container_names"] = containerNames
		}
		if primaryImage != "" {
			properties["primary_image"] = primaryImage
		}
		// Store all container images for ECR relationship matching
		if len(containerImages) > 0 {
			properties["container_images"] = containerImages
		}

		// Add aggregated resource totals
		if totalCPURequests > 0 {
			properties["total_cpu_requests"] = totalCPURequests
		}
		if totalMemoryRequests > 0 {
			properties["total_memory_requests"] = s.formatBytesToHumanReadable(totalMemoryRequests)
		}
		if totalCPULimits > 0 {
			properties["total_cpu_limits"] = totalCPULimits
		}
		if totalMemoryLimits > 0 {
			properties["total_memory_limits"] = s.formatBytesToHumanReadable(totalMemoryLimits)
		}
	}
}

// extractK8sStatus extracts essential status information
func (s *K8sSource) extractK8sStatus(properties map[string]interface{}, metaMap map[string]interface{}) {
	status, hasStatus := metaMap["status"].(map[string]interface{})
	if !hasStatus {
		return
	}

	// Phase (important for operational state)
	if phase, ok := status["phase"].(string); ok && phase != "" {
		properties["phase"] = phase
	}

	// Ready status (important for health checks) - extract from conditions, not store all conditions
	if conditions, ok := status["conditions"].([]interface{}); ok && len(conditions) > 0 {
		for _, cond := range conditions {
			if condMap, ok := cond.(map[string]interface{}); ok {
				if condType, ok := condMap["type"].(string); ok && condType == "Ready" {
					if condStatus, ok := condMap["status"].(string); ok {
						properties["ready"] = (condStatus == "True")
					}
				}
			}
		}
	}

	// Available replicas (important for deployment health)
	if availableReplicas, ok := status["availableReplicas"].(float64); ok {
		properties["available_replicas"] = int(availableReplicas)
	}
	if readyReplicas, ok := status["readyReplicas"].(float64); ok {
		properties["ready_replicas"] = int(readyReplicas)
	}
}

// createClusterNode creates a Cluster node
func (s *K8sSource) createClusterNode(clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":    clusterName,
		"subtype": "Cluster",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeCluster,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeCluster, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createNamespaceNode creates a Namespace node
func (s *K8sSource) createNamespaceNode(namespace, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":    namespace,
		"cluster": clusterName,
		"subtype": "Namespace",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNamespace,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeNamespace, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createK8sNodeResource creates a DbNode (K8s infrastructure node) resource
func (s *K8sSource) createK8sNodeResource(nodeName, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":    nodeName,
		"cluster": clusterName,
		"subtype": "Node",
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNode,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeNode, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// enrichK8sNodesWithCloudAttributes enriches cluster and K8s node resources with cloud account attributes
func (s *K8sSource) enrichK8sNodesWithCloudAttributes(reqCtx *security.RequestContext, cloudAccountID string, nodes []*core.DbNode) error {
	// Fetch cloud account attributes
	attributes, err := GetCloudAccountAttributes(reqCtx, cloudAccountID)
	if err != nil {
		return fmt.Errorf("failed to get cloud account attributes: %w", err)
	}

	// If no attributes found, nothing to enrich
	if len(attributes) == 0 {
		s.logger.Debug("no cloud account attributes found for enrichment")
		return nil
	}

	s.logger.Info("enriching K8s nodes with cloud account attributes",
		"cloud_account_id", cloudAccountID,
		"attributes_count", len(attributes))

	// Enrich cluster and K8s node resources with attributes
	enrichedCount := 0
	for _, node := range nodes {
		if node.NodeType == core.NodeTypeCluster || node.NodeType == core.NodeTypeNode {
			if node.Properties == nil {
				node.Properties = make(map[string]interface{})
			}

			// Add each attribute to the node properties
			for attrName, attrValue := range attributes {
				if attrValue != "" {
					node.Properties[attrName] = attrValue
				}
			}
			enrichedCount++
		}
	}

	s.logger.Info("enriched K8s nodes with cloud account attributes",
		"enriched_count", enrichedCount,
		"total_nodes", len(nodes))

	return nil
}

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

// parseCPUValue parses Kubernetes CPU resource values into cores
// Examples: "500m" -> 0.5, "2" -> 2.0, "1000m" -> 1.0
func (s *K8sSource) parseCPUValue(value string) float64 {
	if value == "" {
		return 0
	}

	// Handle millicores (e.g., "500m")
	if strings.HasSuffix(value, "m") {
		cpuStr := strings.TrimSuffix(value, "m")
		var cpuVal float64
		if _, err := fmt.Sscanf(cpuStr, "%f", &cpuVal); err == nil {
			return cpuVal / 1000.0 // Convert millicores to cores
		}
	}

	// Handle plain number (cores, e.g., "2")
	var cpuVal float64
	if _, err := fmt.Sscanf(value, "%f", &cpuVal); err == nil {
		return cpuVal
	}

	return 0
}

// parseMemoryValue parses Kubernetes memory resource values into bytes
// Examples: "512Mi" -> 536870912, "1Gi" -> 1073741824, "100M" -> 100000000
func (s *K8sSource) parseMemoryValue(value string) float64 {
	if value == "" {
		return 0
	}

	var memVal float64
	var unit string

	// Try to parse with unit
	if n, err := fmt.Sscanf(value, "%f%s", &memVal, &unit); err == nil && n == 2 {
		switch unit {
		case "Ki":
			return memVal * 1024
		case "Mi":
			return memVal * 1024 * 1024
		case "Gi":
			return memVal * 1024 * 1024 * 1024
		case "Ti":
			return memVal * 1024 * 1024 * 1024 * 1024
		case "Pi":
			return memVal * 1024 * 1024 * 1024 * 1024 * 1024
		case "K", "k":
			return memVal * 1000
		case "M":
			return memVal * 1000 * 1000
		case "G":
			return memVal * 1000 * 1000 * 1000
		case "T":
			return memVal * 1000 * 1000 * 1000 * 1000
		case "P":
			return memVal * 1000 * 1000 * 1000 * 1000 * 1000
		}
	}

	// Try parsing as plain number (bytes)
	if _, err := fmt.Sscanf(value, "%f", &memVal); err == nil {
		return memVal
	}

	return 0
}

// isRFC1918IPv4 reports whether the given dotted-quad string is a private
// IPv4 address (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16). Used to recover
// a Node's InternalIP from the collector's flat addresses list when the
// (type,address) shape is no longer available.
func isRFC1918IPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	if v4 == nil {
		return false
	}
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}

// formatBytesToHumanReadable converts bytes to human-readable format using binary units (Ki, Mi, Gi, Ti)
// Examples: 536870912 -> "512Mi", 1073741824 -> "1Gi", 1536 -> "1.5Ki"
func (s *K8sSource) formatBytesToHumanReadable(bytes float64) string {
	if bytes == 0 {
		return "0"
	}

	const (
		Ki = 1024
		Mi = 1024 * 1024
		Gi = 1024 * 1024 * 1024
		Ti = 1024 * 1024 * 1024 * 1024
		Pi = 1024 * 1024 * 1024 * 1024 * 1024
	)

	// Select appropriate unit
	switch {
	case bytes >= Pi:
		return fmt.Sprintf("%.2fPi", bytes/Pi)
	case bytes >= Ti:
		return fmt.Sprintf("%.2fTi", bytes/Ti)
	case bytes >= Gi:
		return fmt.Sprintf("%.2fGi", bytes/Gi)
	case bytes >= Mi:
		return fmt.Sprintf("%.2fMi", bytes/Mi)
	case bytes >= Ki:
		return fmt.Sprintf("%.2fKi", bytes/Ki)
	default:
		return fmt.Sprintf("%.0f", bytes)
	}
}

// parseResourceValue determines resource type and parses accordingly
// CPU values (with 'm' suffix or small numbers) -> cores
// Memory values (with size units) -> bytes
func (s *K8sSource) parseResourceValue(value string) float64 {
	if value == "" {
		return 0
	}

	// CPU resource (millicores or cores)
	if strings.HasSuffix(value, "m") {
		return s.parseCPUValue(value)
	}

	// Memory resource (has unit suffix)
	if strings.ContainsAny(value, "KMGTPikKMGTP") {
		return s.parseMemoryValue(value)
	}

	// Plain number - assume CPU cores
	var numVal float64
	if _, err := fmt.Sscanf(value, "%f", &numVal); err == nil {
		return numVal
	}

	return 0
}

// createRepositoryConnection checks for git repository annotations and creates a repository node connection
func (s *K8sSource) createRepositoryConnection(
	workload *K8sWorkloadRow,
	workloadNode *core.DbNode,
	metaMap map[string]interface{},
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract annotations from metadata
	var annotations map[string]interface{}

	// Try to get annotations from config.annotations first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if ann, ok := config["annotations"].(map[string]interface{}); ok {
			annotations = ann
		}
	}

	// If not found, try metadata.annotations
	if annotations == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if ann, ok := metadata["annotations"].(map[string]interface{}); ok {
				annotations = ann
			}
		}
	}

	if annotations == nil {
		return
	}

	// Check for required git annotations
	gitHash, hasHash := annotations[annotationkeys.CIGitHash].(string)
	gitRepo, hasRepo := annotations[annotationkeys.CIGitRepo].(string)
	helmValuesPath, hasPath := annotations[annotationkeys.CIHelmValuesPath].(string)

	// All three annotations must be present
	if !hasHash || !hasRepo || !hasPath || gitHash == "" || gitRepo == "" || helmValuesPath == "" {
		return
	}

	s.logger.Info("found git repository annotations for workload",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"git_repo", gitRepo,
		"git_hash", gitHash,
		"helm_values_path", helmValuesPath)

	// Create or get repository node
	var repoNode *core.DbNode
	if existingRepo, exists := (*repositoryNodes)[gitRepo]; exists {
		repoNode = existingRepo
	} else {
		repoNode = s.createRepositoryNode(gitRepo, req)
		(*repositoryNodes)[gitRepo] = repoNode
		*nodes = append(*nodes, repoNode)
	}

	// Create edge from workload to repository
	edge := core.NewEdge(
		workloadNode.ID,
		repoNode.ID,
		core.RelationshipBuiltFrom,
		map[string]interface{}{
			"git_hash":         gitHash,
			"helm_values_path": helmValuesPath,
			"connection_type":  "repository",
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	s.logger.Info("created repository connection",
		"workload", workload.Name,
		"repository", gitRepo)
}

// createRepositoryNode creates a Repository node
func (s *K8sSource) createRepositoryNode(repoURL string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"url":  repoURL,
		"name": s.extractRepoName(repoURL),
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeRepository,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeRepository, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// extractRepoName extracts the repository name from a git URL
// Examples:
//   - https://github.com/org/repo.git -> repo
//   - git@github.com:org/repo.git -> repo
//   - https://github.com/org/repo -> repo
func (s *K8sSource) extractRepoName(repoURL string) string {
	// Remove .git suffix if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Split by / to get the last part
	parts := strings.Split(repoURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return repoURL
}

// createHelmConnection checks for Helm labels and creates Helm chart nodes if workload is Helm-managed
func (s *K8sSource) createHelmConnection(
	workload *K8sWorkloadRow,
	workloadNode *core.DbNode,
	metaMap map[string]interface{},
	helmChartNodes *map[string]*core.DbNode,
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract labels from metadata
	var labels map[string]interface{}

	// Try to get labels from config.labels first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if lbl, ok := config["labels"].(map[string]interface{}); ok {
			labels = lbl
		}
	}

	// If not found, try metadata.labels
	if labels == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if lbl, ok := metadata["labels"].(map[string]interface{}); ok {
				labels = lbl
			}
		}
	}

	if labels == nil {
		return
	}

	// Check if workload is managed by Helm
	managedBy, hasManagedBy := labels["app.kubernetes.io/managed-by"].(string)
	if !hasManagedBy || managedBy != "Helm" {
		return
	}

	// Extract Helm metadata from labels
	releaseName, _ := labels["app.kubernetes.io/instance"].(string)
	chartName, hasChartName := labels["helm.sh/chart"].(string)

	// We need at least the chart name to create Helm nodes
	if !hasChartName || chartName == "" {
		return
	}

	s.logger.Info("found Helm-managed workload",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"release", releaseName,
		"chart", chartName)

	// Extract chart name and version from the chart label (format: chart-name-version)
	parsedChartName, chartVersion := s.parseHelmChartLabel(chartName)

	// Create or get HelmChart node and link workload directly to chart
	chartKey := fmt.Sprintf("%s/%s", parsedChartName, chartVersion)
	var helmChartNode *core.DbNode
	if existingChart, exists := (*helmChartNodes)[chartKey]; exists {
		helmChartNode = existingChart
	} else {
		helmChartNode = s.createHelmChartNode(parsedChartName, chartVersion, releaseName, req)
		(*helmChartNodes)[chartKey] = helmChartNode
		*nodes = append(*nodes, helmChartNode)

		s.logger.Info("created Helm chart node",
			"chart", parsedChartName,
			"version", chartVersion)
	}

	// Create edge from workload to Helm chart (workload is configured by chart)
	edge := core.NewEdge(
		workloadNode.ID,
		helmChartNode.ID,
		core.RelationshipIsConfiguredBy,
		map[string]interface{}{
			"connection_type": "helm_chart",
			"chart_name":      parsedChartName,
			"chart_version":   chartVersion,
			"release_name":    releaseName,
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	// Check for git repository annotations and connect Helm chart to repository
	s.connectHelmChartToRepository(workload, helmChartNode, metaMap, repositoryNodes, nodes, edges, req)
}

// connectHelmChartToRepository checks for git repository annotations and creates a connection
// between HelmChart and Repository nodes when helm.values.filePath is present
func (s *K8sSource) connectHelmChartToRepository(
	workload *K8sWorkloadRow,
	helmChartNode *core.DbNode,
	metaMap map[string]interface{},
	repositoryNodes *map[string]*core.DbNode,
	nodes *[]*core.DbNode,
	edges *[]*core.DbEdge,
	req *core.SourceBuildRequest,
) {
	// Extract annotations from metadata
	var annotations map[string]interface{}

	// Try to get annotations from config.annotations first
	if config, ok := metaMap["config"].(map[string]interface{}); ok {
		if ann, ok := config["annotations"].(map[string]interface{}); ok {
			annotations = ann
		}
	}

	// If not found, try metadata.annotations
	if annotations == nil {
		if metadata, ok := metaMap["metadata"].(map[string]interface{}); ok {
			if ann, ok := metadata["annotations"].(map[string]interface{}); ok {
				annotations = ann
			}
		}
	}

	if annotations == nil {
		return
	}

	// Check for required git annotations
	gitHash, hasHash := annotations[annotationkeys.CIGitHash].(string)
	gitRepo, hasRepo := annotations[annotationkeys.CIGitRepo].(string)
	helmValuesPath, hasPath := annotations[annotationkeys.CIHelmValuesPath].(string)

	// All three annotations must be present to create the connection
	if !hasHash || !hasRepo || !hasPath || gitHash == "" || gitRepo == "" || helmValuesPath == "" {
		return
	}

	s.logger.Info("found Helm values repository connection",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"git_repo", gitRepo,
		"git_hash", gitHash,
		"helm_values_path", helmValuesPath)

	// Create or get repository node
	var repoNode *core.DbNode
	if existingRepo, exists := (*repositoryNodes)[gitRepo]; exists {
		repoNode = existingRepo
	} else {
		repoNode = s.createRepositoryNode(gitRepo, req)
		(*repositoryNodes)[gitRepo] = repoNode
		*nodes = append(*nodes, repoNode)

		s.logger.Info("created repository node for Helm chart",
			"repository", gitRepo)
	}

	// Create edge from Helm chart to repository
	edge := core.NewEdge(
		helmChartNode.ID,
		repoNode.ID,
		core.RelationshipIsConfiguredBy,
		map[string]interface{}{
			"connection_type":  "helm_values",
			"git_hash":         gitHash,
			"helm_values_path": helmValuesPath,
			"values_file_path": helmValuesPath,
		},
		req.TenantID,
		req.CloudAccountID,
		"k8s",
	)
	*edges = append(*edges, edge)

	s.logger.Info("created Helm chart to repository connection",
		"chart", helmChartNode.Properties["name"],
		"repository", gitRepo,
		"values_path", helmValuesPath)
}

// parseHelmChartLabel parses Helm chart label (format: "chart-name-version") into name and version
// Examples:
//   - "nginx-ingress-4.0.13" -> ("nginx-ingress", "4.0.13")
//   - "prometheus-15.0.0" -> ("prometheus", "15.0.0")
//   - "my-app-1.2.3" -> ("my-app", "1.2.3")
func (s *K8sSource) parseHelmChartLabel(chartLabel string) (name string, version string) {
	// Find the last hyphen followed by version pattern (digits and dots)
	parts := strings.Split(chartLabel, "-")
	if len(parts) < 2 {
		return chartLabel, ""
	}

	// Try to identify where the version starts (last part that looks like a version)
	for i := len(parts) - 1; i >= 1; i-- {
		possibleVersion := parts[i]
		// Check if this looks like a version (starts with digit)
		if len(possibleVersion) > 0 && possibleVersion[0] >= '0' && possibleVersion[0] <= '9' {
			// This is likely the version
			version = strings.Join(parts[i:], "-")
			name = strings.Join(parts[:i], "-")
			return name, version
		}
	}

	// If no version pattern found, return the whole string as name
	return chartLabel, ""
}

// createHelmChartNode creates a HelmChart node
func (s *K8sSource) createHelmChartNode(chartName, chartVersion, releaseName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name": chartName,
	}

	if chartVersion != "" {
		properties["version"] = chartVersion
	}

	// Optionally include release name for reference
	if releaseName != "" {
		properties["release_name"] = releaseName
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeHelmChart,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeHelmChart, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// =====================================================
// Ingress Backend Service Resolution
// =====================================================

// resolveIngressBackendServices queries Kubernetes Ingress resources and creates Service nodes for backend services.
// This resolves: LoadBalancer/Ingress Controller → Ingress Resource → Backend Services
// The query is executed once per unique account_id to optimize performance.
func (s *K8sSource) resolveIngressBackendServices(
	ctx context.Context,
	reqCtx *security.RequestContext,
	ingressControllerNodes []*core.DbNode,
	serviceNodes []*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge) {
	if len(ingressControllerNodes) == 0 {
		return nil, nil
	}

	// Group ingress controller nodes by account_id
	nodesByAccount := make(map[string][]*core.DbNode)
	for _, node := range ingressControllerNodes {
		accountID := node.CloudAccountID
		if accountID == "" {
			continue
		}
		nodesByAccount[accountID] = append(nodesByAccount[accountID], node)
	}

	var allNodes []*core.DbNode
	var allEdges []*core.DbEdge

	// Execute query once per account_id
	for accountID, controllerNodes := range nodesByAccount {
		nodes, edges, err := s.resolveIngressBackendsForAccount(ctx, accountID, req.TenantID, controllerNodes, serviceNodes, req)
		if err != nil {
			s.logger.Warn("Failed to resolve ingress backends for account",
				"account_id", accountID,
				"error", err)
			continue
		}
		allNodes = append(allNodes, nodes...)
		allEdges = append(allEdges, edges...)
	}

	return allNodes, allEdges
}

// resolveIngressBackendsForAccount fetches ingress resources for a single account and creates backend service nodes
func (s *K8sSource) resolveIngressBackendsForAccount(
	ctx context.Context,
	k8sAccountID string,
	tenantID string,
	controllerNodes []*core.DbNode,
	serviceNodes []*core.DbNode,
	req *core.SourceBuildRequest,
) ([]*core.DbNode, []*core.DbEdge, error) {
	ingressData, err := s.fetchIngressResources(k8sAccountID)
	if err != nil {
		return nil, nil, err
	}
	if ingressData == nil {
		return nil, nil, nil
	}

	controllerMap := s.buildIngressControllerMap(controllerNodes)
	processor := s.newIngressBackendProcessor(k8sAccountID, tenantID, controllerMap, serviceNodes, req)
	nodes, edges := processor.processIngressList(ingressData)

	s.logger.Info("Resolved ingress backend services",
		"account_id", k8sAccountID,
		"backend_services", len(nodes),
		"edges", len(edges))

	return nodes, edges, nil
}

// fetchIngressResources executes kubectl to get ingress resources for an account
func (s *K8sSource) fetchIngressResources(k8sAccountID string) (*k8sIngressList, error) {
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  k8sAccountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": "kubectl get ingress --all-namespaces -o json",
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to execute kubectl command: %w", err)
	}

	stdout, err := s.extractIngressRelayStdout(relayResponse)
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		s.logger.Debug("No Ingress resources found", "account_id", k8sAccountID)
		return nil, nil
	}

	var result k8sIngressList
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, fmt.Errorf("failed to parse ingress list: %w", err)
	}
	return &result, nil
}

// extractIngressRelayStdout extracts stdout from relay response
func (s *K8sSource) extractIngressRelayStdout(relayResponse map[string]any) (string, error) {
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected data format in relay response: %T", relayResponse["data"])
	}
	var relayData struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return "", fmt.Errorf("failed to unmarshal relay data: %w", err)
	}
	return relayData.Stdout, nil
}

// ingressControllerMap holds ingress controllers indexed by class
type ingressControllerMap struct {
	byClass           map[string]*core.DbNode
	defaultController *core.DbNode
}

// buildIngressControllerMap creates a map of controllers by ingress class
func (s *K8sSource) buildIngressControllerMap(controllerNodes []*core.DbNode) *ingressControllerMap {
	cm := &ingressControllerMap{byClass: make(map[string]*core.DbNode)}
	for _, controller := range controllerNodes {
		if ingressClass, ok := controller.Properties["ingress_class"].(string); ok && ingressClass != "" {
			cm.byClass[ingressClass] = controller
		}
		if cm.defaultController == nil {
			cm.defaultController = controller
		}
	}
	return cm
}

func (cm *ingressControllerMap) getController(ingressClassName string) *core.DbNode {
	if ingressClassName != "" {
		if controller, ok := cm.byClass[ingressClassName]; ok {
			return controller
		}
	}
	return cm.defaultController
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

func (s *K8sSource) newIngressBackendProcessor(accountID, tenantID string, cm *ingressControllerMap, serviceNodes []*core.DbNode, req *core.SourceBuildRequest) *ingressBackendProcessor {
	// Build lookup map for existing K8s services by namespace:name
	existingServices := make(map[string]*core.DbNode)
	for _, node := range serviceNodes {
		if node.NodeType != core.NodeTypeK8sService {
			continue
		}
		namespace, _ := node.Properties["namespace"].(string)
		name, _ := node.Properties["name"].(string)
		if namespace != "" && name != "" {
			key := fmt.Sprintf("%s:%s", namespace, name)
			existingServices[key] = node
		}
	}

	return &ingressBackendProcessor{
		source:           s,
		k8sAccountID:     accountID,
		tenantID:         tenantID,
		controllerMap:    cm,
		uniqueBackends:   make(map[string]*core.DbNode),
		existingServices: existingServices,
		req:              req,
	}
}

func (p *ingressBackendProcessor) processIngressList(data *k8sIngressList) ([]*core.DbNode, []*core.DbEdge) {
	for i := range data.Items {
		p.processIngress(&data.Items[i])
	}
	return p.nodes, p.edges
}

func (p *ingressBackendProcessor) processIngress(ingress *k8sIngressResource) {
	controller := p.controllerMap.getController(ingress.Spec.IngressClassName)
	if controller == nil {
		return
	}
	environment, _ := core.GetNodePropertyString(controller, "environment")
	cluster, _ := core.GetNodePropertyString(controller, "cluster")

	// Emit the Ingress node itself + the controller→Ingress edge. Without
	// this the LoadBalancer→Controller→Ingress→K8sService→Workload chain
	// collapses to Controller→backendService and the Ingress resource has
	// no representation in the KG.
	ingressNode := p.createIngressNode(ingress, cluster, environment)
	p.nodes = append(p.nodes, ingressNode)
	p.edges = append(p.edges, p.createEdge(controller.ID, ingressNode.ID,
		map[string]interface{}{
			"connection_type": "ingress_resource",
			"ingress_class":   ingress.Spec.IngressClassName,
		}))

	for i := range ingress.Spec.Rules {
		rule := &ingress.Spec.Rules[i]
		for j := range rule.HTTP.Paths {
			p.processBackendPath(ingress, ingressNode, rule, &rule.HTTP.Paths[j], environment)
		}
	}
}

func (p *ingressBackendProcessor) processBackendPath(
	ingress *k8sIngressResource,
	ingressNode *core.DbNode,
	rule *k8sIngressRule,
	path *k8sIngressPath,
	environment string,
) {
	serviceName := path.Backend.Service.Name
	namespace := ingress.Metadata.Namespace
	if serviceName == "" {
		return
	}

	backendKey := fmt.Sprintf("%s:%s", namespace, serviceName)
	edgeProps := map[string]interface{}{
		"discovered_from": "ingress_resource",
		"ingress_name":    ingress.Metadata.Name,
		"ingress_host":    rule.Host,
		"ingress_path":    path.Path,
		"backend_port":    path.Backend.Service.Port.Number,
	}

	// Resolve backend node: prefer an existing K8sService, otherwise create
	// the synthesized backend Service (cross-namespace ref, or a service
	// filtered out of the relay snapshot). The cache key is processor-scoped
	// so we don't create the same synthesized node twice across multiple
	// Ingresses that share a backend.
	dst, isCached := p.uniqueBackends[backendKey]
	if !isCached {
		if existingService, exists := p.existingServices[backendKey]; exists {
			dst = existingService
		} else {
			dst = p.createBackendNode(serviceName, namespace, environment, ingress, rule, path)
			p.nodes = append(p.nodes, dst)
		}
		p.uniqueBackends[backendKey] = dst
	}

	// Emit one Ingress→K8sService edge per path. Don't skip when the
	// destination is cached — different Ingresses pointing at the same
	// backend each need their own edge (different host/path metadata).
	p.edges = append(p.edges, p.createEdgeWithRel(
		ingressNode.ID, dst.ID, core.RelationshipRoutesToService, edgeProps))

	p.source.logger.Debug("Linked Ingress to backend service",
		"ingress", ingress.Metadata.Name,
		"namespace", namespace,
		"backend_service", serviceName,
		"host", rule.Host,
		"path", path.Path,
		"backend_existed", isCached,
		"account_id", p.k8sAccountID)
}

// createIngressNode builds an Ingress KG node from a fetched K8s Ingress
// resource. One node per Ingress (not per rule/path) — the host/path
// list is aggregated; per-path detail lives on the outbound
// Ingress→K8sService edges. lb_hostname / dns_name come from
// Status.LoadBalancer.Ingress so a future cross-account enricher can
// match the Ingress to its cloud LoadBalancer by DNS.
func (p *ingressBackendProcessor) createIngressNode(
	ingress *k8sIngressResource,
	cluster, environment string,
) *core.DbNode {
	hosts := make([]string, 0, len(ingress.Spec.Rules))
	paths := make([]string, 0)
	for i := range ingress.Spec.Rules {
		r := &ingress.Spec.Rules[i]
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
		for j := range r.HTTP.Paths {
			if r.HTTP.Paths[j].Path != "" {
				paths = append(paths, r.HTTP.Paths[j].Path)
			}
		}
	}

	properties := map[string]interface{}{
		"name":          ingress.Metadata.Name,
		"namespace":     ingress.Metadata.Namespace,
		"cluster":       cluster,
		"environment":   environment,
		"ingress_class": ingress.Spec.IngressClassName,
		"hosts":         hosts,
		"paths":         paths,
	}
	if len(ingress.Metadata.Labels) > 0 {
		properties["labels"] = ingress.Metadata.Labels
	}
	if len(ingress.Metadata.Annotations) > 0 {
		properties["annotations"] = ingress.Metadata.Annotations
	}
	// Pin the first rule's host/path as the canonical query_attribute (the
	// query_attributes spec in core/types.go locks these as scalar strings).
	if len(hosts) > 0 {
		properties["host"] = hosts[0]
	}
	if len(paths) > 0 {
		properties["path"] = paths[0]
	}
	if lbi := ingress.Status.LoadBalancer.Ingress; len(lbi) > 0 {
		if lbi[0].Hostname != "" {
			properties["lb_hostname"] = lbi[0].Hostname
			properties["dns_name"] = strings.ToLower(lbi[0].Hostname)
		}
		if lbi[0].IP != "" {
			properties["lb_ip"] = lbi[0].IP
		}
	}

	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeIngress,
		Properties:     properties,
		CloudAccountID: p.k8sAccountID,
	}
	uniqueKey := p.source.GenerateUniqueKey(tempNode)
	return core.NewNode(core.NodeTypeIngress, uniqueKey, properties, p.tenantID, p.k8sAccountID, "k8s")
}

func (p *ingressBackendProcessor) createBackendNode(
	serviceName, namespace, environment string,
	ingress *k8sIngressResource,
	rule *k8sIngressRule,
	path *k8sIngressPath,
) *core.DbNode {
	properties := map[string]interface{}{
		"name":          serviceName,
		"namespace":     namespace,
		"environment":   environment,
		"service.name":  serviceName,
		"ingress_host":  rule.Host,
		"ingress_path":  path.Path,
		"ingress_name":  ingress.Metadata.Name,
		"backend_port":  path.Backend.Service.Port.Number,
		"exposed_via":   "ingress",
		"ingress_class": ingress.Spec.IngressClassName,
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeService,
		Properties:     properties,
		CloudAccountID: p.k8sAccountID,
	}
	uniqueKey := p.source.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeService, uniqueKey, properties, p.tenantID, p.k8sAccountID, "k8s")
}

func (p *ingressBackendProcessor) createEdge(sourceKey, destKey string, props map[string]interface{}) *core.DbEdge {
	return p.createEdgeWithRel(sourceKey, destKey, core.RelationshipRoutesTo, props)
}

// createEdgeWithRel is the same as createEdge but lets callers pick the
// relationship type. Used for Ingress→K8sService which is RoutesToService,
// not the generic RoutesTo.
func (p *ingressBackendProcessor) createEdgeWithRel(
	sourceKey, destKey string,
	rel core.RelationshipType,
	props map[string]interface{},
) *core.DbEdge {
	return core.NewEdge(sourceKey, destKey, rel, props, p.tenantID, p.k8sAccountID, "k8s")
}

// findIngressControllerNodes identifies ingress controller nodes from workload and service nodes
func (s *K8sSource) findIngressControllerNodes(workloadNodes map[string]*core.DbNode, serviceNodes []*core.DbNode) []*core.DbNode {
	var controllers []*core.DbNode

	// Check workloads for ingress controllers
	for _, node := range workloadNodes {
		if s.isIngressController(node) {
			controllers = append(controllers, node)
		}
	}

	// Check services for LoadBalancer type with ingress-related names
	for _, node := range serviceNodes {
		if s.isIngressControllerService(node) {
			controllers = append(controllers, node)
		}
	}

	return controllers
}

// isIngressController checks if a workload node is an ingress controller
func (s *K8sSource) isIngressController(node *core.DbNode) bool {
	name, _ := core.GetNodePropertyString(node, "name")
	nameLower := strings.ToLower(name)

	// Check name patterns
	if strings.Contains(nameLower, "nginx-ingress") ||
		strings.Contains(nameLower, "ingress-nginx") ||
		strings.Contains(nameLower, "ingress-controller") ||
		strings.Contains(nameLower, "traefik") ||
		strings.Contains(nameLower, "haproxy-ingress") ||
		strings.Contains(nameLower, "kong-ingress") {
		return true
	}

	// Check labels
	if labels, ok := node.Properties["labels"].(map[string]interface{}); ok {
		if appName, ok := labels["app.kubernetes.io/name"].(string); ok {
			appNameLower := strings.ToLower(appName)
			if strings.Contains(appNameLower, "ingress") ||
				strings.Contains(appNameLower, "nginx") ||
				strings.Contains(appNameLower, "traefik") {
				return true
			}
		}
		if component, ok := labels["app.kubernetes.io/component"].(string); ok {
			if strings.ToLower(component) == "controller" {
				return true
			}
		}
	}

	return false
}

// isIngressControllerService checks if a service node is an ingress controller service
func (s *K8sSource) isIngressControllerService(node *core.DbNode) bool {
	// Check if it's a LoadBalancer service
	serviceType, _ := core.GetNodePropertyString(node, "service_type")
	if serviceType != "LoadBalancer" {
		return false
	}

	name, _ := core.GetNodePropertyString(node, "name")
	nameLower := strings.ToLower(name)

	return strings.Contains(nameLower, "ingress") ||
		strings.Contains(nameLower, "nginx") ||
		strings.Contains(nameLower, "traefik")
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
