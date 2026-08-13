package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// kmsKeySchema — concrete schema for KMS keys (NodeTypeEncryptionKey). Key state/usage
// are carried by base properties; no resource-specific fields are lifted at creation.
var kmsKeySchema = core.SpecificTypeSchema{
	SpecificType: "KMSKey",
	NodeType:     core.NodeTypeEncryptionKey,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "description"},
		{Name: "enabled"},
		{Name: "key_state"},
		{Name: "key_usage"},
		{Name: "key_manager"},
		{Name: "origin"},
		{Name: "creation_date"},
		{Name: "deletion_date"},
		{Name: "valid_to"},
		{Name: "custom_key_store_id"},
		{Name: "cloud_hsm_cluster_id"},
		{Name: "expiration_model"},
		{Name: "customer_master_key_spec"},
		{Name: "encryption_algorithms"},
		{Name: "signing_algorithms"},
		{Name: "anonymous_access"},
		{Name: "anonymous_actions"},
	},
}

func init() { core.RegisterSpecificTypeSchema(kmsKeySchema) }

// createKMSEdges creates edges from resources to KMS keys they use for encryption
func (s *AWSSource) createKMSEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build a map of KMS key ARN/ID to node for quick lookup
	kmsLookup := make(map[string]*core.DbNode)
	if kmsNodes, exists := lookup.ByNodeType[core.NodeTypeEncryptionKey]; exists {
		for _, node := range kmsNodes {
			// Index by ARN
			if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
				kmsLookup[arn] = node
			}
			// Index by resource_id (key ID)
			if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
				kmsLookup[resourceID] = node
			}
		}
	}

	// If no KMS keys found, return early
	if len(kmsLookup) == 0 {
		return edges
	}

	// Check all node types for KMS key references
	for nodeType, nodeList := range lookup.ByNodeType {
		switch nodeType {
		case core.NodeTypeDatabase:
			// Handle RDS, Aurora, etc.
			edges = append(edges, s.createKMSEdgesForDatabases(nodeList, kmsLookup, req)...)

		case core.NodeTypeCloudResource:
			// Handle EBS volumes and other storage resources
			edges = append(edges, s.createKMSEdgesForStorage(nodeList, kmsLookup, req)...)

		case core.NodeTypeLogAggregator:
			// Handle CloudWatch Log Groups
			edges = append(edges, s.createKMSEdgesForCloudWatch(nodeList, kmsLookup, req)...)

		default:
			// Check any other resource type for KMS key references
			edges = append(edges, s.createKMSEdgesGeneric(nodeList, kmsLookup, req)...)
		}
	}

	return edges
}

// createKMSEdgesForDatabases creates edges from RDS instances/clusters to their KMS keys
func (s *AWSSource) createKMSEdgesForDatabases(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for storage encryption KMS key
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "storage",
					}, req))
			}
		}

		// Check for Performance Insights KMS key (RDS specific)
		if piKmsKeyId, ok := node.Properties["performance_insights_kms_key_id"].(string); ok && piKmsKeyId != "" {
			if kmsNode, exists := kmsLookup[piKmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "performance_insights",
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesForStorage creates edges from EBS volumes and other storage to their KMS keys
func (s *AWSSource) createKMSEdgesForStorage(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only process EBS volumes (service_name = AmazonEC2, type = storage/volume)
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "AmazonEC2" && serviceName != "AWSS3" && serviceName != "AmazonEFS" {
			continue
		}

		// Check if encrypted
		encrypted, _ := node.Properties["encrypted"].(bool)
		if !encrypted {
			continue
		}

		// Get KMS key ID
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				// Determine encryption type based on service
				var encryptionType string
				switch serviceName {
				case "AWSS3":
					encryptionType = "bucket"
				case "AmazonEFS":
					encryptionType = "filesystem"
				default:
					encryptionType = "volume"
				}

				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": encryptionType,
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesForCloudWatch creates edges from CloudWatch Log Groups to their KMS keys
func (s *AWSSource) createKMSEdgesForCloudWatch(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for KMS key in CloudWatch Log Groups
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "logs",
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesGeneric creates edges for any resource type with KmsKeyId in properties
func (s *AWSSource) createKMSEdgesGeneric(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for any KmsKeyId field in properties
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				// Get service name for context
				serviceName, _ := node.Properties["service_name"].(string)

				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "default",
						"service":         serviceName,
					}, req))
			}
		}
	}

	return edges
}
