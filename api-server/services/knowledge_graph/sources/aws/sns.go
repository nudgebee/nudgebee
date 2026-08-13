package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// snsTopicSchema — concrete schema for SNS topics.
var snsTopicSchema = core.SpecificTypeSchema{
	SpecificType: "SNSTopic",
	NodeType:     core.NodeTypeTopic,
	Properties: []core.PropertyDef{
		{Name: "topic_arn"},
		{Name: "display_name"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "topic_name"},
		{Name: "owner"},
		{Name: "subscriptions_pending"},
		{Name: "subscriptions_confirmed"},
		{Name: "subscriptions_deleted"},
		{Name: "delivery_policy"},
		{Name: "effective_delivery_policy"},
		{Name: "kms_master_key_id"},
	},
}

func init() { core.RegisterSpecificTypeSchema(snsTopicSchema) }

// extractTopicMetadata extracts essential fields for SNS topics
func (s *AWSSource) extractTopicMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Topic ARN (critical for operations)
	if topicArn, ok := metaMap["TopicArn"].(string); ok && topicArn != "" {
		properties["topic_arn"] = topicArn
	}

	// Display name (useful for UI)
	if displayName, ok := metaMap["DisplayName"].(string); ok && displayName != "" {
		properties["display_name"] = displayName
	}

	// Subscriptions count (important for fan-out analysis) - extracted during edge creation
}

// createSNSEdges creates edges for SNS topics (Topic -> MessageQueue, ServerlessFunction, etc.)
// Fetches ALL subscription data from AWS in one call for efficiency
func (s *AWSSource) createSNSEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Fetch ALL SNS subscriptions at once (much more efficient than per-topic calls)
	allSubscriptionsMap, err := s.fetchAllSNSSubscriptions(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Warn("failed to fetch all SNS subscriptions from AWS, falling back to metadata for all topics",
			"error", err)
		// Continue with metadata fallback for each topic below
	}

	for _, node := range nodes {
		// Get topic ARN from node properties
		topicArn, hasArn := node.Properties["arn"].(string)
		if !hasArn || topicArn == "" {
			s.logger.Debug("SNS topic missing ARN, skipping",
				"topic_name", node.Properties["name"])
			continue
		}

		// Get subscriptions for this topic from the batch fetch
		var subscriptions []interface{}
		if allSubscriptionsMap != nil {
			if topicSubs, exists := allSubscriptionsMap[topicArn]; exists {
				subscriptions = topicSubs
				s.logger.Debug("Using batch-fetched subscriptions for SNS topic",
					"topic_name", node.Properties["name"],
					"subscription_count", len(subscriptions))
			}
		}

		// Fallback to metadata if no subscriptions found from AWS.
		// NOTE: this branch is currently dead — Topic nodes built by
		// createNodeFromResource don't carry raw "meta" on Properties (see
		// comment near line 732). The primary AWS-fetch path above is doing
		// all the work today. Left in place because it's harmless, but a
		// future fix should either route this through metaFromCache(node, "topic")
		// or delete it. Tracked under the sibling KG edge-builder ticket.
		if len(subscriptions) == 0 {
			meta, hasMeta := getMetadataMap(node)
			if hasMeta {
				if metaSubscriptions, ok := meta["Subscriptions"].([]interface{}); ok {
					subscriptions = metaSubscriptions
					s.logger.Debug("Using metadata subscriptions for SNS topic",
						"topic_name", node.Properties["name"],
						"subscription_count", len(subscriptions))
				}
			}
		}

		// Process subscriptions to create edges
		for _, sub := range subscriptions {
			if subMap, ok := sub.(map[string]interface{}); ok {
				protocol, _ := subMap["Protocol"].(string)
				endpoint, _ := subMap["Endpoint"].(string)
				subscriptionArn, _ := subMap["SubscriptionArn"].(string)

				// Skip pending confirmations
				if subscriptionArn == "PendingConfirmation" {
					continue
				}

				// Handle SQS subscriptions: SNS -> SQS
				if protocol == "sqs" && endpoint != "" {
					// Look up SQS queue by ARN (endpoint is the queue ARN)
					if sqsNode, exists := lookup.ByARN[endpoint]; exists {
						edges = append(edges, s.createEdge(node, sqsNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type":  "sns_subscription",
								"protocol":         protocol,
								"subscription_arn": subscriptionArn,
							}, req))
						s.logger.Debug("created SNS to SQS edge from AWS data",
							"sns_topic", node.Properties["name"],
							"sqs_queue", sqsNode.Properties["name"],
							"protocol", protocol)
					} else {
						s.logger.Debug("SQS queue not found in lookup for SNS subscription",
							"sns_topic", node.Properties["name"],
							"queue_arn", endpoint)
					}
				}

				// Handle Lambda subscriptions: SNS -> Lambda
				if protocol == "lambda" && endpoint != "" {
					// Look up Lambda function by ARN (endpoint is the function ARN)
					if lambdaNode, exists := lookup.ByARN[endpoint]; exists {
						edges = append(edges, s.createEdge(node, lambdaNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type":  "sns_subscription",
								"protocol":         protocol,
								"subscription_arn": subscriptionArn,
							}, req))
						s.logger.Debug("created SNS to Lambda edge from AWS data",
							"sns_topic", node.Properties["name"],
							"lambda_function", lambdaNode.Properties["name"],
							"protocol", protocol)
					} else {
						s.logger.Debug("Lambda function not found in lookup for SNS subscription",
							"sns_topic", node.Properties["name"],
							"function_arn", endpoint)
					}
				}

				// Handle HTTP/HTTPS subscriptions (could be API Gateway or other endpoints)
				if (protocol == "http" || protocol == "https") && endpoint != "" {
					s.logger.Debug("SNS HTTP/HTTPS subscription detected",
						"sns_topic", node.Properties["name"],
						"endpoint", endpoint,
						"protocol", protocol)
					// Note: HTTP endpoints are not currently modeled as nodes in the knowledge graph
				}

				// Handle email subscriptions
				if (protocol == "email" || protocol == "email-json") && endpoint != "" {
					s.logger.Debug("SNS email subscription detected",
						"sns_topic", node.Properties["name"],
						"email", endpoint,
						"protocol", protocol)
					// Note: Email endpoints are not currently modeled as nodes in the knowledge graph
				}
			}
		}
	}

	return edges
}

