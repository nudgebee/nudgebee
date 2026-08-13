package aws

import (
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// createVPCPeeringEdges builds VPC↔VPC peering edges from the account's AWS VPC peering
// connections, fetched live per region via the ec2 CLI (peering connections are not in
// the per-VPC DB meta). Mirrors the Azure VNet-peering edges (③). Only "active" peerings
// whose BOTH VPCs are present in this account's graph are linked; cross-account peers
// (accepter in another account) resolve via the cross-account rules engine.
func (s *AWSSource) createVPCPeeringEdges(reqCtx *security.RequestContext, vpcNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	regions := make(map[string]struct{})
	for _, n := range vpcNodes {
		if r, ok := n.Properties["region"].(string); ok && r != "" {
			regions[r] = struct{}{}
		}
	}

	var peerings [][]string
	for region := range regions {
		peerings = append(peerings, s.fetchVPCPeeringConnections(reqCtx, req.CloudAccountID, region)...)
	}
	return s.buildVPCPeeringEdges(peerings, lookup, req)
}

// fetchVPCPeeringConnections returns [requesterVpcId, accepterVpcId, statusCode] rows
// for one region via `aws ec2 describe-vpc-peering-connections`. Best-effort.
func (s *AWSSource) fetchVPCPeeringConnections(reqCtx *security.RequestContext, accountID, region string) [][]string {
	cmd := fmt.Sprintf(
		"aws ec2 describe-vpc-peering-connections --region %s --query 'VpcPeeringConnections[].[RequesterVpcInfo.VpcId,AccepterVpcInfo.VpcId,Status.Code]' --output json",
		region,
	)
	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{AccountID: accountID, Command: cmd}, 2)
	if err != nil {
		s.logger.Debug("ec2 describe-vpc-peering-connections failed", "region", region, "error", err)
		return nil
	}
	var rows [][]string
	if !parseAWSCliData(resp, &rows) {
		return nil
	}
	return rows
}

// buildVPCPeeringEdges is the pure (network-free) edge builder: requester VPC
// -ASSOCIATED_WITH(vpc_peering)-> accepter VPC for each active peering whose two VPCs
// both resolve to nodes.
func (s *AWSSource) buildVPCPeeringEdges(peerings [][]string, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	for _, p := range peerings {
		if len(p) != 3 || p[0] == "" || p[1] == "" || p[2] != "active" {
			continue
		}
		reqNode, ok1 := lookup.ByResourceID[p[0]]
		accNode, ok2 := lookup.ByResourceID[p[1]]
		if ok1 && ok2 && reqNode.ID != accNode.ID {
			edges = append(edges, s.createEdge(reqNode, accNode, core.RelationshipAssociatedWith,
				map[string]interface{}{"connection_type": "vpc_peering"}, req))
		}
	}
	return edges
}

// ec2VpcSchema — concrete schema for VPCs.
// cidr_block / vpc_state / is_default are populated on inferred VPC nodes (createInferredVPCNode).
var ec2VpcSchema = core.SpecificTypeSchema{
	SpecificType: "EC2Vpc",
	NodeType:     core.NodeTypeVPC,
	Properties: []core.PropertyDef{
		{Name: "cidr_block", Indexed: true},
		{Name: "vpc_state", Indexed: true},
		{Name: "is_default", Indexed: true},
		{Name: "vpc_id"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "primary_cidr_block"},
		{Name: "instance_tenancy"},
		{Name: "dhcp_options_id"},
	},
}

