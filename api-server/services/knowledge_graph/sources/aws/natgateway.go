package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// natGatewaySchema — concrete schema for NAT gateways. Fields written by
// extractNetworkGatewayMetadata.
var natGatewaySchema = core.SpecificTypeSchema{
	SpecificType: "NATGateway",
	NodeType:     core.NodeTypeNetworkGateway,
	Properties: []core.PropertyDef{
		{Name: "connectivity_type", Indexed: true},
		{Name: "nat_state", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "nat_gateway_id"},
		{Name: "subnet_id"},
		{Name: "network_interface_id"},
		{Name: "allocation_id"},
		{Name: "public_ip"},
		{Name: "private_ip"},
		{Name: "association_id"},
		{Name: "address_status"},
		{Name: "is_primary"},
		{Name: "create_time"},
	},
}

// internetGatewaySchema — Internet Gateways share NodeTypeNetworkGateway. Their VPC
// attachment (vpc_id) is set during route-table edge building.
var internetGatewaySchema = core.SpecificTypeSchema{
	SpecificType: "InternetGateway",
	NodeType:     core.NodeTypeNetworkGateway,
	Properties: []core.PropertyDef{
		{Name: "vpc_id", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "owner_id"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(natGatewaySchema)
	core.RegisterSpecificTypeSchema(internetGatewaySchema)
}

// extractNetworkGatewayMetadata extracts essential fields for NAT Gateways
func (s *AWSSource) extractNetworkGatewayMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// NAT Gateway ID (critical identifier)
	if natGatewayID, ok := metaMap["NatGatewayId"].(string); ok && natGatewayID != "" {
		properties["nat_gateway_id"] = natGatewayID
	}

	// State (available, pending, deleting, deleted, failed)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["nat_state"] = state
	}

	// Connectivity Type (public or private)
	if connectivityType, ok := metaMap["ConnectivityType"].(string); ok && connectivityType != "" {
		properties["connectivity_type"] = connectivityType
	}

	// Subnet ID (where NAT Gateway is deployed)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// VPC ID (parent VPC)
	if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}

	// NAT Gateway Addresses (includes ENI, EIP, private/public IPs)
	if addresses, ok := metaMap["NatGatewayAddresses"].([]interface{}); ok && len(addresses) > 0 {
		// Extract primary address info
		if primaryAddr, ok := addresses[0].(map[string]interface{}); ok {
			// Network Interface ID
			if eniID, ok := primaryAddr["NetworkInterfaceId"].(string); ok && eniID != "" {
				properties["network_interface_id"] = eniID
			}

			// Allocation ID (Elastic IP allocation)
			if allocationID, ok := primaryAddr["AllocationId"].(string); ok && allocationID != "" {
				properties["allocation_id"] = allocationID
			}

			// Public IP
			if publicIP, ok := primaryAddr["PublicIp"].(string); ok && publicIP != "" {
				properties["public_ip"] = publicIP
			}

			// Private IP
			if privateIP, ok := primaryAddr["PrivateIp"].(string); ok && privateIP != "" {
				properties["private_ip"] = privateIP
			}

			// Association ID (EIP association)
			if associationID, ok := primaryAddr["AssociationId"].(string); ok && associationID != "" {
				properties["association_id"] = associationID
			}

			// Is Primary address
			if isPrimary, ok := primaryAddr["IsPrimary"].(bool); ok {
				properties["is_primary"] = isPrimary
			}

			// Address Status
			if status, ok := primaryAddr["Status"].(string); ok && status != "" {
				properties["address_status"] = status
			}
		}

		// Store all addresses for reference
		properties["nat_gateway_addresses"] = addresses
	}

	// Create Time
	if createTime, ok := metaMap["CreateTime"].(string); ok && createTime != "" {
		properties["create_time"] = createTime
	}

	// Failure Code and Message (if failed)
	if failureCode, ok := metaMap["FailureCode"].(string); ok && failureCode != "" {
		properties["failure_code"] = failureCode
	}
	if failureMessage, ok := metaMap["FailureMessage"].(string); ok && failureMessage != "" {
		properties["failure_message"] = failureMessage
	}
}