// fetchAllSNSSubscriptions fetches ALL SNS subscriptions at once and returns them grouped by topic ARN
// This is much more efficient than calling fetchSNSSubscriptions for each topic individually
func (s *AWSSource) fetchAllSNSSubscriptions(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string) (map[string][]interface{}, error) {
	// Build AWS CLI command to list ALL subscriptions (no topic filter)
	cmd := "aws sns list-subscriptions --output json"

	// Add region filter if specified
	if req.Region != "" {
		cmd = fmt.Sprintf("aws sns list-subscriptions --region %s --output json", req.Region)
	}

	s.logger.Debug("Fetching all SNS subscriptions",
		"account_id", accountID,
		"region", req.Region)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all SNS subscriptions: %w", err)
	}

	// Parse response
	var result struct {
		Subscriptions []map[string]interface{} `json:"Subscriptions"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, fmt.Errorf("failed to parse SNS subscriptions response: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid response format from cloud CLI")
	}

	// Group subscriptions by TopicArn
	subscriptionsByTopic := make(map[string][]interface{})
	for _, sub := range result.Subscriptions {
		if topicArn, ok := sub["TopicArn"].(string); ok && topicArn != "" {
			subscriptionsByTopic[topicArn] = append(subscriptionsByTopic[topicArn], sub)
		}
	}

	s.logger.Info("Successfully fetched all SNS subscriptions",
		"account_id", accountID,
		"total_subscriptions", len(result.Subscriptions),
		"topics_with_subscriptions", len(subscriptionsByTopic))

	return subscriptionsByTopic, nil
}
