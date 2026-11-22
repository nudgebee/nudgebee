package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"sync"
	"time"
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

	// list-subscriptions is region-scoped and returns only the calling region's
	// subscriptions, so it runs once per region the topics live in. A region-less
	// call would silently cover only the collector's default region (#36573).
	// Topic ARNs are region-qualified, so no two regions can produce the same key.
	allSubscriptionsMap := make(map[string][]interface{})
	regions := nodeRegions(nodes)
	if len(regions) == 0 && len(nodes) > 0 {
		s.logger.Warn("no SNS topic carries a region, falling back to metadata for all topics",
			"topic_count", len(nodes))
	}
	fetched, missing := fetchSNSSubscriptionsByRegion(regions, func(region string) (map[string][]interface{}, error) {
		return s.fetchSNSSubscriptionsForRegion(reqCtx, req.CloudAccountID, region)
	}, snsRegionConcurrency, snsStepTimeLimit)
	for topicArn, subs := range fetched {
		allSubscriptionsMap[topicArn] = append(allSubscriptionsMap[topicArn], subs...)
	}
	if len(missing) > 0 {
		s.logger.Warn("SNS subscriptions missing for some regions; their topics get no subscription links in this build",
			"account_id", req.CloudAccountID,
			"regions_missing", len(missing),
			"regions_total", len(regions),
			"missing", missing)
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
		if topicSubs, exists := allSubscriptionsMap[topicArn]; exists {
			subscriptions = topicSubs
			s.logger.Debug("Using batch-fetched subscriptions for SNS topic",
				"topic_name", node.Properties["name"],
				"subscription_count", len(subscriptions))
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

const (
	// snsRegionConcurrency is how many regions' subscriptions are listed at once.
	// Each region has its own SNS rate limit, so looking regions up side by side
	// keeps one slow region from holding up the rest without adding to throttling.
	snsRegionConcurrency = 4
	// snsStepTimeLimit bounds the SNS step for a whole account: no new region is
	// started after it. A lookup already running still finishes, within the cloud
	// command timeout.
	snsStepTimeLimit = 3 * time.Minute
)

// fetchSNSSubscriptionsByRegion lists the subscriptions of every region, a few at a
// time, and merges them by topic ARN. It returns, keyed by region, why each region
// it could not list is missing: the lookup failed, or the time limit was reached
// before the region was started.
func fetchSNSSubscriptionsByRegion(regions []string, fetch func(region string) (map[string][]interface{}, error), concurrency int, timeLimit time.Duration) (map[string][]interface{}, map[string]string) {
	merged := make(map[string][]interface{})
	missing := make(map[string]string)
	if concurrency < 1 {
		concurrency = 1
	}
	deadline := time.Now().Add(timeLimit)

	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	for _, region := range regions {
		slots <- struct{}{}
		if timeLimit > 0 && time.Now().After(deadline) {
			<-slots
			mu.Lock()
			missing[region] = "not started: SNS step time limit reached"
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(region string) {
			defer wg.Done()
			defer func() { <-slots }()
			subs, err := fetch(region)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				missing[region] = err.Error()
				return
			}
			for topicArn, topicSubs := range subs {
				merged[topicArn] = append(merged[topicArn], topicSubs...)
			}
		}(region)
	}
	wg.Wait()
	return merged, missing
}

// buildSNSListSubscriptionsCommand returns the CLI command listing every SNS
// subscription in one region. --region is always present: list-subscriptions only ever
// returns the calling region's subscriptions, so a region-less call silently covers
// just the collector's AWS_DEFAULT_REGION (#36573).
func buildSNSListSubscriptionsCommand(region string) string {
	return fmt.Sprintf("aws sns list-subscriptions --region %s --output json", region)
}

// fetchSNSSubscriptionsForRegion fetches one region's SNS subscriptions in a single
// call and returns them grouped by topic ARN — far cheaper than a call per topic.
func (s *AWSSource) fetchSNSSubscriptionsForRegion(reqCtx *security.RequestContext, accountID string, region string) (map[string][]interface{}, error) {
	// Validate the region before interpolating it into the CLI command.
	if region == "" {
		return nil, fmt.Errorf("region is required to list SNS subscriptions")
	}
	if err := validateAWSRegion(region); err != nil {
		return nil, fmt.Errorf("invalid region: %w", err)
	}

	cmd := buildSNSListSubscriptionsCommand(region)

	s.logger.Debug("Fetching all SNS subscriptions",
		"account_id", accountID,
		"region", region)

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

// The inventory rows this file's resources claim: what each row's node
// type, concrete label and metadata extraction are (see kinds.go).
func init() {
	sNSRows := resourceKind{
		Service:      "AmazonSNS",
		NodeType:     core.NodeTypeTopic,
		SpecificType: "SNSTopic",
		Subtype:      "SNS",
		Extract:      (*AWSSource).extractTopicMetadata,
	}
	registerKind(sNSRows)
	registerKind(sNSRows.withTypes("topic"))
}

// The edges the account build asks this file's resources for (see dispatch.go).
func init() {
	// SNS topics publish to SQS queues, Lambda functions and other endpoints
	// via their subscriptions.
	registerEdgeStep(edgeStep{NodeType: core.NodeTypeTopic, Name: "sns",
		Build: func(s *AWSSource, reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbEdge, []*core.DbNode) {
			return s.createSNSEdges(reqCtx, nodes, lookup, req), nil
		}})
}

// Per-resource updates (see patch.go). Topics can be created from their row.
// Their subscription edges need a live SNS call and arrive with the account build.
func init() {
	registerPatchSpec(sources.PatchSpec{
		SpecificTypes: []string{"SNSTopic"},
		Creatable:     true,
	}, nil)
}
