package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// networkInterfaceSchema — concrete schema for ENIs. Fields written by
// extractENIMetadata / createENINodeFromAWSData.
var networkInterfaceSchema = core.SpecificTypeSchema{
	SpecificType: "NetworkInterface",
	NodeType:     core.NodeTypeNetworkInterface,
	Properties: []core.PropertyDef{
		{Name: "private_ip_address", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "interface_type", Indexed: true},
		{Name: "availability_zone", Indexed: true},
		{Name: "eni_status"},
		{Name: "private_ips"},
		{Name: "network_interface_id"},
		{Name: "requester_id"},
		{Name: "description"},
		{Name: "security_groups"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "mac_address"},
		{Name: "private_dns_name"},
		{Name: "public_ip"},
		{Name: "requester_managed"},
		{Name: "source_dest_check"},
		{Name: "attach_time"},
		{Name: "device_index"},
	},
}

func init() { core.RegisterSpecificTypeSchema(networkInterfaceSchema) }

// extractENIMetadata extracts essential fields for AWS Elastic Network Interfaces.
// Runs on the primary createNodeFromResource path so every ENI gets the IP set
// regardless of whether createENINodeFromAWSData (the secondary, fallback path)
// also fires. Without this, the 28 active ENIs in prod kept arriving without
// a private_ips property even after #30683.
func (s *AWSSource) extractENIMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if id, ok := metaMap["NetworkInterfaceId"].(string); ok && id != "" {
		properties["network_interface_id"] = id
	}
	if primary, ok := metaMap["PrivateIpAddress"].(string); ok && primary != "" {
		properties["private_ip_address"] = primary
	}
	if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if iface, ok := metaMap["InterfaceType"].(string); ok && iface != "" {
		properties["interface_type"] = iface
	}
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["eni_status"] = status
	}

	// PrivateIpAddresses[] is the full list including the primary plus every
	// secondary IP — these are the VPC-CNI pod IPs in EKS. Without storing
	// them, 172.31.x.x ExternalService orphans can't resolve to their owning
	// ENI. See #30683.
	if rawList, ok := metaMap["PrivateIpAddresses"].([]interface{}); ok && len(rawList) > 0 {
		ips := make([]string, 0, len(rawList))
		for _, item := range rawList {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if ip, _ := entry["PrivateIpAddress"].(string); ip != "" {
				ips = append(ips, ip)
			}
		}
		if len(ips) > 0 {
			properties["private_ips"] = ips
		}
	}
}

