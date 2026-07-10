package aws

import (
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// vpcEndpointSchema — concrete schema for VPC endpoints (NodeTypePrivateEndpoint).
// vpc_id / endpoint type / target service / state are enriched during edge building
// (createPrivateEndpointEdges).
var vpcEndpointSchema = core.SpecificTypeSchema{
	SpecificType: "VPCEndpoint",
	NodeType:     core.NodeTypePrivateEndpoint,
	Properties: []core.PropertyDef{
		{Name: "vpc_id"},
		{Name: "vpc_endpoint_type"},
		{Name: "target_service_name"},
		{Name: "private_dns_enabled"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "service_region"},
		{Name: "policy_document"},
		{Name: "route_table_ids"},
		{Name: "subnet_ids"},
		{Name: "network_interface_ids"},
		{Name: "dns_entries"},
		{Name: "requester_managed"},
		{Name: "ip_address_type"},
		{Name: "owner_id"},
		{Name: "creation_timestamp"},
	},
}

func init() { core.RegisterSpecificTypeSchema(vpcEndpointSchema) }

// createPrivateEndpointEdges creates edges for VPC Endpoints (Private Endpoints)
// Handles both Interface and Gateway endpoint types
func (s *AWSSource) createPrivateEndpointEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Get VPC Endpoint ID
		vpcEndpointID, _ := node.Properties["resource_id"].(string)
		if vpcEndpointID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				vpcEndpointID = name
			}
		}

		// Fetch VPC Endpoint details from AWS CLI
		var endpointData *PrivateEndpointData
		if vpcEndpointID != "" && req.CloudAccountID != "" {
			s.logger.Info("Fetching VPC Endpoint details from AWS",
				"vpc_endpoint_id", vpcEndpointID,
				"account_id", req.CloudAccountID)

			data, err := s.fetchPrivateEndpointDataFromAWS(reqCtx, req, req.CloudAccountID, vpcEndpointID)
			if err != nil {
				s.logger.Error("Failed to fetch VPC Endpoint data from AWS",
					"vpc_endpoint_id", vpcEndpointID,
					"error", err)
			} else {
				endpointData = data
				// Store useful properties in the node
				node.Properties["vpc_id"] = data.VpcId
				node.Properties["vpc_endpoint_type"] = data.VpcEndpointType
				node.Properties["target_service_name"] = data.ServiceName
				node.Properties["private_dns_enabled"] = data.PrivateDnsEnabled
				node.Properties["state"] = data.State

				s.logger.Info("Successfully enriched VPC Endpoint node with AWS CLI data",
					"vpc_endpoint_id", vpcEndpointID,
					"vpc_endpoint_type", data.VpcEndpointType,
					"target_service_name", data.ServiceName)
			}
		}

		if endpointData == nil {
			// Try to get VPC ID from existing properties
			vpcID, _ := node.Properties["vpc_id"].(string)
			if vpcID != "" {
				// Create VPC edge with just VPC ID
				if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "vpc"}, req))
				}
			}
			continue
		}

		// 1. PrivateEndpoint → VPC relationship
		if endpointData.VpcId != "" {
			if vpcNode, exists := lookup.ByResourceID[endpointData.VpcId]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// For Interface endpoints only (Gateway endpoints don't have subnets, ENIs, or security groups)
		if endpointData.VpcEndpointType == "Interface" {
			// 2. PrivateEndpoint → Subnet relationships
			for _, subnetID := range endpointData.SubnetIds {
				if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "subnet",
							"subnet_id":       subnetID,
						}, req))
				}
			}

			// 3. PrivateEndpoint → ENI (Network Interface) relationships
			for _, eniID := range endpointData.NetworkInterfaceIds {
				if eniNode, exists := lookup.ByResourceID[eniID]; exists {
					edges = append(edges, s.createEdge(node, eniNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":      "network_interface",
							"network_interface_id": eniID,
						}, req))
				} else {
					s.logger.Warn("PrivateEndpoint ENI not found in lookup", "vpc_endpoint_id", node.Properties["resource_id"], "eni_id", eniID)
				}
			}

			// 4. SecurityGroup → PrivateEndpoint relationships (PROTECTS)
			for _, group := range endpointData.Groups {
				if sgNode, exists := lookup.ByResourceID[group.GroupId]; exists {
					edges = append(edges, s.createEdge(sgNode, node, core.RelationshipProtects,
						map[string]interface{}{
							"security_group_id":   group.GroupId,
							"security_group_name": group.GroupName,
						}, req))
				}
			}
		}

		// 5. PrivateEndpoint → Target Service relationship (ROUTES_TO)
		// Match based on service name pattern to existing nodes
		targetServiceEdges := s.createPrivateEndpointServiceEdges(node, endpointData, lookup, req)
		edges = append(edges, targetServiceEdges...)
	}

	return edges
}

