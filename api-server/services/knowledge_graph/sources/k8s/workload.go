package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"strings"
	"time"

	"github.com/lib/pq"
)

// --- Concrete per-specific_type property schemas ---
//
// Every workload specific_type emitted by createNodeFromWorkload
// ("Kubernetes"+Kind) declares its fixed concrete-property schema here. Names
// are the exact node.Properties keys written by createNodeFromWorkload +
// extractK8sMetadata / extractK8sTimestamps / extractResourceUsage /
// extractK8sStatus (metadata.go); universal base keys (name/subtype/labels/
// is_active/nb_resource_id/...) are implicit via core.universalBaseProperties.
// Indexed fields are hoisted into query_attributes, and at minimum cover the
// per-NodeType QueryablePropertiesMap entries so no existing filter regresses.

// k8sWorkloadProperties is the shared concrete shape for the four apps/v1
// workload Kinds (Deployment / StatefulSet / DaemonSet / ReplicaSet), all of
// which roll up to NodeTypeWorkload. Mirrors how core.k8sWorkloadFields shares
// the ontology shape across the same Kinds.
var k8sWorkloadProperties = []core.PropertyDef{
	{Name: "kind", Indexed: true},
	{Name: "namespace", Indexed: true},
	{Name: "cluster", Indexed: true},
	{Name: "replicas", Indexed: true},
	{Name: "service_account_name", Indexed: true},
	{Name: "last_deployed_time", Indexed: true},
	{Name: "engine", Indexed: true}, // datastore facet (db_classifier.go)
	{Name: "role", Indexed: true},   // datastore facet
	{Name: "primary_image", Indexed: true},
	{Name: "phase", Indexed: true},
	{Name: "strategy_type"},
	{Name: "annotations"},
	{Name: "deployment_revision"},
	{Name: "observed_generation"},
	{Name: "created_at"},
	{Name: "last_status_update_time"},
	{Name: "container_count"},
	{Name: "container_names"},
	{Name: "container_images"},
	{Name: "total_cpu_requests"},
	{Name: "total_memory_requests"},
	{Name: "total_cpu_limits"},
	{Name: "total_memory_limits"},
	{Name: "available_replicas"},
	{Name: "ready_replicas"},
	{Name: "ready"},
	{Name: "k8s_workload_id"},
}

var (
	deploymentSchema  = core.SpecificTypeSchema{SpecificType: "KubernetesDeployment", NodeType: core.NodeTypeWorkload, Properties: k8sWorkloadProperties}
	statefulSetSchema = core.SpecificTypeSchema{SpecificType: "KubernetesStatefulSet", NodeType: core.NodeTypeWorkload, Properties: k8sWorkloadProperties}
	daemonSetSchema   = core.SpecificTypeSchema{SpecificType: "KubernetesDaemonSet", NodeType: core.NodeTypeWorkload, Properties: k8sWorkloadProperties}
	replicaSetSchema  = core.SpecificTypeSchema{SpecificType: "KubernetesReplicaSet", NodeType: core.NodeTypeWorkload, Properties: k8sWorkloadProperties}
)

// k8sBatchWorkloadProperties is the shared shape for the batch Kinds (Job /
// CronJob). They flow through createNodeFromWorkload too, so they carry the
// common workload fields minus the apps/v1-only replicas/strategy fields.
var k8sBatchWorkloadProperties = []core.PropertyDef{
	{Name: "kind", Indexed: true},
	{Name: "namespace", Indexed: true},
	{Name: "cluster", Indexed: true},
	{Name: "service_account_name", Indexed: true},
	{Name: "last_deployed_time"},
	{Name: "phase", Indexed: true},
	{Name: "annotations"},
	{Name: "created_at"},
	{Name: "container_count"},
	{Name: "container_names"},
	{Name: "container_images"},
	{Name: "primary_image"},
	{Name: "k8s_workload_id"},
}

var (
	jobSchema     = core.SpecificTypeSchema{SpecificType: "KubernetesJob", NodeType: core.NodeTypeJob, Properties: k8sBatchWorkloadProperties}
	cronJobSchema = core.SpecificTypeSchema{SpecificType: "KubernetesCronJob", NodeType: core.NodeTypeCronJob, Properties: k8sBatchWorkloadProperties}
)

// podSchema — Pods flow through createNodeFromWorkload (Kind "Pod"); the Pod
// case in extractKindSpecificFields adds node_name / restart_policy / pod_ip /
// host_ip on top of the common workload fields.
var podSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesPod",
	NodeType:     core.NodeTypePod,
	Properties: []core.PropertyDef{
		{Name: "kind", Indexed: true},
		{Name: "namespace", Indexed: true},
		{Name: "cluster", Indexed: true},
		{Name: "phase", Indexed: true},
		{Name: "node_name", Indexed: true},
		{Name: "pod_ip", Indexed: true},
		{Name: "host_ip", Indexed: true},
		{Name: "restart_policy"},
		{Name: "service_account_name"},
		{Name: "annotations"},
		{Name: "ready"},
		{Name: "container_count"},
		{Name: "container_names"},
		{Name: "container_images"},
		{Name: "primary_image"},
		{Name: "k8s_workload_id"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "status_phase"},
		{Name: "creation_timestamp"},
		{Name: "deletion_timestamp"},
		{Name: "automount_service_account_token"},
		{Name: "host_pid"},
		{Name: "host_ipc"},
		{Name: "host_network"},
		{Name: "seccomp_profile_type"},
		{Name: "host_path_volume_paths"},
		{Name: "node"},
		{Name: "architecture_normalized"},
		{Name: "exposed_internet"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(deploymentSchema)
	core.RegisterSpecificTypeSchema(statefulSetSchema)
	core.RegisterSpecificTypeSchema(daemonSetSchema)
	core.RegisterSpecificTypeSchema(replicaSetSchema)
	core.RegisterSpecificTypeSchema(jobSchema)
	core.RegisterSpecificTypeSchema(cronJobSchema)
	core.RegisterSpecificTypeSchema(podSchema)
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

// convertWorkloadsToGraph converts K8s workloads to knowledge graph nodes and edges
func (s *K8sSource) convertWorkloadsToGraph(workloads []K8sWorkloadRow, k8sNodeMap *map[string]*core.DbNode, frameworks map[string]string, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge, map[string]*core.DbNode, map[string]*core.DbNode, map[string]*core.DbNode) {
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
		workloadNode := s.createNodeFromWorkload(&workload, frameworks, req)
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
func (s *K8sSource) createNodeFromWorkload(workload *K8sWorkloadRow, frameworks map[string]string, req *core.SourceBuildRequest) *core.DbNode {
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

	// Concrete native label (specific_type), e.g. KubernetesDeployment. NewNode
	// lifts this into the dedicated column.
	properties["specific_type"] = "Kubernetes" + workload.Kind

	// Datastore facet: if the runtime framework signal marks this workload as an
	// in-cluster database/cache/queue, tag the engine + role it plays. node_type stays
	// Workload (it keeps all its k8s topology); the facet drives the DB icon + UI badge.
	// See db_classifier.go.
	if nodeType == core.NodeTypeWorkload {
		if engine, role, ok := classifyDatastore(frameworks[workload.ID]); ok {
			properties["engine"] = engine
			properties["role"] = role
		}
	}

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)
	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}
