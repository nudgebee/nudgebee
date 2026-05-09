package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go"
)

// Map CloudFormation stack status strings to provider status enum
func cfnStatusToNbStatus(status types.StackStatus) providers.ResourceStatus {
	s := string(status)
	switch {
	case strings.HasSuffix(s, "_COMPLETE") && !strings.HasPrefix(s, "DELETE_"):
		return providers.ResourceStatusActive
	case strings.HasSuffix(s, "_IN_PROGRESS"):
		return providers.ResourceStatusActive
	case strings.HasSuffix(s, "_FAILED"):
		return providers.ResourceStatusInactive
	case s == "DELETE_COMPLETE":
		return providers.ResourceStatusDeleted
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsCloudFormation struct {
	DefaultAwsServiceImpl
}

func (a *awsCloudFormation) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for CloudFormation yet.
	return errors.ErrUnsupported
}

func (a *awsCloudFormation) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for CloudFormation yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsCloudFormation) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsCloudFormation) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameCloudFormation)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := cloudformation.NewFromConfig(cfg)
	resources := []providers.Resource{}

	statusFilter := []types.StackStatus{
		types.StackStatusCreateInProgress,
		types.StackStatusCreateComplete,
		types.StackStatusRollbackInProgress,
		types.StackStatusRollbackFailed,
		types.StackStatusRollbackComplete,
		types.StackStatusDeleteInProgress,
		types.StackStatusDeleteFailed,
		types.StackStatusUpdateInProgress,
		types.StackStatusUpdateCompleteCleanupInProgress,
		types.StackStatusUpdateComplete,
		types.StackStatusUpdateRollbackInProgress,
		types.StackStatusUpdateRollbackFailed,
		types.StackStatusUpdateRollbackCompleteCleanupInProgress,
		types.StackStatusUpdateRollbackComplete,
		types.StackStatusReviewInProgress,
		types.StackStatusImportInProgress,
		types.StackStatusImportComplete,
		types.StackStatusImportRollbackInProgress,
		types.StackStatusImportRollbackFailed,
		types.StackStatusImportRollbackComplete,
	}

	paginator := cloudformation.NewListStacksPaginator(svc, &cloudformation.ListStacksInput{
		StackStatusFilter: statusFilter,
	})

	for paginator.HasMorePages() {
		stacksOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region")) {
				ctx.GetLogger().Warn("CloudFormation API might not be available in this region", "region", regionName, "error", apiErr.ErrorCode())
				break
			}
			ctx.GetLogger().Error("failed to list cloudformation stacks", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, summary := range stacksOutput.StackSummaries {
			descOutput, err := svc.DescribeStacks(ctx.GetContext(), &cloudformation.DescribeStacksInput{
				StackName: summary.StackId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe cloudformation stack", "error", err, "stackId", *summary.StackId, "region", regionName)
				continue
			}
			if len(descOutput.Stacks) == 0 {
				continue
			}
			stackDetails := descOutput.Stacks[0]

			tags := make(map[string][]string)
			for _, tag := range stackDetails.Tags {
				tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
			}

			resource := providers.Resource{
				Id:          aws.ToString(stackDetails.StackId),
				ServiceName: ServiceNameCloudFormation,
				Name:        aws.ToString(stackDetails.StackName),
				Status:      cfnStatusToNbStatus(stackDetails.StackStatus),
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(stackDetails),
				Arn:         aws.ToString(stackDetails.StackId),
				CreatedAt:   aws.ToTime(stackDetails.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameCloudFormation, "stack"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsCloudFormation) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameCloudFormation)
		return recommendations, err
	}

	for _, resource := range existingResources {
		if resource.Type != getAwsServiceResourceType(ServiceNameCloudFormation, "stack") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		cfg.Region = resource.Region

		if driftInfo, ok := meta["DriftInformation"].(map[string]interface{}); ok {
			if status, ok := driftInfo["StackDriftStatus"].(string); ok {
				switch types.StackDriftStatus(status) {
				case types.StackDriftStatusNotChecked:
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "aws_cfn_drift_detection_check",
						Severity:            providers.RecommendationSeverityLow,
						Data:                map[string]any{"stack_name": resource.Name, "stack_arn": resource.Arn, "reason": "Drift status has not been checked."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				case types.StackDriftStatusDrifted:
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "aws_cfn_stack_drifted",
						Severity:            providers.RecommendationSeverityMedium,
						Data:                map[string]any{"stack_name": resource.Name, "stack_arn": resource.Arn, "reason": "Stack resources have drifted."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		if enabled, ok := meta["EnableTerminationProtection"].(bool); ok && !enabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_cfn_termination_protection",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"stack_name": resource.Name, "stack_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if policyBody, ok := meta["StackPolicyBody"].(string); !ok || policyBody == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_cfn_stack_policy",
				Severity:            providers.RecommendationSeverityLow,
				Data:                map[string]any{"stack_name": resource.Name, "stack_arn": resource.Arn, "reason": "Stack does not have a stack policy."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags",
				Severity:            providers.RecommendationSeverityLow,
				Data:                map[string]any{"stack_name": resource.Name, "stack_arn": resource.Arn},
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

func (a *awsCloudFormation) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)

	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := aws.ToString(lg.LogGroupName)
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        aws.String(logGroupName),
				LogStreamNamePrefix: aws.String(resourceId),
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *awsCloudFormation) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "cloudformation",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
	// 	return providers.ServiceMapApplication{}, err
	// }

	return app, nil
}