// createNATGatewayEdges creates edges for NAT Gateways
// Fetches NAT Gateway metadata from AWS CLI if not present in database
func (s *AWSSource) createNATGatewayEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// NodeTypeNetworkGateway now includes both NAT GWs and Internet
		// Gateways. The IGW branch is handled in createRouteTableEdges;
		// skip IGW rows here so we don't fire `describe-nat-gateways` at
		// IGW IDs and burn 5s per RT on a doomed AWS call.
		resourceID, _ := node.Properties["resource_id"].(string)
		if strings.HasPrefix(resourceID, "igw-") {
			continue
		}

		// Check if we have required properties for edge creation
		vpcID, _ := node.Properties["vpc_id"].(string)
		subnetID, _ := node.Properties["subnet_id"].(string)
		natGatewayID, _ := node.Properties["nat_gateway_id"].(string)
		networkInterfaceID, _ := node.Properties["network_interface_id"].(string)

		// If properties are missing, try to fetch from AWS CLI.
		// Include networkInterfaceID in the check: NatGatewayAddresses (which contains
		// NetworkInterfaceId) may not be stored in DB meta even when vpc/subnet/nat ids are present.
		if (vpcID == "" || subnetID == "" || natGatewayID == "" || networkInterfaceID == "") && req.CloudAccountID != "" {
			// Get NAT Gateway ID from resource_id or existing property
			if natGatewayID == "" {
				if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
					natGatewayID = resourceID
				} else if name, ok := node.Properties["name"].(string); ok && name != "" {
					natGatewayID = name
				}
			}

			// The early-exit guard at the top of the loop checks resource_id, but
			// the name fallback above can still pick up an igw- id (some discovery
			// shapes store the id in name with resource_id empty). describe-nat-gateways
			// rejects igw- ids with NatGatewayMalformed, so skip here too.
			if strings.HasPrefix(natGatewayID, "igw-") {
				continue
			}

			if natGatewayID != "" {
				s.logger.Info("NAT Gateway properties missing, fetching from AWS",
					"node_id", node.ID,
					"nat_gateway_id", natGatewayID)

				// Fetch from AWS CLI
				natData, err := s.fetchNATGatewayDataFromAWS(reqCtx, req, req.CloudAccountID, natGatewayID)
				if err != nil {
					s.logger.Error("Failed to fetch NAT Gateway data from AWS",
						"nat_gateway_id", natGatewayID,
						"error", err)
				} else {
					// Create temp meta map for extraction
					tempMeta := map[string]interface{}{
						"NatGatewayId":        natData.NatGatewayId,
						"State":               natData.State,
						"SubnetId":            natData.SubnetId,
						"VpcId":               natData.VpcId,
						"CreateTime":          natData.CreateTime,
						"ConnectivityType":    natData.ConnectivityType,
						"NatGatewayAddresses": natData.NatGatewayAddresses,
					}

					// Extract fields to properties (without storing meta)
					s.extractNetworkGatewayMetadata(node.Properties, tempMeta)

					// Update local variables used in edge creation below
					vpcID, _ = node.Properties["vpc_id"].(string)
					subnetID, _ = node.Properties["subnet_id"].(string)
					// networkInterfaceID is re-read from node.Properties directly at edge 3, no update needed

					s.logger.Info("Successfully enriched NAT Gateway node with AWS CLI data",
						"nat_gateway_id", natGatewayID,
						"state", natData.State)
				}
			}
		}

		// 1. NAT Gateway → VPC relationship
		if vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			} else {
				s.logger.Warn("NAT Gateway VPC not found in lookup", "nat_id", node.Properties["resource_id"], "vpc_id", vpcID)
			}
		}

		// 2. NAT Gateway → Subnet relationship
		if subnetID != "" {
			if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			} else {
				s.logger.Warn("NAT Gateway Subnet not found in lookup", "nat_id", node.Properties["resource_id"], "subnet_id", subnetID)
			}
		}

		// 3. NAT Gateway → ENI (Network Interface) relationship
		if networkInterfaceID, ok := node.Properties["network_interface_id"].(string); ok && networkInterfaceID != "" {
			if eniNode, exists := lookup.ByResourceID[networkInterfaceID]; exists {
				edges = append(edges, s.createEdge(node, eniNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "network_interface",
						"interface_id":    networkInterfaceID,
					}, req))
			}
		}

		// 4. NAT Gateway → Elastic IP relationship
		if allocationID, ok := node.Properties["allocation_id"].(string); ok && allocationID != "" {
			// Check if EIP node exists in lookup (elastic-ip type)
			if eipNode, exists := lookup.ByResourceID[allocationID]; exists {
				edges = append(edges, s.createEdge(node, eipNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "elastic_ip",
						"allocation_id":   allocationID,
					}, req))
			} else {
				s.logger.Warn("NAT Gateway EIP not found in lookup", "nat_id", node.Properties["resource_id"], "allocation_id", allocationID)
			}
		}
	}

	return edges
}

// fetchNATGatewayDataFromAWS fetches NAT Gateway metadata from AWS using cloud collector CLI
func (s *AWSSource) fetchNATGatewayDataFromAWS(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string, natGatewayID string) (*NATGatewayData, error) {
	// Build AWS CLI command to describe specific NAT Gateway
	cmd := fmt.Sprintf("aws ec2 describe-nat-gateways --nat-gateway-ids %s --output json", natGatewayID)

	s.logger.Info("Fetching NAT Gateway data from AWS",
		"nat_gateway_id", natGatewayID,
		"account_id", accountID,
		"command", cmd)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	// Parse response
	var result struct {
		NatGateways []NATGatewayData `json:"NatGateways"`
	}

	// Log the raw response for debugging
	s.logger.Info("Cloud CLI response received", "response_keys", getMapKeys(resp))

	// Try different response formats
	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		// Try to see if the entire response is the JSON
		if respBytes, err := json.Marshal(resp); err == nil {
			s.logger.Error("Invalid response format from cloud CLI", "raw_response", string(respBytes))
		}
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field with string value")
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.logger.Error("Failed to parse NAT Gateway JSON", "error", err, "output_preview", sources.TruncateString(output, 200))
		return nil, fmt.Errorf("failed to parse NAT Gateway response: %w", err)
	}

	if len(result.NatGateways) == 0 {
		return nil, fmt.Errorf("NAT Gateway not found: %s", natGatewayID)
	}

	s.logger.Info("Successfully fetched NAT Gateway data from AWS",
		"nat_gateway_id", natGatewayID,
		"state", result.NatGateways[0].State)

	return &result.NatGateways[0], nil
}
