package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// s3BucketSchema — concrete schema for S3 buckets. Encryption/versioning flags drive
// the ontology.
var s3BucketSchema = core.SpecificTypeSchema{
	SpecificType: "S3Bucket",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "encryption_enabled", Indexed: true},
		{Name: "versioning_enabled", Indexed: true},
		{Name: "bucket_name"},
		{Name: "bucket_region"},
		{Name: "notification_topic_arns"},
		{Name: "notification_queue_arns"},
		{Name: "notification_lambda_arns"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "creation_date"},
	},
}

func init() { core.RegisterSpecificTypeSchema(s3BucketSchema) }

// extractStorageMetadata extracts essential fields for S3 buckets, EBS volumes, and EBS snapshots
func (s *AWSSource) extractStorageMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Check service_name and subtype to differentiate between S3, EBS volumes, and snapshots
	serviceName, _ := properties["service_name"].(string)
	subtype, _ := properties["subtype"].(string)

	if serviceName == "AmazonEC2" {
		if subtype == "snapshot" {
			// EBS snapshot metadata extraction
			s.extractSnapshotMetadata(properties, metaMap)
			return
		}
		// EBS volume metadata extraction
		s.extractEBSMetadata(properties, metaMap)
		return
	}

	// S3 bucket metadata extraction (default)
	// Bucket name (critical identifier)
	if bucketName, ok := metaMap["Name"].(string); ok && bucketName != "" {
		properties["bucket_name"] = bucketName
	}

	// Region (important for data locality)
	if region, ok := metaMap["Region"].(string); ok && region != "" {
		properties["bucket_region"] = region
	}

	// Versioning (important for data protection)
	if versioning, ok := metaMap["VersioningConfiguration"].(map[string]interface{}); ok {
		if status, ok := versioning["Status"].(string); ok && status != "" {
			properties["versioning_enabled"] = (status == "Enabled")
		}
	}

	// Encryption (important for security)
	if _, ok := metaMap["ServerSideEncryptionConfiguration"].(map[string]interface{}); ok {
		properties["encryption_enabled"] = true
	}

	// Notification configurations (needed for S3->SNS, S3->SQS, S3->Lambda relationships)
	if notifConfig, ok := metaMap["NotificationConfiguration"].(map[string]interface{}); ok {
		// Extract SNS topic ARNs
		if topicConfigs, ok := notifConfig["TopicConfigurations"].([]interface{}); ok && len(topicConfigs) > 0 {
			var topicARNs []string
			for _, config := range topicConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if topicArn, ok := cfg["TopicArn"].(string); ok && topicArn != "" {
						topicARNs = append(topicARNs, topicArn)
					}
				}
			}
			if len(topicARNs) > 0 {
				if arnsJSON, err := json.Marshal(topicARNs); err == nil {
					properties["notification_topic_arns"] = string(arnsJSON)
				}
			}
		}

		// Extract SQS queue ARNs
		if queueConfigs, ok := notifConfig["QueueConfigurations"].([]interface{}); ok && len(queueConfigs) > 0 {
			var queueARNs []string
			for _, config := range queueConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if queueArn, ok := cfg["QueueArn"].(string); ok && queueArn != "" {
						queueARNs = append(queueARNs, queueArn)
					}
				}
			}
			if len(queueARNs) > 0 {
				if arnsJSON, err := json.Marshal(queueARNs); err == nil {
					properties["notification_queue_arns"] = string(arnsJSON)
				}
			}
		}

		// Extract Lambda function ARNs
		if lambdaConfigs, ok := notifConfig["LambdaFunctionConfigurations"].([]interface{}); ok && len(lambdaConfigs) > 0 {
			var lambdaARNs []string
			for _, config := range lambdaConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if lambdaArn, ok := cfg["LambdaFunctionArn"].(string); ok && lambdaArn != "" {
						lambdaARNs = append(lambdaARNs, lambdaArn)
					}
				}
			}
			if len(lambdaARNs) > 0 {
				if arnsJSON, err := json.Marshal(lambdaARNs); err == nil {
					properties["notification_lambda_arns"] = string(arnsJSON)
				}
			}
		}
	}
}

// createS3Edges creates edges for S3 buckets (Storage -> SNS/SQS/Lambda for event notifications)
func (s *AWSSource) createS3Edges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := s.getS3MetaFromCache(node)
		if !hasMeta {
			continue
		}

		// Handle S3 event notifications: S3 -> SNS/SQS/Lambda
		// NotificationConfiguration structure: {
		//   "TopicConfigurations": [{"TopicArn": "arn:aws:sns:...", "Events": ["s3:ObjectCreated:*"]}],
		//   "QueueConfigurations": [{"QueueArn": "arn:aws:sqs:...", "Events": ["s3:ObjectCreated:*"]}],
		//   "LambdaFunctionConfigurations": [{"LambdaFunctionArn": "arn:aws:lambda:...", "Events": ["s3:ObjectCreated:*"]}]
		// }
		if notificationConfig, ok := meta["NotificationConfiguration"].(map[string]interface{}); ok {
			// 1. S3 -> SNS topic configurations
			if topicConfigs, ok := notificationConfig["TopicConfigurations"].([]interface{}); ok {
				for _, config := range topicConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						topicArn, _ := configMap["TopicArn"].(string)
						if topicArn != "" {
							if snsNode, exists := lookup.ByARN[topicArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to SNS edge",
									"s3_bucket", node.Properties["name"],
									"sns_topic", snsNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}

			// 2. S3 -> SQS queue configurations
			if queueConfigs, ok := notificationConfig["QueueConfigurations"].([]interface{}); ok {
				for _, config := range queueConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						queueArn, _ := configMap["QueueArn"].(string)
						if queueArn != "" {
							if sqsNode, exists := lookup.ByARN[queueArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, sqsNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to SQS edge",
									"s3_bucket", node.Properties["name"],
									"sqs_queue", sqsNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}

			// 3. S3 -> Lambda function configurations
			if lambdaConfigs, ok := notificationConfig["LambdaFunctionConfigurations"].([]interface{}); ok {
				for _, config := range lambdaConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						lambdaArn, _ := configMap["LambdaFunctionArn"].(string)
						if lambdaArn != "" {
							if lambdaNode, exists := lookup.ByARN[lambdaArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, lambdaNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to Lambda edge",
									"s3_bucket", node.Properties["name"],
									"lambda_function", lambdaNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}
		}
	}

	return edges
}

// getS3MetaFromCache reads S3-bucket meta from the per-build cache.
// Used by createS3Edges for the NotificationConfiguration → SNS/SQS path.
func (s *AWSSource) getS3MetaFromCache(node *core.DbNode) (map[string]interface{}, bool) {
	return s.metaFromCache(node, "storage")
}
