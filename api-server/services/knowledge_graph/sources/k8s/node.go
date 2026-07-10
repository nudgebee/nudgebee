package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// k8sNodeSchema is the concrete-property schema for KubernetesNode.
// Names are the node.Properties keys written by createNodeFromK8sNode; the
// ontology reads cluster/node_flavor/node_region/cpu_capacity/
// memory_capacity, all declared here.
var k8sNodeSchema = core.SpecificTypeSchema{
	SpecificType: "KubernetesNode",
	NodeType:     core.NodeTypeNode,
	Properties: []core.PropertyDef{
		{Name: "cluster", Indexed: true},
		{Name: "node_type", Indexed: true},
		{Name: "node_flavor", Indexed: true},
		{Name: "node_region", Indexed: true},
		{Name: "node_zone", Indexed: true},
		{Name: "cpu_capacity", Indexed: true},
		{Name: "memory_capacity", Indexed: true},
		{Name: "internal_ip", Indexed: true},
		{Name: "cpu_allocatable"},
		{Name: "memory_allocatable"},
		{Name: "node_creation_time"},
		{Name: "conditions"},
		{Name: "pods"},
		{Name: "pod_count"},
		{Name: "taints"},
		{Name: "providerID"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "architecture"},
		{Name: "architecture_normalized"},
		{Name: "os"},
		{Name: "os_image"},
		{Name: "kernel_version"},
		{Name: "container_runtime_version"},
		{Name: "kubelet_version"},
		{Name: "provider_id"},
		{Name: "instance_id"},
	},
}

func init() { core.RegisterSpecificTypeSchema(k8sNodeSchema) }

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
			COALESCE(n.conditions, '') AS conditions, COALESCE(n.node_type, '') AS node_type,
			COALESCE(n.node_flavor, '') AS node_flavor, COALESCE(n.node_region, '') AS node_region,
			COALESCE(n.node_zone, '') AS node_zone,
			n.memory_capacity, n.cpu_capacity, n.memory_allocatable, n.cpu_allocatable,
			n.meta, COALESCE(ca.account_name, '') as cluster_name
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
	properties["specific_type"] = "KubernetesNode"

	// Build unique key using GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNode,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeNode, uniqueKey, properties, req.TenantID, req.CloudAccountID, "k8s")
}

// createK8sNodeResource creates a DbNode (K8s infrastructure node) resource
func (s *K8sSource) createK8sNodeResource(nodeName, clusterName string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":          nodeName,
		"cluster":       clusterName,
		"subtype":       "Node",
		"specific_type": "KubernetesNode",
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
	attributes, err := sources.GetCloudAccountAttributes(reqCtx, cloudAccountID)
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
