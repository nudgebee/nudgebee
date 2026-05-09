package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	"github.com/aws/aws-sdk-go-v2/service/xray/types"
)

type amazonXray struct {
	DefaultAwsServiceImpl
}

func (a *amazonXray) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonXray) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonXray) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonXray) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameXray)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := xray.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Groups ---
	groupsPaginator := xray.NewGetGroupsPaginator(svc, &xray.GetGroupsInput{})
	for groupsPaginator.HasMorePages() {
		groupsOutput, err := groupsPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch xray groups", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, group := range groupsOutput.Groups {
			if group.GroupName == nil || group.GroupARN == nil {
				ctx.GetLogger().Warn("Skipping X-Ray group due to missing Name or ARN", "group", group)
				continue
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(context.TODO(), &xray.ListTagsForResourceInput{
				ResourceARN: group.GroupARN,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for xray group", "error", err, "groupArn", *group.GroupARN, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			meta := structToMap(group)
			resource := providers.Resource{
				Id:          *group.GroupName,
				ServiceName: ServiceNameXray,
				Name:        *group.GroupName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *group.GroupARN,
				CreatedAt:   time.Now(), // Not available from GetGroups API
				Type:        getAwsServiceResourceType(ServiceNameXray, "group"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Sampling Rules ---
	samplingPaginator := xray.NewGetSamplingRulesPaginator(svc, &xray.GetSamplingRulesInput{})
	for samplingPaginator.HasMorePages() {
		samplingOutput, err := samplingPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch xray sampling rules", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, ruleRecord := range samplingOutput.SamplingRuleRecords {
			if ruleRecord.SamplingRule == nil || ruleRecord.SamplingRule.RuleName == nil || ruleRecord.SamplingRule.RuleARN == nil {
				ctx.GetLogger().Warn("Skipping X-Ray sampling rule due to missing essential fields", "ruleRecord", ruleRecord)
				continue
			}
			rule := ruleRecord.SamplingRule

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(context.TODO(), &xray.ListTagsForResourceInput{
				ResourceARN: rule.RuleARN,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for xray sampling rule", "error", err, "ruleArn", *rule.RuleARN, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			meta := structToMap(rule)
			createdAt := time.Now()
			if ruleRecord.CreatedAt != nil {
				createdAt = *ruleRecord.CreatedAt
			} else if ruleRecord.ModifiedAt != nil {
				createdAt = *ruleRecord.ModifiedAt
			}

			resource := providers.Resource{
				Id:          *rule.RuleName,
				ServiceName: ServiceNameXray,
				Name:        *rule.RuleName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *rule.RuleARN,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameXray, "sampling-rule"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonXray) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameXray)
		return recommendations, err
	}

	encryptionChecked := false
	var encryptionConfig *xray.GetEncryptionConfigOutput
	var encryptionErr error
	samplingRulesExist := false

	for _, resource := range existingResources {
		if !encryptionChecked && resource.Region != "" {
			cfg.Region = resource.Region
			svc := xray.NewFromConfig(cfg)
			encryptionConfig, encryptionErr = svc.GetEncryptionConfig(context.TODO(), &xray.GetEncryptionConfigInput{})
			encryptionChecked = true

			if encryptionErr != nil {
				ctx.GetLogger().Error("failed to get xray encryption configuration", "error", encryptionErr, "accountNumber", account.AccountNumber, "region", resource.Region)
			} else if encryptionConfig.EncryptionConfig == nil || encryptionConfig.EncryptionConfig.KeyId == nil || *encryptionConfig.EncryptionConfig.KeyId == "" {
				reason := "Encryption is not enabled (Type: NONE)."
				if encryptionConfig.EncryptionConfig != nil && encryptionConfig.EncryptionConfig.Type == types.EncryptionTypeKms {
					reason = "Encryption is enabled but not using a Customer Managed Key (CMK)."
				}
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_xray_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"region": resource.Region, "reason": reason},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: ServiceNameXray,
					ResourceId:          account.AccountNumber,
					ResourceType:        getAwsServiceResourceType(ServiceNameXray, "encryption-configuration"),
					ResourceRegion:      resource.Region,
				})
			}
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameXray, "sampling-rule") {
			samplingRulesExist = true
		}

		if len(resource.Meta) == 0 {
			continue
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameXray, "group") {
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"group_name": resource.Name, "group_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameXray, "sampling-rule") {
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"rule_name": resource.Name, "rule_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	if encryptionChecked && !samplingRulesExist {
		if len(existingResources) > 0 {
			region := existingResources[0].Region
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_xray_sampling_rules_exist",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"region": region, "reason": "No custom X-Ray sampling rules found. Default rules are being used."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: ServiceNameXray,
				ResourceId:          account.AccountNumber,
				ResourceType:        getAwsServiceResourceType(ServiceNameXray, "sampling-configuration"),
				ResourceRegion:      region,
			})
		}
	}

	return recommendations, nil
}

func (a *amazonXray) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)

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
				LogGroupName:        aws.String(logGroupName),
				LogStreamNamePrefix: aws.String(resourceId),
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, err
}

func (a *amazonXray) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "xray",
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
