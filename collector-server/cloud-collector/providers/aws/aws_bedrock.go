package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go"
)

type amazonBedrock struct {
	DefaultAwsServiceImpl
}

func (a *amazonBedrock) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.New("unsupported")
}

func (a *amazonBedrock) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.New("unsupported")
}

func (a *amazonBedrock) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonBedrock) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameBedrock)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := bedrock.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Provisioned Model Throughputs ---
	provisionedPaginator := bedrock.NewListProvisionedModelThroughputsPaginator(svc, &bedrock.ListProvisionedModelThroughputsInput{})
	for provisionedPaginator.HasMorePages() {
		provisionedOutput, err := provisionedPaginator.NextPage(ctx.GetContext())
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region")) {
				ctx.GetLogger().Warn("Bedrock ListProvisionedModelThroughputs API not available/supported in region", "region", regionName, "error", apiErr.ErrorCode())
				break
			}
			ctx.GetLogger().Error("failed to fetch provisioned model throughputs", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			break
		}

		for _, summary := range provisionedOutput.ProvisionedModelSummaries {
			if summary.ProvisionedModelArn == nil {
				ctx.GetLogger().Warn("ProvisionedModelArn is nil, skipping summary", "summary", summary, "region", regionName)
				continue
			}
			if summary.ProvisionedModelName == nil || summary.Status == "" || summary.CreationTime == nil {
				ctx.GetLogger().Warn("Provisioned model summary missing essential fields", "arn", *summary.ProvisionedModelArn, "region", regionName)
			}

			tags := make(map[string][]string)
			details, errDetails := svc.GetProvisionedModelThroughput(ctx.GetContext(), &bedrock.GetProvisionedModelThroughputInput{
				ProvisionedModelId: summary.ProvisionedModelArn,
			})
			if errDetails != nil {
				ctx.GetLogger().Warn("failed to get provisioned model throughput details", "error", errDetails, "arn", *summary.ProvisionedModelArn, "region", regionName)
			}

			tagsOutput, errTags := svc.ListTagsForResource(ctx.GetContext(), &bedrock.ListTagsForResourceInput{
				ResourceARN: summary.ProvisionedModelArn,
			})
			if errTags != nil {
				ctx.GetLogger().Warn("failed to fetch tags for provisioned model throughput", "error", errTags, "arn", *summary.ProvisionedModelArn, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
				}
			}

			meta := structToMap(summary)
			if details != nil {
				meta["Details"] = structToMap(details)
			}

			var resourceStatus providers.ResourceStatus
			switch summary.Status {
			case types.ProvisionedModelStatusFailed:
				resourceStatus = providers.ResourceStatusInactive
			case types.ProvisionedModelStatusCreating, types.ProvisionedModelStatusInService:
				resourceStatus = providers.ResourceStatusActive
			default:
				resourceStatus = providers.ResourceStatusUnknown
			}

			resource := providers.Resource{
				Id:          aws.ToString(summary.ProvisionedModelArn),
				ServiceName: ServiceNameBedrock,
				Name:        aws.ToString(summary.ProvisionedModelName),
				Status:      resourceStatus,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(summary.ProvisionedModelArn),
				CreatedAt:   aws.ToTime(summary.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameBedrock, "provisioned-throughput"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Custom Models ---
	customPaginator := bedrock.NewListCustomModelsPaginator(svc, &bedrock.ListCustomModelsInput{})
	for customPaginator.HasMorePages() {
		customModelsOutput, err := customPaginator.NextPage(ctx.GetContext())
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region")) {
				ctx.GetLogger().Warn("Bedrock ListCustomModels API not available/supported in region", "region", regionName, "error", apiErr.ErrorCode())
				break
			}
			ctx.GetLogger().Error("failed to fetch custom models", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, nil
		}

		for _, summary := range customModelsOutput.ModelSummaries {
			if summary.ModelArn == nil {
				ctx.GetLogger().Warn("Custom ModelArn is nil, skipping summary", "summary", summary, "region", regionName)
				continue
			}
			if summary.ModelName == nil || summary.CreationTime == nil {
				ctx.GetLogger().Warn("Custom model summary missing essential fields", "arn", *summary.ModelArn, "region", regionName)
			}

			tags := make(map[string][]string)
			details, errDetails := svc.GetCustomModel(ctx.GetContext(), &bedrock.GetCustomModelInput{
				ModelIdentifier: summary.ModelArn,
			})
			if errDetails != nil {
				ctx.GetLogger().Warn("failed to get custom model details", "error", errDetails, "arn", *summary.ModelArn, "region", regionName)
			}

			tagsOutput, errTags := svc.ListTagsForResource(ctx.GetContext(), &bedrock.ListTagsForResourceInput{
				ResourceARN: summary.ModelArn,
			})
			if errTags != nil {
				ctx.GetLogger().Warn("failed to fetch tags for custom model", "error", errTags, "arn", *summary.ModelArn, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
				}
			}

			meta := structToMap(summary)
			if details != nil {
				meta["Details"] = structToMap(details)
			}

			resource := providers.Resource{
				Id:          aws.ToString(summary.ModelArn),
				ServiceName: ServiceNameBedrock,
				Name:        aws.ToString(summary.ModelName),
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(summary.ModelArn),
				CreatedAt:   aws.ToTime(summary.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameBedrock, "custom-model"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonBedrock) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameBedrock)
		return recommendations, err
	}

	checkedRegionsForLogging := make(map[string]bool)

	for _, resource := range existingResources {
		if resource.Region != "" && !checkedRegionsForLogging[resource.Region] {
			cfg.Region = resource.Region
			svc := bedrock.NewFromConfig(cfg)
			loggingConfig, loggingErr := svc.GetModelInvocationLoggingConfiguration(ctx.GetContext(), &bedrock.GetModelInvocationLoggingConfigurationInput{})
			checkedRegionsForLogging[resource.Region] = true

			if loggingErr != nil {
				var apiErr smithy.APIError
				if errors.As(loggingErr, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region") || apiErr.ErrorCode() == "AccessDeniedException") {
					ctx.GetLogger().Warn("Bedrock GetModelInvocationLoggingConfiguration API not available/supported or access denied in region", "region", resource.Region, "error", apiErr.ErrorCode())
				} else {
					ctx.GetLogger().Error("failed to get model invocation logging configuration", "error", loggingErr, "accountNumber", account.AccountNumber, "region", resource.Region)
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "aws_bedrock_invocation_logging_check_failed",
						Severity:            providers.RecommendationSeverityLow,
						Data:                map[string]any{"region": resource.Region, "reason": "Failed to retrieve invocation logging status. Manual verification required.", "error": loggingErr.Error()},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: ServiceNameBedrock,
						ResourceId:          account.AccountNumber,
						ResourceType:        getAwsServiceResourceType(ServiceNameBedrock, "logging-configuration"),
						ResourceRegion:      resource.Region,
					})
				}
			} else if loggingConfig == nil || loggingConfig.LoggingConfig == nil || (loggingConfig.LoggingConfig.CloudWatchConfig == nil && loggingConfig.LoggingConfig.S3Config == nil) {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_bedrock_invocation_logging_enabled",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"region": resource.Region, "reason": "Model invocation logging is not configured for CloudWatch or S3."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: ServiceNameBedrock,
					ResourceId:          account.AccountNumber,
					ResourceType:        getAwsServiceResourceType(ServiceNameBedrock, "logging-configuration"),
					ResourceRegion:      resource.Region,
				})
			}
		}

		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations", "resourceArn", resource.Arn, "region", resource.Region)
			continue
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameBedrock, "provisioned-throughput") {
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
					Severity:            providers.RecommendationSeverityLow,
					Data:                map[string]any{"provisioned_model_name": resource.Name, "provisioned_model_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameBedrock, "custom-model") {
			recommendEncryption := true
			if detailsAny, detailsOk := meta["Details"]; detailsOk {
				if detailsMap, detailsMapOk := detailsAny.(map[string]any); detailsMapOk {
					if outputDataConfigAny, outputOk := detailsMap["OutputDataConfig"]; outputOk {
						if outputDataConfigMap, outputMapOk := outputDataConfigAny.(map[string]any); outputMapOk {
							if kmsKeyIdAny, kmsOk := outputDataConfigMap["KmsKeyId"]; kmsOk {
								if kmsKeyIdStr, kmsStrOk := kmsKeyIdAny.(string); kmsStrOk && kmsKeyIdStr != "" {
									recommendEncryption = false
								}
							}
						}
					} else {
						recommendEncryption = false
					}
				} else {
					recommendEncryption = false
				}
			} else {
				recommendEncryption = false
			}

			if recommendEncryption {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_bedrock_custom_model_output_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"custom_model_name": resource.Name, "custom_model_arn": resource.Arn, "reason": "Output data is not encrypted with a Customer Managed Key (CMK)."},
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
					Data:                map[string]any{"custom_model_name": resource.Name, "custom_model_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	return recommendations, nil
}

func (a *amazonBedrock) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonBedrock) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "bedrock",
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
