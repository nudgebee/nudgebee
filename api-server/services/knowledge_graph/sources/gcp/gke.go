package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// gkeClusterSchema is the concrete per-specific_type property schema for the
// GKECluster specific_type. Names are the node.Properties keys written by
// extractGCPGKEMetadata + enrichGKEClusterFromCLI.
// Indexed mirrors core.QueryablePropertiesMap[NodeTypeManagedCluster] for the
// fields GCP populates. cluster_state is declared (the ontology coalesces it) but
// not Indexed — the map's "status" key already covers state filtering.
var gkeClusterSchema = core.SpecificTypeSchema{
	SpecificType: "GKECluster",
	NodeType:     core.NodeTypeManagedCluster,
	Properties: []core.PropertyDef{
		{Name: "dns_name", Indexed: true},
		{Name: "kubernetes_version", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		{Name: "node_pool_count", Indexed: true},
		{Name: "cluster_state"},
		{Name: "vpc_network_url"},
		{Name: "subnet_url"},
		{Name: "node_version"},
		{Name: "node_pools"},
		{Name: "total_initial_node_count"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "self_link"},
		{Name: "description"},
		{Name: "logging_service"},
		{Name: "monitoring_service"},
		{Name: "network"},
		{Name: "subnetwork"},
		{Name: "cluster_ipv4cidr"},
		{Name: "zone"},
		{Name: "endpoint"},
		{Name: "initial_version"},
		{Name: "current_master_version"},
		{Name: "services_ipv4cidr"},
		{Name: "database_encryption"},
		{Name: "network_policy"},
		{Name: "master_authorized_networks"},
		{Name: "legacy_abac"},
		{Name: "shielded_nodes"},
		{Name: "workload_identity_enabled"},
		{Name: "exposed_internet"},
		{Name: "private_nodes"},
		{Name: "private_endpoint_enabled"},
		{Name: "private_endpoint"},
		{Name: "public_endpoint"},
		{Name: "masterauth_username"},
		{Name: "masterauth_password"},
		{Name: "created_at"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gkeClusterSchema) }

// extractGCPGKEMetadata extracts network info from container.googleapis.com/Cluster meta
func (s *GCPSource) extractGCPGKEMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoint
	if endpoint, ok := metaMap["endpoint"].(string); ok && endpoint != "" {
		properties["dns_name"] = endpoint
	}

	// Network config
	if networkConfig, ok := metaMap["network_config"].(map[string]interface{}); ok {
		if network, ok := networkConfig["network"].(string); ok && network != "" {
			vpcName := extractGCPResourceNameFromURL(network)
			properties["vpc_id"] = vpcName
			properties["vpc_network_url"] = network
		}
		if subnetwork, ok := networkConfig["subnetwork"].(string); ok && subnetwork != "" {
			subnetName := extractGCPResourceNameFromURL(subnetwork)
			properties["subnet_id"] = subnetName
			properties["subnet_url"] = subnetwork
		}
	}

	// Fallback: top-level network/subnetwork fields
	if _, hasVPC := properties["vpc_id"]; !hasVPC {
		if network, ok := metaMap["network"].(string); ok && network != "" {
			properties["vpc_id"] = network
		}
	}
	if _, hasSubnet := properties["subnet_id"]; !hasSubnet {
		if subnetwork, ok := metaMap["subnetwork"].(string); ok && subnetwork != "" {
			properties["subnet_id"] = subnetwork
		}
	}

	// Current node version
	if version, ok := metaMap["current_master_version"].(string); ok && version != "" {
		properties["kubernetes_version"] = version
	}
}

// fetchGKEClustersFromGCP fetches all GKE clusters via gcloud CLI
func (s *GCPSource) fetchGKEClustersFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPGKECluster, error) {
	cmd := "gcloud container clusters list --format=json"

	s.logger.Info("fetching GCP GKE clusters via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var clusters []GCPGKECluster
	if err := json.Unmarshal([]byte(output), &clusters); err != nil {
		return nil, fmt.Errorf("failed to parse GKE clusters response: %w", err)
	}

	return clusters, nil
}

// enrichGKEClusterFromCLI enriches a GKE cluster node with CLI data
func (s *GCPSource) enrichGKEClusterFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	cluster, ok := cliData.gkeClusters[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if cluster.Network != "" {
			node.Properties["vpc_id"] = cluster.Network
		}
		if cluster.Subnetwork != "" {
			node.Properties["subnet_id"] = cluster.Subnetwork
		}
	}

	// Basic properties
	if cluster.Endpoint != "" {
		node.Properties["dns_name"] = cluster.Endpoint
	}
	if cluster.Status != "" {
		node.Properties["cluster_state"] = cluster.Status
	}
	if cluster.Location != "" {
		node.Properties["location"] = cluster.Location
	}

	// Kubernetes version
	if cluster.CurrentMasterVersion != "" {
		node.Properties["kubernetes_version"] = cluster.CurrentMasterVersion
	}
	if cluster.CurrentNodeVersion != "" {
		node.Properties["node_version"] = cluster.CurrentNodeVersion
	}

	// Node pools information
	if len(cluster.NodePools) > 0 {
		nodePoolsInfo := make([]map[string]interface{}, 0, len(cluster.NodePools))
		var totalNodeCount int
		for _, np := range cluster.NodePools {
			poolInfo := map[string]interface{}{
				"name":               np.Name,
				"initial_node_count": np.InitialNodeCount,
			}
			if np.Version != "" {
				poolInfo["version"] = np.Version
			}
			if np.Status != "" {
				poolInfo["status"] = np.Status
			}
			if np.Config.MachineType != "" {
				poolInfo["machine_type"] = np.Config.MachineType
			}
			if np.Config.DiskSizeGb > 0 {
				poolInfo["disk_size_gb"] = np.Config.DiskSizeGb
			}
			if np.Autoscaling.Enabled {
				poolInfo["autoscaling_enabled"] = true
				poolInfo["min_node_count"] = np.Autoscaling.MinNodeCount
				poolInfo["max_node_count"] = np.Autoscaling.MaxNodeCount
			}
			nodePoolsInfo = append(nodePoolsInfo, poolInfo)
			totalNodeCount += np.InitialNodeCount
		}
		node.Properties["node_pools"] = nodePoolsInfo
		node.Properties["node_pool_count"] = len(cluster.NodePools)
		node.Properties["total_initial_node_count"] = totalNodeCount
	}
}

