package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// cloudTrailSchema — concrete schema for CloudTrail trails (NodeTypeLogAggregator).
// Fields written by extractCloudTrailMetadata.
var cloudTrailSchema = core.SpecificTypeSchema{
	SpecificType: "CloudTrail",
	NodeType:     core.NodeTypeLogAggregator,
	Properties: []core.PropertyDef{
		{Name: "s3_bucket_name"},
		{Name: "s3_key_prefix"},
		{Name: "sns_topic_arn"},
		{Name: "cloudwatch_logs_log_group_arn"},
		{Name: "kms_key_id"},
		{Name: "is_multi_region_trail"},
		{Name: "is_organization_trail"},
		{Name: "home_region"},
		{Name: "log_file_validation_enabled"},
		{Name: "is_logging"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "cloudwatch_logs_role_arn"},
		{Name: "event_selectors"},
		{Name: "advanced_event_selectors"},
		{Name: "has_custom_event_selectors"},
		{Name: "has_insight_selectors"},
		{Name: "include_global_service_events"},
	},
}

// cloudTrailEventDataStoreSchema — CloudTrail Lake event data stores share the
// extractCloudTrailMetadata path; the EDS-specific fields are declared here.
var cloudTrailEventDataStoreSchema = core.SpecificTypeSchema{
	SpecificType: "CloudTrailEventDataStore",
	NodeType:     core.NodeTypeLogAggregator,
	Properties: []core.PropertyDef{
		{Name: "eds_status"},
		{Name: "retention_period_days"},
		{Name: "multi_region_enabled"},
		{Name: "termination_protection_enabled"},
		{Name: "kms_key_id"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(cloudTrailSchema)
	core.RegisterSpecificTypeSchema(cloudTrailEventDataStoreSchema)
}

// extractCloudTrailMetadata extracts essential fields for CloudTrail resources (Trails and Event Data Stores)
func (s *AWSSource) extractCloudTrailMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Trail: S3 bucket destination (for PUBLISHES_TO edge)
	if s3BucketName, ok := metaMap["S3BucketName"].(string); ok && s3BucketName != "" {
		properties["s3_bucket_name"] = s3BucketName
	}
	if s3KeyPrefix, ok := metaMap["S3KeyPrefix"].(string); ok && s3KeyPrefix != "" {
		properties["s3_key_prefix"] = s3KeyPrefix
	}

	// Trail: SNS topic for notifications (for PUBLISHES_TO edge)
	if snsTopicArn, ok := metaMap["SnsTopicARN"].(string); ok && snsTopicArn != "" {
		properties["sns_topic_arn"] = snsTopicArn
	}

	// Trail: CloudWatch Logs integration (for PUBLISHES_TO edge)
	if cwLogsArn, ok := metaMap["CloudWatchLogsLogGroupArn"].(string); ok && cwLogsArn != "" {
		properties["cloudwatch_logs_log_group_arn"] = cwLogsArn
	}

	// KMS encryption (for IS_ENCRYPTED_BY edge)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Trail configuration metadata
	if isMultiRegion, ok := metaMap["IsMultiRegionTrail"].(bool); ok {
		properties["is_multi_region_trail"] = isMultiRegion
	}
	if isOrgTrail, ok := metaMap["IsOrganizationTrail"].(bool); ok {
		properties["is_organization_trail"] = isOrgTrail
	}
	if homeRegion, ok := metaMap["HomeRegion"].(string); ok && homeRegion != "" {
		properties["home_region"] = homeRegion
	}
	if logValidation, ok := metaMap["LogFileValidationEnabled"].(bool); ok {
		properties["log_file_validation_enabled"] = logValidation
	}

	// Trail status (from TrailStatus sub-map)
	if trailStatus, ok := metaMap["TrailStatus"].(map[string]interface{}); ok {
		if isLogging, ok := trailStatus["IsLogging"].(bool); ok {
			properties["is_logging"] = isLogging
		}
	}

	// Event Data Store metadata
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["eds_status"] = status
	}
	if retentionPeriod, ok := metaMap["RetentionPeriod"].(float64); ok {
		properties["retention_period_days"] = int(retentionPeriod)
	}
	if multiRegionEnabled, ok := metaMap["MultiRegionEnabled"].(bool); ok {
		properties["multi_region_enabled"] = multiRegionEnabled
	}
	if termProtection, ok := metaMap["TerminationProtectionEnabled"].(bool); ok {
		properties["termination_protection_enabled"] = termProtection
	}
}

