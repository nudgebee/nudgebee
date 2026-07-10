package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// gceInstanceSchema is the concrete per-specific_type property schema for the
// GCEInstance specific_type. Names are the node.Properties keys written by
// extractGCPComputeMetadata + enrichComputeInstanceFromCLI +
// extractComputePropertiesFromLabels; universal base keys (name/region/labels/…)
// are implicit (see core.universalBaseProperties). Indexed fields mirror
// core.QueryablePropertiesMap[NodeTypeComputeInstance] so query_attributes never
// regresses. machine_type/public_ip/subnet_id are declared but NOT Indexed
// (their query_attributes are governed by the existing map only).
var gceInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "GCEInstance",
	NodeType:     core.NodeTypeComputeInstance,
	Properties: []core.PropertyDef{
		{Name: "instance_state", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "zone", Indexed: true},
		{Name: "gke_cluster_name", Indexed: true},
		{Name: "gke_node_pool_name", Indexed: true},
		{Name: "is_gke_node", Indexed: true},
		{Name: "machine_type"},
		{Name: "public_ip"},
		{Name: "subnet_id"},
		{Name: "subnet_url"},
		{Name: "vpc_network_url"},
		{Name: "total_disk_size_gb"},
		{Name: "boot_disk_type"},
		{Name: "cpu_cores"},
		{Name: "memory_mb"},
		{Name: "gke_cluster_location"},
		{Name: "provisioning_model"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "self_link"},
		{Name: "hostname"},
		{Name: "zone_name"},
		{Name: "project_id"},
		{Name: "creation_timestamp"},
		{Name: "service_account_email"},
		{Name: "service_account_scopes"},
		{Name: "can_ip_forward"},
		{Name: "enable_vtpm"},
		{Name: "enable_integrity_monitoring"},
		{Name: "enable_confidential_compute"},
		{Name: "enable_oslogin_metadata"},
		{Name: "block_project_ssh_keys"},
		{Name: "serial_port_enable"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gceInstanceSchema) }

// extractGCPComputeMetadata extracts network info from compute.googleapis.com/Instance meta
func (s *GCPSource) extractGCPComputeMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract zone
	if zone, ok := metaMap["zone"].(string); ok && zone != "" {
		properties["zone"] = zone
	}

	// Extract machine type
	if machineType, ok := metaMap["machine_type"].(string); ok && machineType != "" {
		properties["machine_type"] = extractGCPResourceNameFromURL(machineType)
	}

	// Extract network interfaces
	networkInterfaces, ok := metaMap["network_interfaces"].([]interface{})
	if !ok || len(networkInterfaces) == 0 {
		return
	}

	firstNI, ok := networkInterfaces[0].(map[string]interface{})
	if !ok {
		return
	}

	// VPC from network URL
	if network, ok := firstNI["network"].(string); ok && network != "" {
		vpcName := extractGCPResourceNameFromURL(network)
		properties["vpc_id"] = vpcName
		properties["vpc_network_url"] = network
	}

	// Subnet from subnetwork URL
	if subnetwork, ok := firstNI["subnetwork"].(string); ok && subnetwork != "" {
		subnetName := extractGCPResourceNameFromURL(subnetwork)
		properties["subnet_id"] = subnetName
		properties["subnet_url"] = subnetwork
	}

	// Private IP
	if networkIP, ok := firstNI["network_i_p"].(string); ok && networkIP != "" {
		properties["private_ip"] = networkIP
	}

	// External IP from access configs
	if accessConfigs, ok := firstNI["access_configs"].([]interface{}); ok && len(accessConfigs) > 0 {
		if ac, ok := accessConfigs[0].(map[string]interface{}); ok {
			if natIP, ok := ac["nat_i_p"].(string); ok && natIP != "" {
				properties["public_ip"] = natIP
			}
		}
	}
}

// fetchComputeInstancesFromGCP fetches all compute instances via gcloud CLI
func (s *GCPSource) fetchComputeInstancesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPComputeInstance, error) {
	cmd := "gcloud compute instances list --format=json"

	s.logger.Info("fetching GCP compute instances via CLI", "account_id", accountID)

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

	var instances []GCPComputeInstance
	if err := json.Unmarshal([]byte(output), &instances); err != nil {
		return nil, fmt.Errorf("failed to parse compute instances response: %w", err)
	}

	return instances, nil
}