// ensureGCPNodePoolNodes ensures Node Pool nodes exist for all GKE node pools from CLI data
func (s *GCPSource) ensureGCPNodePoolNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for clusterName, cluster := range cliData.gkeClusters {
		for _, nodePool := range cluster.NodePools {
			// Check if a Node Pool node already exists
			found := false
			if poolNodes, ok := lookup.ByNodeType[core.NodeTypeComputeInstancePool]; ok {
				for _, existing := range poolNodes {
					existingName := getNodeName(existing)
					existingCluster, _ := existing.Properties["cluster_name"].(string)
					if existingName == nodePool.Name && existingCluster == clusterName {
						found = true
						break
					}
				}
			}

			if !found {
				// Create new Node Pool node from CLI data
				properties := map[string]interface{}{
					"name":           nodePool.Name,
					"cluster_name":   clusterName,
					"cloud_provider": "GCP",
					"service_name":   "Kubernetes Engine",
					"subtype":        "GKENodePool",
					"inferred":       false,
				}

				// Location from cluster
				if cluster.Location != "" {
					properties["region"] = cluster.Location
				}

				// Node pool config
				if nodePool.Config.MachineType != "" {
					properties["machine_type"] = nodePool.Config.MachineType
				}
				if nodePool.Config.DiskSizeGb > 0 {
					properties["disk_size_gb"] = nodePool.Config.DiskSizeGb
				}
				if nodePool.Config.DiskType != "" {
					properties["disk_type"] = nodePool.Config.DiskType
				}

				// Node counts
				properties["initial_node_count"] = nodePool.InitialNodeCount

				// Version and status
				if nodePool.Version != "" {
					properties["version"] = nodePool.Version
				}
				if nodePool.Status != "" {
					properties["status"] = nodePool.Status
				}

				// Autoscaling config
				properties["autoscaling_enabled"] = nodePool.Autoscaling.Enabled
				if nodePool.Autoscaling.Enabled {
					properties["min_node_count"] = nodePool.Autoscaling.MinNodeCount
					properties["max_node_count"] = nodePool.Autoscaling.MaxNodeCount
				}

				// Generate unique key: gcp:{account}:{location}:ComputeInstancePool:{cluster_name}:{node_pool_name}
				tempNode := &core.DbNode{
					NodeType:       core.NodeTypeComputeInstancePool,
					Properties:     properties,
					CloudAccountID: req.CloudAccountID,
				}
				uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s:%s",
					req.CloudAccountID,
					cluster.Location,
					core.NodeTypeComputeInstancePool,
					clusterName,
					nodePool.Name)
				tempNode.UniqueKey = uniqueKey

				node := core.NewNode(core.NodeTypeComputeInstancePool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
				nodes = append(nodes, node)
				s.logger.Debug("created GCP Node Pool node from CLI",
					"name", nodePool.Name,
					"cluster", clusterName,
					"machine_type", nodePool.Config.MachineType)
			}
		}
	}

	return nodes
}

