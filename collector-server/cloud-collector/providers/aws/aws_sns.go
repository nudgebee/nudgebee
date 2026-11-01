package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/samber/lo"
)

type amazonSns struct {
	DefaultAwsServiceImpl
}

func (a *amazonSns) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonSns) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonSns) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonSns) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSNS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := sns.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List Topics with Pagination
	paginator := sns.NewListTopicsPaginator(svc, &sns.ListTopicsInput{})
	for paginator.HasMorePages() {
		topicsOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list sns topics", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Return partial results collected so far if pagination fails mid-way
			return resources, err
		}

		if len(topicsOutput.Topics) == 0 {
			break // No more topics in this page or region
		}

		for _, topic := range topicsOutput.Topics {
			if topic.TopicArn == nil {
				continue // Skip if ARN is missing
			}
			topicArn := *topic.TopicArn

			// Get Topic Attributes for detailed metadata
			attrsOutput, attrsErr := svc.GetTopicAttributes(ctx.GetContext(), &sns.GetTopicAttributesInput{
				TopicArn: topic.TopicArn,
			})
			resources = append(resources, buildSnsTopicResource(ctx, svc, topicArn, attrsOutput, attrsErr, account, regionName))
		}
	}
	return resources, nil
}

// buildSnsTopicResource enriches one topic ARN into a providers.Resource.
// Shared by GetResources and GetResourcesByIds so the full-scan and targeted paths
// produce identical rows. The caller fetches the topic attributes: the targeted
// path uses that call to tell a deleted topic from a failed lookup, while the full
// scan keeps the row with basic info when it fails.
func buildSnsTopicResource(
	ctx providers.CloudProviderContext,
	svc *sns.Client,
	topicArn string,
	attrsOutput *sns.GetTopicAttributesOutput,
	attrsErr error,
	account providers.Account,
	regionName string,
) providers.Resource {
	tags := make(map[string][]string)
	meta := make(map[string]any)

	// Get Tags for the topic
	tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &sns.ListTagsForResourceInput{
		ResourceArn: &topicArn,
	})
	if err != nil {
		// Log warning but continue, tags are not critical for resource existence
		ctx.GetLogger().Warn("failed to fetch tags for sns topic", "error", err, "topicArn", topicArn, "accountNumber", account.AccountNumber, "region", regionName)
	} else {
		if len(tagsOutput.Tags) > 0 { // Check if Tags slice is nil
			for _, tag := range tagsOutput.Tags {
				if tag.Key != nil && tag.Value != nil { // Check individual tag and its fields
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
		} else {
			ctx.GetLogger().Info("tagsOutput.Tags is nil for topic", "topicArn", topicArn, "region", regionName)
		}
	}

	if attrsErr != nil {
		ctx.GetLogger().Warn("failed to get sns topic attributes", "error", attrsErr, "topicArn", topicArn, "accountNumber", account.AccountNumber, "region", regionName)
		// Store basic info even if attributes fail, ARN is already known
		meta["TopicArn"] = topicArn
	} else {
		// Populate Meta map from attributes
		if len(attrsOutput.Attributes) > 0 { // Check if Attributes map is nil
			for k, v := range attrsOutput.Attributes {
				meta[k] = v // Store the string value
			}
		} else {
			ctx.GetLogger().Info("attrsOutput.Attributes is nil for topic", "topicArn", topicArn, "region", regionName)
		}
		// Ensure TopicArn is in meta even if GetTopicAttributes succeeds
		if _, exists := meta["TopicArn"]; !exists {
			meta["TopicArn"] = topicArn
		}
	}

	// Get Subscriptions for this topic
	subscriptionsPaginator := sns.NewListSubscriptionsByTopicPaginator(svc, &sns.ListSubscriptionsByTopicInput{
		TopicArn: &topicArn,
	})
	subscriptionsList := []map[string]any{}
	for subscriptionsPaginator.HasMorePages() {
		subsOutput, subsErr := subscriptionsPaginator.NextPage(ctx.GetContext())
		if subsErr != nil {
			ctx.GetLogger().Warn("failed to fetch subscriptions for sns topic", "error", subsErr, "topicArn", topicArn, "accountNumber", account.AccountNumber, "region", regionName)
			break
		}
		for _, sub := range subsOutput.Subscriptions {
			subscriptionsList = append(subscriptionsList, structToMap(sub))
		}
	}
	if len(subscriptionsList) > 0 {
		meta["Subscriptions"] = subscriptionsList
	} else {
		meta["Subscriptions"] = []any{}
	}

	// Extract topic name from ARN
	topicName := topicArn
	if lastColon := strings.LastIndex(topicArn, ":"); lastColon != -1 {
		topicName = topicArn[lastColon+1:]
	}

	return providers.Resource{
		Id:          topicName, // Use topic name as ID
		ServiceName: ServiceNameSNS,
		Name:        topicName,
		Status:      providers.ResourceStatusActive, // Assume active if listed
		Region:      regionName,
		Tags:        tags,
		Meta:        meta, // Contains attributes like Policy, KmsMasterKeyId etc.
		Arn:         topicArn,
		CreatedAt:   time.Now(), // Actual creation time not available via these APIs
		Type:        getAwsServiceResourceType(ServiceNameSNS, "topic"),
	}
}

// snsTopicNamePattern is the SNS topic-name alphabet; a FIFO topic adds ".fifo".
var snsTopicNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// snsTopicArnsForLookupKeys resolves ListResources lookup keys to the topic ARNs
// to fetch in region. A key is the topic name (the stored Id) or the topic ARN;
// an ARN for another region has nothing to fetch here. Any other shape — a
// subscription ARN included — reports ErrUnsupported, which sends ListResources
// back to the region scan and its own filter, exactly as before this service had
// a targeted fetch.
func snsTopicArnsForLookupKeys(keys []string, region, accountNumber string) ([]string, error) {
	topicArns := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(key, "arn:") {
			parts := strings.Split(key, ":")
			if len(parts) != 6 || parts[2] != "sns" {
				return nil, errors.ErrUnsupported
			}
			if parts[3] == region {
				topicArns = append(topicArns, key)
			}
			continue
		}
		if len(key) > 256 || !snsTopicNamePattern.MatchString(strings.TrimSuffix(key, ".fifo")) {
			return nil, errors.ErrUnsupported
		}
		topicArns = append(topicArns, fmt.Sprintf("arn:aws:sns:%s:%s:%s", region, accountNumber, key))
	}
	return lo.Uniq(topicArns), nil
}