// enrichComputeInstanceFromCLI enriches a compute instance node with CLI data
func (s *GCPSource) enrichComputeInstanceFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	inst, ok := cliData.computeInstances[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set from metadata)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC && len(inst.NetworkInterfaces) > 0 {
		ni := inst.NetworkInterfaces[0]
		if ni.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(ni.Network)
			node.Properties["vpc_network_url"] = ni.Network
		}
		if ni.Subnetwork != "" {
			node.Properties["subnet_id"] = extractGCPResourceNameFromURL(ni.Subnetwork)
			node.Properties["subnet_url"] = ni.Subnetwork
		}
		if ni.NetworkIP != "" {
			node.Properties["private_ip"] = ni.NetworkIP
		}
		if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != "" {
			node.Properties["public_ip"] = ni.AccessConfigs[0].NatIP
		}
	}

	// Additional properties from CLI (always enrich if available)
	if inst.Zone != "" {
		node.Properties["zone"] = extractGCPResourceNameFromURL(inst.Zone)
	}
	if inst.Status != "" {
		node.Properties["instance_state"] = inst.Status
	}
	if inst.MachineType != "" {
		// Only set if not already set from labels
		if _, exists := node.Properties["machine_type"]; !exists {
			node.Properties["machine_type"] = extractGCPResourceNameFromURL(inst.MachineType)
		}
	}

	// Merge CLI labels with existing labels
	if len(inst.Labels) > 0 {
		existingLabels, ok := node.Properties["labels"].(map[string]interface{})
		if !ok {
			existingLabels = make(map[string]interface{})
		}
		for k, v := range inst.Labels {
			// Don't overwrite existing labels
			if _, exists := existingLabels[k]; !exists {
				existingLabels[k] = v
			}
		}
		node.Properties["labels"] = existingLabels
	}

	// Disk information
	if len(inst.Disks) > 0 {
		var totalDiskSizeGb int
		var bootDiskType string
		for _, disk := range inst.Disks {
			if disk.DiskSizeGb != "" {
				// Try to parse disk size
				var size int
				if _, err := fmt.Sscanf(disk.DiskSizeGb, "%d", &size); err == nil {
					totalDiskSizeGb += size
				}
			}
			if disk.Boot && disk.Type != "" {
				bootDiskType = extractGCPResourceNameFromURL(disk.Type)
			}
		}
		if totalDiskSizeGb > 0 {
			node.Properties["total_disk_size_gb"] = totalDiskSizeGb
		}
		if bootDiskType != "" {
			node.Properties["boot_disk_type"] = bootDiskType
		}
	}
}

// createComputeInstanceEdges creates edges from compute instances to VPCs and subnets
func (s *GCPSource) createComputeInstanceEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	computeNodes, ok := lookup.ByNodeType[core.NodeTypeComputeInstance]
	if !ok {
		return edges
	}

	for _, node := range computeNodes {
		// Compute → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// Compute → Subnet
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}
	}

	s.logger.Info("created GCP compute instance edges", "edge_count", len(edges))
	return edges
}

// extractComputePropertiesFromLabels extracts compute-related properties from GCP labels
// This is useful for resources without metadata (type=compute-engine from billing)
// that have labels like system:compute.googleapis.com/machine_spec
func extractComputePropertiesFromLabels(properties map[string]interface{}, labels map[string]interface{}) {
	// Extract machine spec from billing/system labels
	if machineSpec := extractGCPLabelValue(labels, "system:compute.googleapis.com/machine_spec"); machineSpec != "" {
		if _, exists := properties["machine_type"]; !exists {
			properties["machine_type"] = machineSpec
		}
	}
	if cores := extractGCPLabelValue(labels, "system:compute.googleapis.com/cores"); cores != "" {
		properties["cpu_cores"] = cores
	}
	if memory := extractGCPLabelValue(labels, "system:compute.googleapis.com/memory"); memory != "" {
		properties["memory_mb"] = memory
	}

	// GKE-specific labels for compute instances that are GKE nodes
	if clusterName := extractGCPLabelValue(labels, "goog-k8s-cluster-name"); clusterName != "" {
		properties["gke_cluster_name"] = clusterName
	}
	if nodePoolName := extractGCPLabelValue(labels, "goog-k8s-node-pool-name"); nodePoolName != "" {
		properties["gke_node_pool_name"] = nodePoolName
	}
	if clusterLocation := extractGCPLabelValue(labels, "goog-k8s-cluster-location"); clusterLocation != "" {
		properties["gke_cluster_location"] = clusterLocation
	}
	if provisioningModel := extractGCPLabelValue(labels, "goog-gke-node-pool-provisioning-model"); provisioningModel != "" {
		properties["provisioning_model"] = provisioningModel // spot, on-demand, reservation, etc.
	}

	// Check if this is a GKE node
	if gkeNode := extractGCPLabelValue(labels, "goog-gke-node"); gkeNode != "" {
		properties["is_gke_node"] = true
	}
}