// createPrivateEndpointServiceEdges creates edges from PrivateEndpoint to target AWS services
// based on the ServiceName (e.g., com.amazonaws.us-east-1.s3)
func (s *AWSSource) createPrivateEndpointServiceEdges(node *core.DbNode, endpointData *PrivateEndpointData, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	serviceName := endpointData.ServiceName
	if serviceName == "" {
		return edges
	}

	// Parse service name pattern: com.amazonaws.{region}.{service}
	// Examples:
	// - com.amazonaws.us-east-1.s3
	// - com.amazonaws.us-east-1.dynamodb
	// - com.amazonaws.us-east-1.ecr.api
	// - com.amazonaws.us-east-1.ecr.dkr
	// - com.amazonaws.us-east-1.logs
	// - com.amazonaws.us-east-1.monitoring
	// - com.amazonaws.us-east-1.sns
	// - com.amazonaws.us-east-1.sqs
	// - com.amazonaws.us-east-1.elasticache

	var targetNodeType core.NodeType
	var serviceType string

	switch {
	case strings.Contains(serviceName, ".s3"):
		targetNodeType = core.NodeTypeStorage
		serviceType = "S3"
	case strings.Contains(serviceName, ".dynamodb"):
		targetNodeType = core.NodeTypeDatabase
		serviceType = "DynamoDB"
	case strings.Contains(serviceName, ".ecr"):
		targetNodeType = core.NodeTypeContainerRegistry
		serviceType = "ECR"
	case strings.Contains(serviceName, ".logs"):
		targetNodeType = core.NodeTypeLogAggregator
		serviceType = "CloudWatchLogs"
	case strings.Contains(serviceName, ".monitoring"):
		targetNodeType = core.NodeTypeMonitoringService
		serviceType = "CloudWatch"
	case strings.Contains(serviceName, ".sns"):
		targetNodeType = core.NodeTypeTopic
		serviceType = "SNS"
	case strings.Contains(serviceName, ".sqs"):
		targetNodeType = core.NodeTypeMessageQueue
		serviceType = "SQS"
	case strings.Contains(serviceName, ".elasticache"):
		targetNodeType = core.NodeTypeCache
		serviceType = "ElastiCache"
	case strings.Contains(serviceName, ".rds"):
		targetNodeType = core.NodeTypeDatabase
		serviceType = "RDS"
	case strings.Contains(serviceName, ".lambda"):
		targetNodeType = core.NodeTypeServerlessFunction
		serviceType = "Lambda"
	case strings.Contains(serviceName, ".secretsmanager"):
		targetNodeType = core.NodeTypeSecretVault
		serviceType = "SecretsManager"
	case strings.Contains(serviceName, ".kms"):
		targetNodeType = core.NodeTypeEncryptionKey
		serviceType = "KMS"
	default:
		// For external services (MongoDB Atlas, Datadog, etc.) or unknown services
		// Log for visibility but don't create edges to non-existent nodes
		s.logger.Debug("Unknown VPC Endpoint service type, skipping target service edge",
			"service_name", serviceName,
			"vpc_endpoint_id", endpointData.VpcEndpointId)
		return edges
	}

	// Find matching nodes of the target type in the same account
	if targetNodes, exists := lookup.ByNodeType[targetNodeType]; exists {
		for _, targetNode := range targetNodes {
			// Create edge to each node of the matching type
			// The edge indicates that the VPC Endpoint provides private access to these services
			edges = append(edges, s.createEdge(node, targetNode, core.RelationshipRoutesTo,
				map[string]interface{}{
					"connection_type":     "private_endpoint",
					"target_service_type": serviceType,
					"service_name":        serviceName,
				}, req))
		}

		if len(targetNodes) > 0 {
			s.logger.Info("Created private endpoint to service edges",
				"vpc_endpoint_id", endpointData.VpcEndpointId,
				"service_type", serviceType,
				"target_node_count", len(targetNodes))
		}
	}

	return edges
}

// fetchPrivateEndpointDataFromAWS fetches VPC Endpoint metadata from the in-memory meta cache.
func (s *AWSSource) fetchPrivateEndpointDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string, vpcEndpointID string) (*PrivateEndpointData, error) {
	row, ok := s.metaByTypeAndID["vpc-endpoint"][vpcEndpointID]
	if !ok {
		return nil, fmt.Errorf("VPC Endpoint %s not found in loaded resources", vpcEndpointID)
	}
	var ep PrivateEndpointData
	if err := unmarshalMetaInto(row, &ep); err != nil {
		return nil, fmt.Errorf("failed to parse VPC Endpoint meta: %w", err)
	}
	s.logger.Info("Fetched VPC Endpoint data from DB cache",
		"vpc_endpoint_id", vpcEndpointID,
		"vpc_endpoint_type", ep.VpcEndpointType,
		"service_name", ep.ServiceName,
		"state", ep.State)
	return &ep, nil
}
