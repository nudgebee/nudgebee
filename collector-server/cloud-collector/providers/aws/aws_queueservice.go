package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type awsQueueService struct {
	DefaultAwsServiceImpl
}

func (a *awsQueueService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsQueueService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsQueueService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsQueueService) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSQS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := sqs.NewFromConfig(cfg)
	resources := []providers.Resource{}

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
				AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get sqs queue attributes", "error", err, "queueUrl", queueURL, "accountNumber", account.AccountNumber, "region", regionName)
				// Continue with basic info? Or skip? Let's skip if attributes fail.
				continue
			}

			// Populate Meta map from attributes
			for k, v := range attrsOutput.Attributes {
				meta[k] = v // Store the string value
			}

			// Extract specific fields for the Resource struct
			queueArn := ""
			if arn, ok := meta["QueueArn"].(string); ok {
				queueArn = arn
			} else {
				ctx.GetLogger().Warn("QueueArn attribute missing or not string", "queueUrl", queueURL)
				continue // ARN is crucial, skip if missing
			}

			if createdTimestampStr, ok := meta["CreatedTimestamp"].(string); ok {
				if createdTimestampUnix, err := strconv.ParseInt(createdTimestampStr, 10, 64); err == nil {
					createdAt = time.Unix(createdTimestampUnix, 0)
				} else {
					ctx.GetLogger().Warn("failed to parse CreatedTimestamp for queue", "error", err, "queueArn", queueArn)
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
				for k, v := range tagsOutput.Tags {
					tags[k] = append(tags[k], v)
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

func (a *awsQueueService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameSQS)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		// Ensure we are looking at SQS Queues
		if resource.Type != getAwsServiceResourceType(ServiceNameSQS, "queue") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Dead-Letter Queue (DLQ) Configuration
		// The RedrivePolicy attribute contains DLQ settings as a JSON string.
		hasDLQ := false
		if policyStr, ok := meta["RedrivePolicy"].(string); ok && policyStr != "" {
			// Basic check: if the policy string is not empty, assume DLQ is configured.
			// A more robust check would parse the JSON and verify 'deadLetterTargetArn' and 'maxReceiveCount'.
			hasDLQ = true
		}
		if !hasDLQ {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Reliability
				RuleName:            "aws_sqs_dlq_configured",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"queue_name": resource.Name, "queue_arn": resource.Arn, "reason": "Dead-letter queue (DLQ) is not configured."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Server-Side Encryption (SSE) Enabled
		// Check KmsMasterKeyId attribute. If it exists and is non-empty, SSE is enabled.
		// Recommend enabling if it's missing or empty.
		kmsKeyId := ""
		if keyId, ok := meta["KmsMasterKeyId"].(string); ok {
			kmsKeyId = keyId
		}
		if kmsKeyId == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_sqs_sse_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"queue_name": resource.Name, "queue_arn": resource.Arn, "reason": "Server-side encryption (SSE) is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
		// Note: Could add a stricter check to recommend using CMK instead of default AWS managed key (`alias/aws/sqs`).

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

func (a *awsQueueService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsQueueService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "queueservice",
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
