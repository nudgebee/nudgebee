package aws

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// sqsQueueSchema — concrete schema for SQS queues.
// Fields written by extractQueueMetadata + the queue_type stamp in createNodeFromResource.
var sqsQueueSchema = core.SpecificTypeSchema{
	SpecificType: "SQSQueue",
	NodeType:     core.NodeTypeMessageQueue,
	Properties: []core.PropertyDef{
		{Name: "queue_type", Indexed: true},
		{Name: "message_retention_period", Indexed: true},
		{Name: "visibility_timeout", Indexed: true},
		{Name: "queue_url"},
		{Name: "queue_arn"},
		{Name: "delay_seconds"},
		{Name: "dead_letter_target_arn"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "url"},
		{Name: "created_timestamp"},
		{Name: "last_modified_timestamp"},
		{Name: "maximum_message_size"},
		{Name: "policy"},
		{Name: "receive_message_wait_time_seconds"},
		{Name: "redrive_policy_dead_letter_target_arn"},
		{Name: "redrive_policy_max_receive_count"},
		{Name: "kms_master_key_id"},
		{Name: "kms_data_key_reuse_period_seconds"},
		{Name: "fifo_queue"},
		{Name: "content_based_deduplication"},
		{Name: "deduplication_scope"},
		{Name: "fifo_throughput_limit"},
	},
}

// mskClusterSchema — MSK (Kafka) clusters share the NodeTypeMessageQueue ontology.
// Their describe metadata carries no SQS-style fields, so only the base/subtype apply.
var mskClusterSchema = core.SpecificTypeSchema{
	SpecificType: "MSKCluster",
	NodeType:     core.NodeTypeMessageQueue,
	Properties:   []core.PropertyDef{},
}

func init() {
	core.RegisterSpecificTypeSchema(sqsQueueSchema)
	core.RegisterSpecificTypeSchema(mskClusterSchema)
}

// extractQueueMetadata extracts essential fields for SQS queues
func (s *AWSSource) extractQueueMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Queue URL (critical for operations)
	if queueURL, ok := metaMap["QueueUrl"].(string); ok && queueURL != "" {
		properties["queue_url"] = queueURL
	}
	if queueArn, ok := metaMap["QueueArn"].(string); ok && queueArn != "" {
		properties["queue_arn"] = queueArn
	}

	// Message retention (important for data retention)
	if retention, ok := metaMap["MessageRetentionPeriod"].(string); ok && retention != "" {
		properties["message_retention_period"] = retention
	}

	// Visibility timeout (important for processing)
	if timeout, ok := metaMap["VisibilityTimeout"].(string); ok && timeout != "" {
		properties["visibility_timeout"] = timeout
	}

	// Delay seconds (important for delayed processing)
	if delay, ok := metaMap["DelaySeconds"].(string); ok && delay != "" {
		properties["delay_seconds"] = delay
	}

	// DLQ configuration (needed for dead letter queue relationship)
	if redrivePolicy, ok := metaMap["RedrivePolicy"].(string); ok && redrivePolicy != "" {
		// RedrivePolicy is a JSON string, parse it to extract the deadLetterTargetArn
		var policy map[string]interface{}
		if err := json.Unmarshal([]byte(redrivePolicy), &policy); err == nil {
			if dlqArn, ok := policy["deadLetterTargetArn"].(string); ok && dlqArn != "" {
				properties["dead_letter_target_arn"] = dlqArn
			}
		}
	}
}

// createSQSEdges creates edges for SQS queues (MessageQueue -> DLQ, receive from S3, etc.)
func (s *AWSSource) createSQSEdges(_ *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// 1. Handle Dead Letter Queue (DLQ) relationships: SQS -> DLQ
		// dead_letter_target_arn is pre-extracted by extractQueueMetadata from RedrivePolicy.
		// Raw meta is not stored in node.Properties, so we read the flattened field directly.
		if dlqArn, ok := node.Properties["dead_letter_target_arn"].(string); ok && dlqArn != "" {
			if dlqNode, exists := lookup.ByARN[dlqArn]; exists {
				edges = append(edges, s.createEdge(node, dlqNode, core.RelationshipRoutesTo,
					map[string]interface{}{
						"connection_type": "dead_letter_queue",
					}, req))
				s.logger.Debug("created SQS to DLQ edge",
					"source_queue", node.Properties["name"],
					"dlq", dlqNode.Properties["name"])
			} else {
				s.logger.Debug("DLQ not found in lookup for SQS queue",
					"queue", node.Properties["name"],
					"dlq_arn", dlqArn)
			}
		}

		// Note: Lambda event source mappings (Lambda -> SQS) are handled in createLambdaEdges
		// S3 event notifications (S3 -> SQS) are handled in createS3Edges
	}

	return edges
}