// createENIEdges creates edges for Elastic Network Interfaces (ENI)
// Fetches ENI metadata from the in-memory DB meta cache and connects them to their cloud resources.
// Returns valid ENI nodes (ENIs present in the DB meta cache) and edges.
func (s *AWSSource) createENIEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	edges := make([]*core.DbEdge, 0)
	validENINodes := make([]*core.DbNode, 0)

	// Use the Nudgebee account ID from the request (cloud_accounts.id)
	if req.CloudAccountID == "" {
		s.logger.Warn("Cannot create ENI edges: Cloud account ID not found")
		return validENINodes, edges
	}

	// Fetch all ENIs from AWS using cloud collector CLI
	eniData, err := s.fetchENIDataFromAWS(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Error("Failed to fetch ENI data from AWS", "error", err)
		return validENINodes, edges
	}

	// Create a map of resource_id to ENI node for quick lookup (existing nodes from DB)
	eniNodeMap := make(map[string]*core.DbNode)
	for _, node := range nodes {
		if resourceID, ok := node.Properties["resource_id"].(string); ok {
			eniNodeMap[resourceID] = node
		}
	}

	// Process each ENI from DB meta cache
	for _, eniInfo := range eniData {
		var eniNode *core.DbNode

		// Check if this ENI already has a node built from the DB rows
		if existingNode, exists := eniNodeMap[eniInfo.NetworkInterfaceId]; exists {
			eniNode = existingNode
		} else {
			// ENI is in the DB meta cache but wasn't converted to a node
			// (e.g. filtered out by ServiceTypeFilter). Create one now from the cached data.
			eniNode = s.createENINodeFromAWSData(eniInfo, req)
			s.logger.Info("Created ENI node from DB meta cache (not in existing node list)",
				"eni_id", eniInfo.NetworkInterfaceId,
				"description", eniInfo.Description)
		}
		// Add to valid nodes (all ENIs present in the DB meta cache)
		validENINodes = append(validENINodes, eniNode)

		// 1. ENI → VPC relationship
		if eniInfo.VpcId != "" {
			if vpcNode, found := lookup.ByResourceID[eniInfo.VpcId]; found {
				edges = append(edges, s.createEdge(eniNode, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			} else {
				s.logger.Warn("ENI VPC not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "vpc_id", eniInfo.VpcId)
			}
		}

		// 2. ENI → Subnet relationship
		if eniInfo.SubnetId != "" {
			if subnetNode, found := lookup.ByResourceID[eniInfo.SubnetId]; found {
				edges = append(edges, s.createEdge(eniNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			} else {
				s.logger.Warn("ENI Subnet not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "subnet_id", eniInfo.SubnetId)
			}
		}

		// 3. ENI → Security Group relationships
		for _, group := range eniInfo.Groups {
			if sgNode, found := lookup.ByResourceID[group.GroupId]; found {
				edges = append(edges, s.createEdge(eniNode, sgNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "security_group",
						"group_name":      group.GroupName,
					}, req))
			} else {
				s.logger.Warn("ENI SecurityGroup not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "sg_id", group.GroupId)
			}
		}

		// 4. ENI → Attached Resource (EC2, RDS, Lambda, etc.)
		if eniInfo.Attachment != nil && eniInfo.Attachment.InstanceId != "" {
			// Try to find the attached resource by instance ID
			if attachedNode, found := lookup.ByResourceID[eniInfo.Attachment.InstanceId]; found {
				edges = append(edges, s.createEdge(attachedNode, eniNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type":       "eni_attachment",
						"status":                eniInfo.Attachment.Status,
						"device_index":          eniInfo.Attachment.DeviceIndex,
						"delete_on_termination": eniInfo.Attachment.DeleteOnTermination,
					}, req))
			}
		}

		// 5. Special handling for RDS ENIs (via requester ID and tags)
		if eniInfo.RequesterId == "amazon-rds" {
			// Try to match RDS instance by subnet and tags
			for _, rdsNode := range lookup.ByNodeType[core.NodeTypeDatabase] {
				if s.matchENIToRDS(eniInfo, rdsNode) {
					edges = append(edges, s.createEdge(eniNode, rdsNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "rds_interface",
							"requester_id":    eniInfo.RequesterId,
						}, req))
					break
				}
			}
		}

		// 6. Special handling for Load Balancer ENIs
		if strings.Contains(eniInfo.RequesterId, "amazon-elb") || strings.Contains(eniInfo.RequesterId, "amazon-elasticloadbalancing") {
			// Try to match load balancer by description
			for _, lbNode := range lookup.ByNodeType[core.NodeTypeLoadBalancer] {
				if lbName, ok := getStringProperty(lbNode, "name"); ok {
					if strings.Contains(eniInfo.Description, lbName) {
						edges = append(edges, s.createEdge(eniNode, lbNode, core.RelationshipHostedOn,
							map[string]interface{}{
								"connection_type": "load_balancer_interface",
								"description":     eniInfo.Description,
							}, req))
						break
					}
				}
			}
		}

		// 7. Special handling for NAT Gateway ENIs.
		// AWS sets Description to "Interface for NAT Gateway nat-XXXXX" for these ENIs,
		// which gives a direct ID match. Fall back to subnet_id matching only if the
		// description-based match fails (e.g. custom descriptions).
		if eniInfo.InterfaceType == "nat_gateway" {
			matched := false
			// Primary: match by NAT Gateway ID embedded in description
			for _, natNode := range lookup.ByNodeType[core.NodeTypeNetworkGateway] {
				if natID, ok := getStringProperty(natNode, "nat_gateway_id"); ok && natID != "" {
					// AWS description format: "Interface for NAT Gateway nat-XXXXX"
					if strings.Contains(eniInfo.Description, natID) {
						edges = append(edges, s.createEdge(eniNode, natNode, core.RelationshipHostedOn,
							map[string]interface{}{
								"connection_type": "nat_gateway_interface",
								"interface_type":  eniInfo.InterfaceType,
							}, req))
						matched = true
						break
					}
				}
			}
			// Fallback: match by shared subnet_id (less precise)
			if !matched {
				for _, natNode := range lookup.ByNodeType[core.NodeTypeNetworkGateway] {
					if natSubnetID, ok := getStringProperty(natNode, "subnet_id"); ok {
						if natSubnetID == eniInfo.SubnetId {
							edges = append(edges, s.createEdge(eniNode, natNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "nat_gateway_interface",
									"interface_type":  eniInfo.InterfaceType,
								}, req))
							break
						}
					}
				}
			}
		}
	}

	s.logger.Info("Created ENI edges",
		"db_eni_count", len(nodes),
		"valid_eni_count", len(validENINodes),
		"edges_created", len(edges))

	return validENINodes, edges
}

// createENINodeFromAWSData creates a new ENI node from AWS CLI data
func (s *AWSSource) createENINodeFromAWSData(eniInfo *ENINetworkInterface, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties from AWS data
	properties := make(map[string]interface{})
	properties["name"] = eniInfo.NetworkInterfaceId
	properties["type"] = "network-interface"
	properties["status"] = eniInfo.Status
	properties["cloud_provider"] = "AWS"
	properties["region"] = req.Region
	properties["resource_id"] = eniInfo.NetworkInterfaceId
	properties["service_name"] = "AmazonVPC"
	properties["is_active"] = true
	properties["external_resource_id"] = eniInfo.NetworkInterfaceId
	properties["description"] = eniInfo.Description
	properties["interface_type"] = eniInfo.InterfaceType
	properties["private_ip_address"] = eniInfo.PrivateIpAddress
	if len(eniInfo.PrivateIpAddresses) > 0 {
		ips := make([]string, 0, len(eniInfo.PrivateIpAddresses))
		for _, p := range eniInfo.PrivateIpAddresses {
			if p.PrivateIpAddress != "" {
				ips = append(ips, p.PrivateIpAddress)
			}
		}
		if len(ips) > 0 {
			properties["private_ips"] = ips
		}
	}
	properties["availability_zone"] = eniInfo.AvailabilityZone
	properties["requester_id"] = eniInfo.RequesterId

	// Add VPC and Subnet IDs
	if eniInfo.VpcId != "" {
		properties["vpc_id"] = eniInfo.VpcId
	}
	if eniInfo.SubnetId != "" {
		properties["subnet_id"] = eniInfo.SubnetId
	}

	// Extract security groups
	if len(eniInfo.Groups) > 0 {
		securityGroups := make([]map[string]string, 0, len(eniInfo.Groups))
		for _, group := range eniInfo.Groups {
			securityGroups = append(securityGroups, map[string]string{
				"GroupId":   group.GroupId,
				"GroupName": group.GroupName,
			})
		}
		properties["security_groups"] = securityGroups
	}

	// Extract attachment info
	if eniInfo.Attachment != nil {
		properties["attachment"] = map[string]interface{}{
			"attachment_id":         eniInfo.Attachment.AttachmentId,
			"instance_id":           eniInfo.Attachment.InstanceId,
			"device_index":          eniInfo.Attachment.DeviceIndex,
			"status":                eniInfo.Attachment.Status,
			"delete_on_termination": eniInfo.Attachment.DeleteOnTermination,
		}
	}

	// Extract tags
	if len(eniInfo.TagSet) > 0 {
		tags := make(map[string]string)
		for _, tag := range eniInfo.TagSet {
			tags[tag.Key] = tag.Value
		}
		properties["tags"] = tags
	}

	// Mark this node as dynamically created from AWS
	properties["source"] = "aws_cli"
	properties["created_from_live_data"] = true

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNetworkInterface,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(
		core.NodeTypeNetworkInterface,
		uniqueKey,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"aws",
	)
}

// fetchENIDataFromAWS fetches all ENI metadata from the in-memory meta cache.
func (s *AWSSource) fetchENIDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]*ENINetworkInterface, error) {
	rows := s.metaByType["network-interface"]
	enis := make([]*ENINetworkInterface, 0, len(rows))
	for _, row := range rows {
		var eni ENINetworkInterface
		if err := unmarshalMetaInto(row, &eni); err != nil {
			s.logger.Warn("Failed to parse ENI meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		enis = append(enis, &eni)
	}
	s.logger.Info("Fetched ENI data from DB cache", "eni_count", len(enis))
	return enis, nil
}

// matchENIToRDS checks if an ENI belongs to a specific RDS instance/cluster
func (s *AWSSource) matchENIToRDS(eniInfo *ENINetworkInterface, rdsNode *core.DbNode) bool {
	// Check RDS tags in ENI
	rdsDBID := ""
	rdsClusterID := ""

	for _, tag := range eniInfo.TagSet {
		if tag.Key == "aws:rds:db-id" {
			rdsDBID = tag.Value
		}
		if tag.Key == "aws:rds:cluster-id" {
			rdsClusterID = tag.Value
		}
	}

	// Match by RDS instance name or resource ID
	if rdsDBID != "" {
		if name, ok := getStringProperty(rdsNode, "name"); ok && name == rdsDBID {
			return true
		}
		if resourceID, ok := getStringProperty(rdsNode, "resource_id"); ok && resourceID == rdsDBID {
			return true
		}
	}

	// Match by RDS cluster name
	if rdsClusterID != "" {
		if name, ok := getStringProperty(rdsNode, "name"); ok && name == rdsClusterID {
			return true
		}
		if resourceID, ok := getStringProperty(rdsNode, "resource_id"); ok && resourceID == rdsClusterID {
			return true
		}
	}

	// Match by subnet (same subnet as RDS)
	if eniInfo.SubnetId != "" {
		if rdsMeta, ok := getMetadataMap(rdsNode); ok {
			if dbSubnetGroup, ok := rdsMeta["DBSubnetGroup"].(map[string]interface{}); ok {
				if subnets, ok := dbSubnetGroup["Subnets"].([]interface{}); ok {
					for _, subnet := range subnets {
						if subnetMap, ok := subnet.(map[string]interface{}); ok {
							if subnetID, ok := subnetMap["SubnetIdentifier"].(string); ok {
								if subnetID == eniInfo.SubnetId {
									return true
								}
							}
						}
					}
				}
			}
		}
	}

	return false
}
