package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	ID             string          `db:"id"`
	TenantID       string          `db:"tenant_id"`
	CloudAccountID string          `db:"cloud_account_id"`
	Kind           string          `db:"kind"`
	Namespace      string          `db:"namespace"`
	Name           string          `db:"name"`
	ClusterName    string          `db:"cluster_name"`
	IsActive       bool            `db:"is_active"`
	Meta           json.RawMessage `db:"meta"`
	Labels         json.RawMessage `db:"labels"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
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
			ca.account_name as cluster_name, w.is_active, w.meta, w.labels, w.creation_time as created_at, w.creation_time as updated_at
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
	// Try to find a workload in the same namespace
	for _, workload := range workloads {
		if workload.Namespace == service.Metadata.Namespace {
			return workload.ClusterName
		}
	}
	// Return empty string if no match found
	return ""
}

// getClusterNameForResource determines the cluster name for a resource by looking at workloads in the same namespace
func (s *K8sSource) getClusterNameForResource(namespace string, workloads []K8sWorkloadRow) string {
	// Try to find a workload in the same namespace
	for _, workload := range workloads {
		if workload.Namespace == namespace {
			return workload.ClusterName
		}
	}
	// Return empty string if no match found
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

	// Add cloud-specific volume source information
	if len(pv.Spec.AWSElasticBlockStore) > 0 {
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

			// Set the timestamps
			if createdAt != "" {
				properties["created_at"] = createdAt
			}
			if lastDeployedTime != "" {
				properties["last_deployed_time"] = lastDeployedTime
			}

			// If we only have one of them, use it for both
			if createdAt == "" && lastDeployedTime != "" {
				properties["created_at"] = lastDeployedTime
			}
			if lastDeployedTime == "" && createdAt != "" {
				properties["last_deployed_time"] = createdAt
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

// extractKindSpecificFields extracts essential fields based on workload kind
func (s *K8sSource) extractKindSpecificFields(properties map[string]interface{}, metaMap map[string]interface{}, kind string) {
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
	gitHash, hasHash := annotations["ci.nudgebee.com/git.hash"].(string)
	gitRepo, hasRepo := annotations["ci.nudgebee.com/git.repo"].(string)
	helmValuesPath, hasPath := annotations["ci.nudgebee.com/helm.values.filePath"].(string)

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
	gitHash, hasHash := annotations["ci.nudgebee.com/git.hash"].(string)
	gitRepo, hasRepo := annotations["ci.nudgebee.com/git.repo"].(string)
	helmValuesPath, hasPath := annotations["ci.nudgebee.com/helm.values.filePath"].(string)

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

	for i := range ingress.Spec.Rules {
		rule := &ingress.Spec.Rules[i]
		for j := range rule.HTTP.Paths {
			p.processBackendPath(ingress, rule, &rule.HTTP.Paths[j], controller, environment)
		}
	}
}

func (p *ingressBackendProcessor) processBackendPath(
	ingress *k8sIngressResource,
	rule *k8sIngressRule,
	path *k8sIngressPath,
	controller *core.DbNode,
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

	// First check if we've already processed this backend in this run
	if _, exists := p.uniqueBackends[backendKey]; exists {
		// edge already created
		// p.edges = append(p.edges, p.createEdge(controller.ID, existingNode.ID, edgeProps))
		return
	}

	// Check if there's an existing K8s service node we can link to
	if existingService, exists := p.existingServices[backendKey]; exists {
		p.uniqueBackends[backendKey] = existingService
		p.edges = append(p.edges, p.createEdge(controller.ID, existingService.ID, edgeProps))
		p.source.logger.Debug("Linked Ingress to existing K8s service",
			"ingress", ingress.Metadata.Name,
			"namespace", namespace,
			"backend_service", serviceName,
			"host", rule.Host,
			"path", path.Path,
			"account_id", p.k8sAccountID)
		return
	}

	// No existing service found, create a new backend node
	node := p.createBackendNode(serviceName, namespace, environment, ingress, rule, path)
	p.uniqueBackends[backendKey] = node
	p.nodes = append(p.nodes, node)
	p.edges = append(p.edges, p.createEdge(controller.UniqueKey, node.UniqueKey, edgeProps))

	p.source.logger.Debug("Created new Ingress backend service node",
		"ingress", ingress.Metadata.Name,
		"namespace", namespace,
		"backend_service", serviceName,
		"host", rule.Host,
		"path", path.Path,
		"account_id", p.k8sAccountID)
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
	return core.NewEdge(
		sourceKey,
		destKey,
		core.RelationshipRoutesTo,
		props,
		p.tenantID,
		p.k8sAccountID,
		"k8s",
	)
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
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec k8sIngressSpec `json:"spec"`
}

type k8sIngressList struct {
	Items []k8sIngressResource `json:"items"`
}