// createCloudTrailEdges creates edges for CloudTrail Trails and Event Data Stores
// CloudTrail -> S3 Bucket (PUBLISHES_TO): Log storage destination
// CloudTrail -> KMS Key (IS_ENCRYPTED_BY): Handled by createKMSEdges
// CloudTrail -> SNS Topic (PUBLISHES_TO): Notifications
// CloudTrail -> CloudWatch Log Group (PUBLISHES_TO): Log forwarding
func (s *AWSSource) createCloudTrailEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "AWSCloudTrail" {
			continue
		}

		nodeName, _ := node.Properties["name"].(string)

		// 1. CloudTrail -> S3 Bucket (PUBLISHES_TO)
		if s3BucketName, ok := node.Properties["s3_bucket_name"].(string); ok && s3BucketName != "" {
			for _, storageNode := range lookup.GetNodesByTypeAndName(core.NodeTypeStorage, s3BucketName) {
				if storageServiceName, ok := getStringProperty(storageNode, "service_name"); ok && storageServiceName == "AmazonS3" {
					edges = append(edges, s.createEdge(node, storageNode, core.RelationshipPublishesTo,
						map[string]interface{}{"connection_type": "cloudtrail_log_destination"}, req))
					s.logger.Debug("Created CloudTrail to S3 edge",
						"trail_name", nodeName,
						"s3_bucket", s3BucketName)
					break
				}
			}
		}

		// 2. CloudTrail -> KMS Key (IS_ENCRYPTED_BY)
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			// Look up KMS key by ARN or key ID
			if kmsNode, exists := lookup.ByARN[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "trail",
					}, req))
				s.logger.Debug("Created CloudTrail to KMS edge",
					"trail_name", nodeName,
					"kms_key_id", kmsKeyId)
			} else if kmsNode, exists := lookup.ByResourceID[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "trail",
					}, req))
				s.logger.Debug("Created CloudTrail to KMS edge",
					"trail_name", nodeName,
					"kms_key_id", kmsKeyId)
			}
		}

		// 3. CloudTrail -> SNS Topic (PUBLISHES_TO)
		if snsTopicArn, ok := node.Properties["sns_topic_arn"].(string); ok && snsTopicArn != "" {
			if snsNode, exists := lookup.ByARN[snsTopicArn]; exists {
				edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
					map[string]interface{}{"connection_type": "cloudtrail_notification"}, req))
				s.logger.Debug("Created CloudTrail to SNS edge",
					"trail_name", nodeName,
					"sns_topic_arn", snsTopicArn)
			}
		}

		// 4. CloudTrail -> CloudWatch Log Group (PUBLISHES_TO)
		if cwLogsArn, ok := node.Properties["cloudwatch_logs_log_group_arn"].(string); ok && cwLogsArn != "" {
			if cwNode, exists := lookup.ByARN[cwLogsArn]; exists {
				edges = append(edges, s.createEdge(node, cwNode, core.RelationshipPublishesTo,
					map[string]interface{}{"connection_type": "cloudtrail_cloudwatch_integration"}, req))
				s.logger.Debug("Created CloudTrail to CloudWatch edge",
					"trail_name", nodeName,
					"log_group_arn", cwLogsArn)
			}
		}
	}

	s.logger.Info("Created CloudTrail edges", "edges_created", len(edges))

	return edges
}