// ec2SubnetSchema — concrete schema for subnets (NodeTypeSubnet). CIDR is not lifted
// at creation; vpc_id links the subnet to its VPC.
var ec2SubnetSchema = core.SpecificTypeSchema{
	SpecificType: "EC2Subnet",
	NodeType:     core.NodeTypeSubnet,
	Properties: []core.PropertyDef{
		{Name: "vpc_id", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "subnet_arn"},
		{Name: "cidr_block"},
		{Name: "available_ip_address_count"},
		{Name: "default_for_az"},
		{Name: "map_customer_owned_ip_on_launch"},
		{Name: "assign_ipv6_address_on_creation"},
		{Name: "map_public_ip_on_launch"},
		{Name: "availability_zone"},
		{Name: "availability_zone_id"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(ec2VpcSchema)
	core.RegisterSpecificTypeSchema(ec2SubnetSchema)
}

// createInferredVPCNode creates an inferred VPC node with name fetched from AWS if available
func (s *AWSSource) createInferredVPCNode(reqCtx *security.RequestContext, vpcID string, req *core.SourceBuildRequest) *core.DbNode {
	vpcName := vpcID // Default to VPC ID
	cidrBlock := ""
	state := ""
	isDefault := false

	// Try to fetch VPC metadata from AWS to get the name and other details
	if req.CloudAccountID != "" && reqCtx != nil {
		vpcData, err := s.fetchVPCDataFromAWS(reqCtx, req, req.CloudAccountID, vpcID)
		if err != nil {
			s.logger.Warn("Failed to fetch VPC data from AWS, using VPC ID as name",
				"vpc_id", vpcID,
				"error", err)
		} else {
			// Extract VPC name from tags
			for _, tag := range vpcData.Tags {
				if tag.Key == "Name" && tag.Value != "" {
					vpcName = tag.Value
					break
				}
			}
			cidrBlock = vpcData.CidrBlock
			state = vpcData.State
			isDefault = vpcData.IsDefault

			s.logger.Info("Successfully enriched inferred VPC with AWS data",
				"vpc_id", vpcID,
				"vpc_name", vpcName,
				"state", state)
		}
	}

	properties := map[string]interface{}{
		"name":           vpcName,
		"vpc_id":         vpcID,
		"resource_id":    vpcID,
		"inferred":       true,
		"type":           "vpc",
		"subtype":        "vpc",
		"service_name":   "AmazonVPC",
		"cloud_provider": "AWS",
	}

	// Add optional fields if available
	if cidrBlock != "" {
		properties["cidr_block"] = cidrBlock
	}
	if state != "" {
		properties["vpc_state"] = state
	}
	if isDefault {
		properties["is_default"] = isDefault
	}

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeVPC,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeVPC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")
}

// createInferredSubnetNode creates an inferred subnet node when subnet is referenced but not in database
func (s *AWSSource) createInferredSubnetNode(subnetID string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":                 subnetID,
		"subnet_id":            subnetID,
		"resource_id":          subnetID,
		"inferred":             true,
		"type":                 "subnet_inferred",
		"subtype":              "subnet_inferred",
		"service_name":         "AmazonVPC",
		"cloud_provider":       "AWS",
		"external_resource_id": subnetID,
	}

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeSubnet,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")
}

// fetchVPCDataFromAWS fetches VPC metadata from the in-memory meta cache.
// Note: this will return "not found" for VPCs that are referenced by resources but not stored
// in cloud_resourses themselves (e.g. cross-account VPCs, un-synced VPCs). Callers handle
// this with a graceful fallback to the VPC ID as the node name.
func (s *AWSSource) fetchVPCDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string, vpcID string) (*VPCData, error) {
	row, ok := s.metaByTypeAndID["vpc"][vpcID]
	if !ok {
		return nil, fmt.Errorf("VPC %s not found in loaded resources", vpcID)
	}
	var vpc VPCData
	if err := unmarshalMetaInto(row, &vpc); err != nil {
		return nil, fmt.Errorf("failed to parse VPC meta: %w", err)
	}
	s.logger.Info("Fetched VPC data from DB cache", "vpc_id", vpcID, "state", vpc.State)
	return &vpc, nil
}

