package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type awsSqs struct {
	DefaultAwsServiceImpl
}

func (a *awsSqs) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsSqs) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsSqs) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsSqs) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSQS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := sqs.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List Queues with Pagination
	paginator := sqs.NewListQueuesPaginator(svc, &sqs.ListQueuesInput{})
	for paginator.HasMorePages() {
		queuesOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list sqs queues", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		if len(queuesOutput.QueueUrls) == 0 {
			break // No more queues or none in the region
		}

		for _, queueURL := range queuesOutput.QueueUrls {
			if queueURL == "" {
				continue
			}
			tags := make(map[string][]string)
			meta := make(map[string]any)
			createdAt := time.Now() // Default, will be updated if attribute exists

			// Get Queue Attributes (includes ARN, creation time, policy, encryption, DLQ etc.)
			// Request all attributes for comprehensive metadata
			attrsOutput, err := svc.GetQueueAttributes(context.TODO(), &sqs.GetQueueAttributesInput{
				QueueUrl:       &queueURL,
				AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll}, // Fetch all attributes
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get sqs queue attributes", "error", err, "queueUrl", queueURL, "accountNumber", account.AccountNumber, "region", regionName)
				// Continue with basic info? Or skip? Let's skip if attributes fail.
				continue
			}

			// Populate Meta map from attributes
			for k, v := range attrsOutput.Attributes {
				meta[string(k)] = v // Store the string value
			}

			// Extract specific fields for the Resource struct
			queueArn := ""
			arnAny, arnOk := meta["QueueArn"]
			if !arnOk {
				ctx.GetLogger().Warn("QueueArn attribute missing in meta", "queueUrl", queueURL, "region", regionName)
				continue // ARN is crucial
			}
			arnStr, arnTypeOk := arnAny.(string)
			if !arnTypeOk {
				ctx.GetLogger().Warn("QueueArn attribute is not a string", "queueUrl", queueURL, "region", regionName, "actualType", fmt.Sprintf("%T", arnAny))
				continue // ARN is crucial
			}
			queueArn = arnStr

			// Parse CreatedTimestamp
			timestampAny, tsOk := meta["CreatedTimestamp"]
			if !tsOk {
				ctx.GetLogger().Warn("CreatedTimestamp attribute missing in meta", "queueUrl", queueURL, "queueArn", queueArn, "region", regionName)
				// createdAt remains zero time
			} else {
				createdTimestampStr, tsTypeOk := timestampAny.(string)
				if !tsTypeOk {
					ctx.GetLogger().Warn("CreatedTimestamp attribute is not a string", "queueUrl", queueURL, "queueArn", queueArn, "region", regionName, "actualType", fmt.Sprintf("%T", timestampAny))
					// createdAt remains zero time
				} else {
					if createdTimestampUnix, err := strconv.ParseInt(createdTimestampStr, 10, 64); err == nil {
						createdAt = time.Unix(createdTimestampUnix, 0)
					} else {
						ctx.GetLogger().Warn("failed to parse CreatedTimestamp for queue", "error", err, "queueArn", queueArn, "timestampStr", createdTimestampStr, "region", regionName)
						// createdAt remains zero time
					}
				}
			}

			// Extract queue name from URL
			queueName := queueURL
			if lastSlash := strings.LastIndex(queueURL, "/"); lastSlash != -1 {
				queueName = (queueURL)[lastSlash+1:]
			}

			// Get Tags for the queue
			tagsOutput, err := svc.ListQueueTags(context.TODO(), &sqs.ListQueueTagsInput{
				QueueUrl: &queueURL,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for sqs queue", "error", err, "queueUrl", queueURL, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				if len(tagsOutput.Tags) > 0 { // Check if the Tags map itself is nil
					for k, v := range tagsOutput.Tags {
						tags[k] = append(tags[k], v)
					}
				} else {
					ctx.GetLogger().Info("Tags map is nil for queue", "queueUrl", queueURL, "queueArn", queueArn, "region", regionName)
				}
			}

			resource := providers.Resource{
				Id:          queueName, // Use queue name as ID
				ServiceName: ServiceNameSQS,
				Name:        queueName,
				Status:      providers.ResourceStatusActive, // Assume active if listed and attributes fetched
				Region:      regionName,
				Tags:        tags,
				Meta:        meta, // Contains all attributes like RedrivePolicy, KmsMasterKeyId etc.
				Arn:         queueArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameSQS, "queue"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *awsSqs) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameSQS)
		return recommendations, err
	}

	for _, resource := range existingResources {
		// Ensure we are looking at SQS Queues
		// Use the constant ServiceNameSQS defined elsewhere
		if resource.Type != getAwsServiceResourceType(ServiceNameSQS, "queue") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 { // Added nil check for meta
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for SQS queue", "queueArn", resource.Arn, "region", resource.Region)
			continue
		}

		// Check 1: Dead-Letter Queue (DLQ) Configuration
		hasDLQConfigured := false
		redrivePolicyAny, rpOk := meta["RedrivePolicy"]
		if rpOk {
			policyStr, typeOk := redrivePolicyAny.(string)
			if typeOk {
				if policyStr != "" {
					// Optional Enhancement: Basic JSON unmarshal to check for deadLetterTargetArn
					// For now, any non-empty string implies some configuration.
					// A more robust check can be added later if needed:
					// var dlqPolicy struct { DeadLetterTargetArn string `json:"deadLetterTargetArn"` }
					// if err := json.Unmarshal([]byte(policyStr), &dlqPolicy); err == nil && dlqPolicy.DeadLetterTargetArn != "" {
					// 	hasDLQConfigured = true
					// } else if err != nil {
					//   ctx.GetLogger().Warn("Failed to unmarshal RedrivePolicy JSON for SQS queue", "queueArn", resource.Arn, "policyStr", policyStr, "error", err)
					// }
					hasDLQConfigured = true // Simplified: non-empty policy string means DLQ is configured.
				}
			} else {
				ctx.GetLogger().Warn("RedrivePolicy attribute is not a string for SQS queue", "queueArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", redrivePolicyAny))
			}
		} // If rpOk is false, RedrivePolicy key doesn't exist, so hasDLQConfigured remains false.

		if !hasDLQConfigured {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_sqs_dlq_configured",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"queue_name": resource.Name, "queue_arn": resource.Arn, "reason": "Dead-letter queue (DLQ) is not configured or RedrivePolicy is empty/invalid."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Server-Side Encryption (SSE) Enabled
		// SSE is enabled if KmsMasterKeyId is present and non-empty, or if SqsManagedSseEnabled is "true".
		// If KmsMasterKeyId is missing, and SqsManagedSseEnabled is missing or "false", then SSE is not effectively enabled with a specific key.

		sseEnabled := false
		kmsMasterKeyIdAny, kmsKeyOk := meta["KmsMasterKeyId"]
		if kmsKeyOk {
			kmsKeyIdStr, typeOk := kmsMasterKeyIdAny.(string)
			if typeOk {
				if kmsKeyIdStr != "" { // Non-empty KmsMasterKeyId means CMK-based SSE
					sseEnabled = true
				}
			} else {
				ctx.GetLogger().Warn("KmsMasterKeyId attribute is not a string for SQS queue", "queueArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", kmsMasterKeyIdAny))
			}
		}

		// Check SqsManagedSseEnabled if no CMK is found yet
		if !sseEnabled {
			sqsManagedSseEnabledAny, sqsSseOk := meta["SqsManagedSseEnabled"]
			if sqsSseOk {
				sqsSseEnabledStr, typeOk := sqsManagedSseEnabledAny.(string)
				if typeOk {
					if strings.ToLower(sqsSseEnabledStr) == "true" {
						sseEnabled = true // SQS-managed SSE
					}
				} else {
					ctx.GetLogger().Warn("SqsManagedSseEnabled attribute is not a string for SQS queue", "queueArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", sqsManagedSseEnabledAny))
				}
			}
		}

		if !sseEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_sqs_sse_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"queue_name": resource.Name, "queue_arn": resource.Arn, "reason": "Server-side encryption (SSE) with either a CMK (KmsMasterKeyId) or SQS-managed SSE (SqsManagedSseEnabled) is not confirmed."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
		// Note: Could add a stricter check to recommend using CMK if only SQS-managed SSE is found.

		// Check 3: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"queue_name": resource.Name, "queue_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Idle Queue Check (based on ApproximateNumberOfMessagesVisible, NumberOfMessagesReceived/Sent metrics over time)
		// - FIFO Queue Usage (recommend FIFO if exactly-once processing or ordering is needed)
		// - Message Retention Period (check if MessageRetentionPeriod is too short/long)
		// - Visibility Timeout (check if VisibilityTimeout is appropriate for processing time)
	}

	return recommendations, nil
}

func (a *awsSqs) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsSqs) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "sqs",
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
