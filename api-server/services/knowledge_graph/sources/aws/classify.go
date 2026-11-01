package aws

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// awsResourceMetaSource returns the meta.nb_source marker ("billing", "api", …)
// for a cloud_resourses row, or "" when absent or unparseable. Mirrors
// gcpResourceMetaSource; see shouldIncludeResource for why AWS gates on it.
func awsResourceMetaSource(r *sources.CloudResourceRow) string {
	if r == nil || len(r.Meta) == 0 {
		return ""
	}
	// Decoded into a typed struct rather than map[string]interface{}: this runs
	// once per inventory row, and one account's billing ingest can carry 500k+
	// of them in a single build.
	var m struct {
		NbSource string `json:"nb_source"`
	}
	if err := json.Unmarshal(r.Meta, &m); err != nil {
		return ""
	}
	return m.NbSource
}

// createNodeFromResource creates a knowledge graph node from a cloud resource.
// Returns nil when the row should be suppressed (eg an IAM Role that's already
// emitted as a typed ServiceIdentity); callers must skip nil returns.
func (s *AWSSource) createNodeFromResource(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	source := "aws"
	kind := kindFor(resource.Type, resource.ServiceName)
	nodeType := kind.NodeType

	// A resource can declare that its rows build no node of their own (IAM
	// roles, which buildServiceIdentityNodes already emits as typed
	// ServiceIdentity nodes) or that they build a different node altogether
	// (IAM users, which belong on ServiceIdentity too).
	if kind.Suppress {
		return nil
	}
	if kind.Node != nil {
		return kind.Node(s, resource, req)
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

	// The concrete flavour of a shared ontology type (SQS vs MSK under
	// MessageQueue, SNS under Topic, ElastiCache under Cache).
	if kind.Subtype != "" {
		properties["subtype"] = kind.Subtype
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
			// Extract only the fields this resource declares
			s.extractMetadataForKind(properties, metaMap, kind)
		}
	}

	// Parse and add ALL tags (keep all tags for filtering and organization)
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
		}
	}

	// Static properties the resource stamps on every one of its nodes.
	for key, value := range kind.Props {
		properties[key] = value
	}

	// Add subtype property for all AWS resources (only if not already set)
	if _, exists := properties["subtype"]; !exists {
		properties["subtype"] = resource.Type
	}

	// Declare the concrete cloud label (specific_type). NewNode lifts this out of
	// properties into the dedicated column.
	properties["specific_type"] = kind.specificType()

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

// extractMetadataForKind extracts the metadata fields a node keeps: the ones
// common to every type, then the ones the resource's own extractor pulls out.
func (s *AWSSource) extractMetadataForKind(properties map[string]interface{}, metaMap map[string]interface{}, kind resourceKind) {
	s.extractCommonMetadataFields(properties, metaMap)

	if kind.Extract != nil {
		kind.Extract(s, properties, metaMap)
	}

	// Synthesize public DNS for AWS resources whose API metadata doesn't
	// expose one (S3/SQS/SNS/DDB/ECR/Lambda/Kinesis/APIGW). Runs after the
	// per-resource extractor so anything that already wrote a real endpoint
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