// createGKEClusterEdges creates edges from GKE clusters to VPCs and subnets
func (s *GCPSource) createGKEClusterEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	clusterNodes, ok := lookup.ByNodeType[core.NodeTypeManagedCluster]
	if !ok {
		return edges
	}

	for _, node := range clusterNodes {
		// GKE → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// GKE → Subnet
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}
	}

	s.logger.Info("created GCP GKE cluster edges", "edge_count", len(edges))
	return edges
}

// createNodePoolToClusterEdges creates edges from Node Pools to their GKE clusters
func (s *GCPSource) createNodePoolToClusterEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	poolNodes, hasPools := lookup.ByNodeType[core.NodeTypeComputeInstancePool]
	if !hasPools {
		return edges
	}

	clusterNodes, hasClusters := lookup.ByNodeType[core.NodeTypeManagedCluster]
	if !hasClusters {
		return edges
	}

	// Build cluster lookup by name
	clusterByName := make(map[string]*core.DbNode)
	for _, clusterNode := range clusterNodes {
		if name, ok := clusterNode.Properties["name"].(string); ok {
			shortName := extractGCPShortName(name)
			if shortName != "" {
				clusterByName[shortName] = clusterNode
			}
		}
	}

	for _, poolNode := range poolNodes {
		clusterName, ok := poolNode.Properties["cluster_name"].(string)
		if !ok || clusterName == "" {
			continue
		}

		// Find the matching cluster node
		if clusterNode, exists := clusterByName[clusterName]; exists {
			edgeProps := map[string]interface{}{
				"connection_type": "cluster_node_pool",
			}

			edges = append(edges, s.createEdge(poolNode, clusterNode, core.RelationshipBelongsTo, edgeProps, req))
			s.logger.Debug("created Node Pool → Cluster edge",
				"node_pool", getNodeName(poolNode),
				"cluster", clusterName)
		}
	}

	s.logger.Info("created GCP Node Pool → Cluster edges", "edge_count", len(edges))
	return edges
}