// GetResourcesByIds fetches specific topics by name or ARN without listing the region.
//
// Without this, ListResources falls back to a full ListTopics sweep plus three API
// calls per topic. On the EventBridge path that is one whole-region inventory scan
// per delivered CreateTopic/DeleteTopic event — see the Lambda implementation for
// what that did to the consumer during a bulk delete.
func (a *amazonSns) GetResourcesByIds(ctx providers.CloudProviderContext, account providers.Account, region string, resourceIds []string) ([]providers.Resource, error) {
	resourceIds = lo.Uniq(resourceIds)
	if len(resourceIds) == 0 {
		// Nothing to look up. Deliberately not ErrUnsupported: that word means
		// "this service has no targeted fetch" and sends ListResources back to a
		// full region scan.
		return []providers.Resource{}, nil
	}
	topicArns, err := snsTopicArnsForLookupKeys(resourceIds, region, account.AccountNumber)
	if err != nil {
		return nil, err
	}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return nil, err
	}
	cfg.Region = region
	svc := sns.NewFromConfig(cfg)

	resources := []providers.Resource{}
	for _, topicArn := range topicArns {
		// A topic deleted before this lookup is the normal case on DeleteTopic and
		// is genuinely gone: skip it. Any other failure is returned so ListResources
		// falls back to the full scan — callers read an empty, no-error result as
		// "gone", and a denied or throttled call must not look like that.
		attrsOutput, err := svc.GetTopicAttributes(ctx.GetContext(), &sns.GetTopicAttributesInput{TopicArn: aws.String(topicArn)})
		if err != nil {
			var notFound *snstypes.NotFoundException
			if errors.As(err, &notFound) {
				ctx.GetLogger().Info("GetResourcesByIds: sns topic not found, skipping", "topicArn", topicArn, "accountNumber", account.AccountNumber, "region", region)
				continue
			}
			ctx.GetLogger().Error("GetResourcesByIds: failed to get sns topic, falling back to full scan", "error", err, "topicArn", topicArn, "accountNumber", account.AccountNumber, "region", region)
			return nil, fmt.Errorf("failed to get sns topic %s: %w", topicArn, err)
		}
		resources = append(resources, buildSnsTopicResource(ctx, svc, topicArn, attrsOutput, nil, account, region))
	}
	return resources, nil
}