// createDefaultVPCEdges creates basic VPC relationship for node types without specific logic
func (s *AWSSource) createDefaultVPCEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// If node has vpc_id, connect it to the VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edge := core.NewEdge(
					node.ID,
					vpcNode.ID,
					core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "vpc",
					},
					req.TenantID,
					req.CloudAccountID,
					"aws",
				)
				edges = append(edges, edge)
			}
		}
	}

	return edges
}

// ensureVPCNodes ensures all referenced VPCs exist, creating inferred nodes if needed
func (s *AWSSource) ensureVPCNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, edges []*core.DbEdge, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Track which VPCs are referenced but don't exist
	referencedVPCs := make(map[string]bool)

	// Collect all vpc_id references from nodes
	for _, node := range nodes {
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			referencedVPCs[vpcID] = true
		}
	}

	// Create inferred VPC nodes for missing VPCs
	for vpcID := range referencedVPCs {
		if _, exists := lookup.ByResourceID[vpcID]; !exists {
			inferredVPC := s.createInferredVPCNode(reqCtx, vpcID, req)
			nodes = append(nodes, inferredVPC)
			lookup.ByResourceID[vpcID] = inferredVPC
			lookup.ByNodeType[core.NodeTypeVPC] = append(lookup.ByNodeType[core.NodeTypeVPC], inferredVPC)
		}
	}

	return nodes, edges
}

// ensureSubnetNodes ensures all referenced subnet nodes exist, creating inferred nodes if needed
func (s *AWSSource) ensureSubnetNodes(nodes []*core.DbNode, edges []*core.DbEdge, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Track which Subnets are referenced but don't exist
	referencedSubnets := make(map[string]bool)

	// Collect all subnet_id references from nodes
	for _, node := range nodes {
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			referencedSubnets[subnetID] = true
		}

		// Also check in metadata for SubnetId
		if meta, ok := getMetadataMap(node); ok {
			if subnetID, ok := meta["SubnetId"].(string); ok && subnetID != "" {
				referencedSubnets[subnetID] = true
			}
		}
	}

	// Create inferred Subnet nodes for missing Subnets
	for subnetID := range referencedSubnets {
		if _, exists := lookup.ByResourceID[subnetID]; !exists {
			inferredSubnet := s.createInferredSubnetNode(subnetID, req)
			nodes = append(nodes, inferredSubnet)
			lookup.ByResourceID[subnetID] = inferredSubnet
			lookup.ByNodeType[core.NodeTypeSubnet] = append(lookup.ByNodeType[core.NodeTypeSubnet], inferredSubnet)
		}
	}

	return nodes, edges
}

// propagateVPCNamesToResources updates all resources to use VPC name in hierarchy instead of VPC ID
func (s *AWSSource) propagateVPCNamesToResources(nodes []*core.DbNode, lookup *sources.NodeLookup) {
	for _, node := range nodes {
		// Skip VPC nodes themselves
		if node.NodeType == core.NodeTypeVPC {
			continue
		}

		// Check if this node has a vpc_id
		vpcID, ok := node.Properties["vpc_id"].(string)
		if !ok || vpcID == "" {
			continue
		}

		// Look up the VPC node to get its name
		vpcNode, exists := lookup.ByResourceID[vpcID]
		if !exists {
			continue
		}

		// Get VPC name from the VPC node
		vpcName, ok := vpcNode.Properties["name"].(string)
		if !ok || vpcName == "" {
			// Fallback to VPC ID if name not available
			vpcName = vpcID
		}

		// Store VPC name in the resource node properties for hierarchy
		node.Properties["vpc_name_hierarchy"] = vpcName

		// Regenerate unique key for this node to use VPC name in hierarchy
		node.UniqueKey = s.GenerateUniqueKey(node)

		s.logger.Debug("Updated resource hierarchy to use VPC name",
			"resource_name", node.Properties["name"],
			"vpc_id", vpcID,
			"vpc_name", vpcName,
			"new_unique_key", node.UniqueKey)
	}
}