// createGKEInstanceEdges creates edges from GKE compute instances to their GKE clusters and Node Pools
// Uses goog-k8s-cluster-name label (primary) or GKE naming pattern (fallback): gke-{cluster-name}-{node-pool}-{hash}
// Creates two edges per GKE instance:
//   - Compute Instance → GKE Cluster (BELONGS_TO)
//   - Compute Instance → Node Pool (BELONGS_TO)
func (s *GCPSource) createGKEInstanceEdges(_ *security.RequestContext, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	computeNodes, hasCompute := lookup.ByNodeType[core.NodeTypeComputeInstance]
	if !hasCompute {
		return edges
	}

	clusterNodes, hasClusters := lookup.ByNodeType[core.NodeTypeManagedCluster]
	if !hasClusters {
		return edges
	}

	// Build a map of cluster short names to cluster nodes
	clusterByName := make(map[string]*core.DbNode)
	for _, clusterNode := range clusterNodes {
		if name, ok := clusterNode.Properties["name"].(string); ok {
			shortName := extractGCPShortName(name)
			if shortName != "" {
				clusterByName[shortName] = clusterNode
			}
		}
	}

	// Build a map of (cluster_name, node_pool_name) to node pool nodes
	poolNodes, hasPools := lookup.ByNodeType[core.NodeTypeComputeInstancePool]
	nodePoolByKey := make(map[string]*core.DbNode)
	if hasPools {
		for _, poolNode := range poolNodes {
			clusterName, _ := poolNode.Properties["cluster_name"].(string)
			poolName := getNodeName(poolNode)
			if clusterName != "" && poolName != "" {
				key := clusterName + ":" + poolName
				nodePoolByKey[key] = poolNode
			}
		}
	}

	for _, computeNode := range computeNodes {
		name, ok := computeNode.Properties["name"].(string)
		if !ok || name == "" {
			continue
		}

		shortName := extractGCPShortName(name)
		if shortName == "" {
			continue
		}

		var clusterName string
		var nodePoolName string
		var clusterLocation string
		var provisioningModel string

		// Primary: Try to get cluster and node pool info from labels
		if labels, ok := computeNode.Properties["labels"].(map[string]interface{}); ok {
			clusterName = extractGCPLabelValue(labels, "goog-k8s-cluster-name")
			nodePoolName = extractGCPLabelValue(labels, "goog-k8s-node-pool-name")
			clusterLocation = extractGCPLabelValue(labels, "goog-k8s-cluster-location")
			provisioningModel = extractGCPLabelValue(labels, "goog-gke-node-pool-provisioning-model")
		}

		// Also check node properties (set by extractComputePropertiesFromLabels)
		if nodePoolName == "" {
			if np, ok := computeNode.Properties["gke_node_pool_name"].(string); ok {
				nodePoolName = np
			}
		}

		// Fallback: Extract cluster name from GKE instance naming pattern
		if clusterName == "" {
			matches := gkeInstanceNameRegex.FindStringSubmatch(shortName)
			if len(matches) >= 2 {
				clusterName = matches[1]
			}
		}

		if clusterName == "" {
			continue
		}

		// Edge 1: Compute Instance → GKE Cluster (BELONGS_TO)
		if clusterNode, exists := clusterByName[clusterName]; exists {
			edgeProps := map[string]interface{}{
				"connection_type": "gke_node",
				"inferred":        true,
			}
			if nodePoolName != "" {
				edgeProps["node_pool_name"] = nodePoolName
			}
			if clusterLocation != "" {
				edgeProps["cluster_location"] = clusterLocation
			}
			if provisioningModel != "" {
				edgeProps["provisioning_model"] = provisioningModel
			}

			edges = append(edges, s.createEdge(computeNode, clusterNode, core.RelationshipBelongsTo, edgeProps, req))
			s.logger.Debug("created GKE instance → cluster edge",
				"instance", shortName,
				"cluster", clusterName,
				"node_pool", nodePoolName)
		}

		// Edge 2: Compute Instance → Node Pool (BELONGS_TO)
		if nodePoolName != "" {
			poolKey := clusterName + ":" + nodePoolName
			if poolNode, exists := nodePoolByKey[poolKey]; exists {
				poolEdgeProps := map[string]interface{}{
					"connection_type": "node_pool_instance",
					"inferred":        true,
				}
				if provisioningModel != "" {
					poolEdgeProps["provisioning_model"] = provisioningModel
				}

				edges = append(edges, s.createEdge(computeNode, poolNode, core.RelationshipBelongsTo, poolEdgeProps, req))
				s.logger.Debug("created GKE instance → node pool edge",
					"instance", shortName,
					"node_pool", nodePoolName,
					"cluster", clusterName)
			}
		}
	}

	s.logger.Info("created GKE instance edges", "edge_count", len(edges))
	return edges
}