func (a *amazonSns) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}
	for _, resource := range existingResources {
		// Ensure we are looking at SNS Topics
		if resource.Type != getAwsServiceResourceType(ServiceNameSNS, "topic") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 { // Check if meta is nil or empty
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for this SNS topic", "topicArn", resource.Arn, "region", resource.Region)
			continue
		}

		// Check 1: Server-Side Encryption (SSE) Enabled
		kmsKeyId := ""
		kmsMasterKeyIdAny, kmsOk := meta["KmsMasterKeyId"]
		if kmsOk {
			keyIdStr, typeOk := kmsMasterKeyIdAny.(string)
			if typeOk {
				kmsKeyId = keyIdStr
			} else {
				ctx.GetLogger().Warn("KmsMasterKeyId attribute is not a string for SNS topic", "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", kmsMasterKeyIdAny))
			}
		} // If !kmsOk, kmsKeyId remains "", indicating SSE not configured with CMK.

		if kmsKeyId == "" { // This means either key was missing, or it was present but an empty string (which implies default AWS key)
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_sns_sse_enabled_cmk", // More specific: recommend CMK
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"topic_name": resource.Name, "topic_arn": resource.Arn, "reason": "Server-side encryption (SSE) with a Customer Managed Key (CMK) is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Public Access via Policy
		isPublic := false
		policyAny, policyOk := meta["Policy"]
		if !policyOk {
			ctx.GetLogger().Warn("Policy attribute missing in meta for SNS topic", "topicArn", resource.Arn, "region", resource.Region)
		} else {
			policyStr, policyTypeOk := policyAny.(string)
			if !policyTypeOk {
				ctx.GetLogger().Warn("Policy attribute is not a string for SNS topic", "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", policyAny))
			} else if policyStr != "" {
				var policyDoc map[string]interface{}
				if err := common.UnmarshalJson([]byte(policyStr), &policyDoc); err == nil {
					statementsAny, stmtKeyOk := policyDoc["Statement"]
					if !stmtKeyOk {
						ctx.GetLogger().Warn("Policy document Statement key missing for SNS topic", "topicArn", resource.Arn, "region", resource.Region)
					} else {
						statements, stmtSliceOk := statementsAny.([]interface{})
						if !stmtSliceOk {
							ctx.GetLogger().Warn("Policy document Statement is not a slice for SNS topic", "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", statementsAny))
						} else {
							for _, stmtAny := range statements {
								stmt, stmtMapOk := stmtAny.(map[string]interface{})
								if !stmtMapOk {
									ctx.GetLogger().Warn("Policy statement is not a map for SNS topic", "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", stmtAny))
									continue
								}

								effectAny, effectOk := stmt["Effect"]
								if !effectOk {
									ctx.GetLogger().Warn("Policy statement Effect missing for SNS topic", "topicArn", resource.Arn, "region", resource.Region)
									continue
								}
								effectStr, effectTypeOk := effectAny.(string)
								if !effectTypeOk {
									ctx.GetLogger().Warn("Policy statement Effect is not a string for SNS topic", "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", effectAny))
									continue
								}

								if strings.ToLower(effectStr) == "allow" {
									principalAny, principalOk := stmt["Principal"]
									if !principalOk {
										ctx.GetLogger().Warn("Policy statement Principal missing for SNS topic", "topicArn", resource.Arn, "region", resource.Region)
										continue
									}

									if pStr, pStrOk := principalAny.(string); pStrOk && pStr == "*" {
										isPublic = true
										break
									}
									if pMap, pMapOk := principalAny.(map[string]interface{}); pMapOk {
										awsPPrincipalAny, awsPOk := pMap["AWS"]
										if awsPOk {
											if awsPStr, awsPTypeOk := awsPPrincipalAny.(string); awsPTypeOk && awsPStr == "*" {
												isPublic = true
												break
											}
										}
									}
								}
							}
						}
					}
				} else {
					ctx.GetLogger().Warn("failed to parse SNS topic policy JSON", "topicArn", resource.Arn, "region", resource.Region, "error", err)
				}
			}
		}

		if isPublic {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_sns_topic_no_public_access",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{"topic_name": resource.Name, "topic_arn": resource.Arn, "reason": "Topic policy potentially allows public access (Principal: '*')."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Delivery Status Logging Enabled
		checkFeedbackArn := func(key string) bool {
			valAny, ok := meta[key]
			if !ok {
				return false // Key doesn't exist, so not configured
			}
			arnStr, typeOk := valAny.(string)
			if !typeOk {
				ctx.GetLogger().Warn("FeedbackRoleArn attribute is not a string", "key", key, "topicArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", valAny))
				return false // Wrong type
			}
			return arnStr != "" // Configured if non-empty string
		}

		httpSuccessFeedback := checkFeedbackArn("HTTPSuccessFeedbackRoleArn")
		httpFailureFeedback := checkFeedbackArn("HTTPFailureFeedbackRoleArn")
		sqsSuccessFeedback := checkFeedbackArn("SQSSuccessFeedbackRoleArn")
		sqsFailureFeedback := checkFeedbackArn("SQSFailureFeedbackRoleArn")
		lambdaSuccessFeedback := checkFeedbackArn("LambdaSuccessFeedbackRoleArn")
		lambdaFailureFeedback := checkFeedbackArn("LambdaFailureFeedbackRoleArn")
		appSuccessFeedback := checkFeedbackArn("ApplicationSuccessFeedbackRoleArn")
		appFailureFeedback := checkFeedbackArn("ApplicationFailureFeedbackRoleArn")
		firehoseSuccessFeedback := checkFeedbackArn("FirehoseSuccessFeedbackRoleArn")
		firehoseFailureFeedback := checkFeedbackArn("FirehoseFailureFeedbackRoleArn")

		if !httpSuccessFeedback && !httpFailureFeedback && !sqsSuccessFeedback && !sqsFailureFeedback && !lambdaSuccessFeedback && !lambdaFailureFeedback && !appSuccessFeedback && !appFailureFeedback && !firehoseSuccessFeedback && !firehoseFailureFeedback {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_sns_delivery_status_logging",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"topic_name": resource.Name, "topic_arn": resource.Arn, "reason": "Delivery status logging is not configured for any endpoint type."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"topic_name": resource.Name, "topic_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (a *amazonSns) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)

	var foundLogGroup string
	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, nil
}

func (a *amazonSns) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "sns",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}

	return app, nil
}
