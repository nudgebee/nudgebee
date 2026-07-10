package aws

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// createNodeFromResource creates a knowledge graph node from a cloud resource.
// Returns nil when the row should be suppressed (eg an IAM Role that's already
// emitted as a typed ServiceIdentity); callers must skip nil returns.
func (s *AWSSource) createNodeFromResource(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	// Determine node type
	source := "aws"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	// IAM Roles are also emitted as typed ServiceIdentity nodes via
	// buildServiceIdentityNodes. The generic CloudResource fallback here would
	// create a duplicate node that every IAM-role-aware edge builder skips
	// (RUNS_AS / ASSUMES bind to ServiceIdentity via lookup.ByARN). Suppress
	// to avoid ~50 orphan nodes per AWS account.
	if nodeType == core.NodeTypeCloudResource &&
		resource.ServiceName == "AWSIAM" &&
		strings.EqualFold(resource.Type, "Role") {
		return nil
	}

	// IAM Users belong on the ServiceIdentity NodeType for the same reason
	// (cloud-agnostic identity), distinguished by subtype="IAMUser". The
	// audit at #31016 found 15 IAM User nodes sitting orphan in the
	// CloudResource catch-all per AWS account. Re-typing them gives them
	// the same first-class semantics IAM Roles get. IAM Groups remain
	// CloudResource — ServiceIdentity doesn't model groups today.
	if nodeType == core.NodeTypeCloudResource &&
		resource.ServiceName == "AWSIAM" &&
		strings.EqualFold(resource.Type, "User") {
		return s.createServiceIdentityFromIAMUser(resource, req)
	}

	// Build properties first (needed for unique key generation)
	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["labels"] = resource.Tags

	// Set ARN with validation (do not fall back to external_resource_id as it often contains malformed ARNs)
	arn := resource.ARN

	// Validate ARN format before using it (prevent malformed ARNs from bad data)
	// ARN format: arn:partition:service:region:account-id:resource-type/resource-id
	// SNS ARN format: arn:aws:sns:region:account-id:topic-name (exactly 6 parts)
	if arn != "" {
		arnParts := strings.Split(arn, ":")
		// Basic validation: ARN should have at least 6 parts
		if len(arnParts) < 6 {
			s.logger.Warn("Invalid ARN format (too few parts), skipping ARN",
				"resource_name", resource.Name,
				"arn", arn,
				"parts", len(arnParts))
			arn = "" // Clear invalid ARN
		} else if resource.ServiceName == "AmazonSNS" && len(arnParts) != 6 {
			// SNS ARNs must have exactly 6 parts
			s.logger.Warn("Invalid SNS ARN format (expected 6 parts), skipping ARN",
				"resource_name", resource.Name,
				"arn", arn,
				"parts", len(arnParts))
			arn = "" // Clear invalid SNS ARN
		}
	}

	properties["arn"] = arn
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID

	if resource.Type == "managedinstance" {
		properties["managed"] = true
	}

	// Add subtype for MessageQueue nodes based on service name
	if nodeType == core.NodeTypeMessageQueue {
		switch resource.ServiceName {
		case "AWSQueueService":
			properties["subtype"] = "SQS"
		case "AmazonMSK":
			properties["subtype"] = "MSK"
		default:
			properties["subtype"] = "MessageQueue"
		}
	}

	// Add subtype for Topic nodes based on service name
	if nodeType == core.NodeTypeTopic {
		switch resource.ServiceName {
		case "AmazonSNS":
			properties["subtype"] = "SNS"
		default:
			properties["subtype"] = "Topic"
		}
	}

	// Add subtype for Cache nodes based on service name
	if nodeType == core.NodeTypeCache {
		switch resource.ServiceName {
		case "AmazonElastiCache":
			properties["subtype"] = "ElastiCache"
		default:
			properties["subtype"] = "Cache"
		}
	}

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["aws_account_number"] = resource.AccountNumber

	// Parse and extract only essential metadata fields (node-type specific)
	// DO NOT store raw metadata to save space - only extract what's needed
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			// Extract only relevant fields based on node type
			s.extractEssentialMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
	}

	// Parse and add ALL tags (keep all tags for filtering and organization)
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
		}
	}

	// Add cache_type property for ElastiCache resources
	if resource.ServiceName == "AmazonElastiCache" {
		properties["cache_type"] = "elasticache"
	}

	// Add queue_type property for SQS resources
	if resource.ServiceName == "AWSQueueService" {
		properties["queue_type"] = "sqs"
	}

	// Add subtype property for all AWS resources (only if not already set)
	if _, exists := properties["subtype"]; !exists {
		properties["subtype"] = resource.Type
	}

	// Declare the concrete cloud label (specific_type). NewNode lifts this out of
	// properties into the dedicated column.
	properties["specific_type"] = s.determineSpecificType(resource.Type, resource.ServiceName, nodeType)

	// Store AWS account number in properties for unique key generation
	properties["account_number"] = resource.AccountNumber

	// Build unique key using new 6-part format: aws:account:region:NodeType:hierarchy:name
	// Create a temporary node to use GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, source)
}

// extractDNSNameFromMetadata extracts DNS name/endpoint from metadata with comprehensive fallback logic
func (s *AWSSource) extractDNSNameFromMetadata(metaMap map[string]interface{}) string {
	// LoadBalancer: DNSName
	if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
		return dnsName
	}

	// RDS Instance: Endpoint.Address
	if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
		if address, ok := endpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: ConfigurationEndpoint.Address
	if configEndpoint, ok := metaMap["ConfigurationEndpoint"].(map[string]interface{}); ok {
		if address, ok := configEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: ReaderEndpoint (can be string or object)
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(string); ok && readerEndpoint != "" {
		return readerEndpoint
	}
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(map[string]interface{}); ok {
		if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: PrimaryEndpoint (can be string or object)
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(string); ok && primaryEndpoint != "" {
		return primaryEndpoint
	}
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(map[string]interface{}); ok {
		if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: NodeGroups array (for replication groups)
	if nodeGroups, ok := metaMap["NodeGroups"].([]interface{}); ok && len(nodeGroups) > 0 {
		// Try first node group's PrimaryEndpoint
		if ng, ok := nodeGroups[0].(map[string]interface{}); ok {
			if primaryEndpoint, ok := ng["PrimaryEndpoint"].(map[string]interface{}); ok {
				if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
			// Try first node group's ReaderEndpoint
			if readerEndpoint, ok := ng["ReaderEndpoint"].(map[string]interface{}); ok {
				if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	// ElastiCache: CacheNodes array (for single cluster)
	if cacheNodes, ok := metaMap["CacheNodes"].([]interface{}); ok && len(cacheNodes) > 0 {
		// Try first cache node's Endpoint
		if cn, ok := cacheNodes[0].(map[string]interface{}); ok {
			if endpoint, ok := cn["Endpoint"].(map[string]interface{}); ok {
				if address, ok := endpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	return ""
}

// extractEssentialMetadataByNodeType extracts only essential metadata fields based on node type
// This prevents storing large metadata blobs and keeps only what's needed for each node type
func (s *AWSSource) extractEssentialMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	// Common fields extracted for all node types
	s.extractCommonMetadataFields(properties, metaMap)

	// Node-type specific extraction
	switch nodeType {
	case core.NodeTypeDatabase:
		s.extractDatabaseMetadata(properties, metaMap)
	case core.NodeTypeCache:
		s.extractCacheMetadata(properties, metaMap)
	case core.NodeTypeComputeInstance:
		s.extractComputeMetadata(properties, metaMap)
	case core.NodeTypeMessageQueue:
		s.extractQueueMetadata(properties, metaMap)
	case core.NodeTypeTopic:
		s.extractTopicMetadata(properties, metaMap)
	case core.NodeTypeStorage:
		s.extractStorageMetadata(properties, metaMap)
		// EFS-specific metadata extraction
		if serviceName == "AmazonEFS" {
			s.extractEFSMetadata(properties, metaMap)
		}
	case core.NodeTypeLoadBalancer:
		s.extractLoadBalancerMetadata(properties, metaMap)
	case core.NodeTypeManagedCluster:
		s.extractClusterMetadata(properties, metaMap)
	case core.NodeTypeServerlessFunction:
		s.extractLambdaMetadata(properties, metaMap)
	case core.NodeTypeCDN:
		s.extractCDNMetadata(properties, metaMap)
	case core.NodeTypeDNSZone:
		s.extractDNSZoneMetadata(properties, metaMap)
	case core.NodeTypeContainerRegistry:
		s.extractContainerRegistryMetadata(properties, metaMap)
	case core.NodeTypeNetworkGateway:
		s.extractNetworkGatewayMetadata(properties, metaMap)
	case core.NodeTypeNetworkInterface:
		s.extractENIMetadata(properties, metaMap)
	case core.NodeTypeSecurityGroup:
		s.extractSecurityGroupMetadata(properties, metaMap)
	case core.NodeTypeBackupVault:
		s.extractBackupVaultMetadata(properties, metaMap)
	case core.NodeTypeBackupPolicy:
		s.extractBackupPolicyMetadata(properties, metaMap)
	case core.NodeTypeInfraStack:
		// CloudFormation stack metadata (RoleARN, Parameters, Outputs)
		s.extractCloudFormationMetadata(properties, metaMap)
	case core.NodeTypeLogAggregator:
		// CloudTrail-specific metadata extraction
		if serviceName == "AWSCloudTrail" {
			s.extractCloudTrailMetadata(properties, metaMap)
		}
	case core.NodeTypeCloudResource:
		// Generic cloud resource - add specific extractors as needed
	}

	// Synthesize public DNS for AWS resources whose API metadata doesn't
	// expose one (S3/SQS/SNS/DDB/ECR/Lambda/Kinesis/APIGW). Runs after the
	// per-type extractors so anything that already wrote a real endpoint
	// (EFS/RDS/ElastiCache/LB/EKS/CloudFront) wins. Required for the
	// ExternalService enricher to match cross-account on dns_name without
	// having to extract bucket/queue identifiers from the hostname.
	synthesizeAWSEndpointDNS(properties)
}

// extractCommonMetadataFields extracts fields common to all node types
func (s *AWSSource) extractCommonMetadataFields(properties map[string]interface{}, metaMap map[string]interface{}) {
	// DNS name (important for connectivity)
	dnsName := s.extractDNSNameFromMetadata(metaMap)
	if dnsName != "" {
		properties["dns_name"] = dnsName
	}

	// VPC ID (important for network topology)
	// Note: AWS uses "VPCId" (uppercase) in LoadBalancer API responses
	if vpcID, ok := metaMap["VPCId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}

	// Security Groups (important for security analysis)
	if secGroups, ok := metaMap["SecurityGroups"].([]interface{}); ok && len(secGroups) > 0 {
		properties["security_groups"] = secGroups
	}

	// Availability Zones (important for HA analysis)
	if azs, ok := metaMap["AvailabilityZones"].([]interface{}); ok && len(azs) > 0 {
		properties["availability_zone"] = azs
	}

	// KMS Key ID (important for encryption relationships)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Performance Insights KMS Key ID (RDS specific)
	if piKmsKeyId, ok := metaMap["PerformanceInsightsKMSKeyId"].(string); ok && piKmsKeyId != "" {
		properties["performance_insights_kms_key_id"] = piKmsKeyId
	}

	// Encrypted flag (important for encryption analysis)
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// Subnet ID (important for network topology)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// VpcId (alternative casing for some AWS resources)
	if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}
}

// determineNodeType determines the knowledge graph node type from AWS resource type and service name
func (s *AWSSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// First, try exact match with type + service_name combination
	if serviceMap, exists := awsResourceTypeMap[resourceTypeLower]; exists {
		if nodeType, found := serviceMap[serviceName]; found {
			return nodeType
		}
	}

	// Second, try service name fallback
	if nodeType, exists := awsServiceFallbackMap[serviceName]; exists {
		return nodeType
	}

	// Default fallback for unmapped resources
	return core.NodeTypeCloudResource
}
