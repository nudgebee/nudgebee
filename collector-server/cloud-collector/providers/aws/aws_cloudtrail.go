package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go/ptr"
)

type awsCloudTrail struct {
	DefaultAwsServiceImpl
}

func (a *awsCloudTrail) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsCloudTrail) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsCloudTrail) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsCloudTrail) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameCloudTrail)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := cloudtrail.NewFromConfig(cfg)
	resources := []providers.Resource{}

	edsPaginator := cloudtrail.NewListEventDataStoresPaginator(svc, &cloudtrail.ListEventDataStoresInput{})
	for edsPaginator.HasMorePages() {
		datastoresOutput, err := edsPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Warn("failed to list cloudtrail event data stores", "error", err, "region", regionName)
			break
		}

		for _, datastore := range datastoresOutput.EventDataStores {
			if datastore.Name == nil || datastore.EventDataStoreArn == nil {
				continue
			}

			tags := make(map[string][]string)
			meta := structToMap(datastore)
			createdAt := time.Now()
			status := providers.ResourceStatusActive

			details, err := svc.GetEventDataStore(ctx.GetContext(), &cloudtrail.GetEventDataStoreInput{
				EventDataStore: datastore.EventDataStoreArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get cloudtrail event data store details", "error", err, "arn", *datastore.EventDataStoreArn)
			} else {
				meta = structToMap(details)
				if details.Status != "" {
					status = providers.ResourceStatusActive // Simplified status mapping
				}
				if details.CreatedTimestamp != nil {
					createdAt = *details.CreatedTimestamp
				}
			}

			tagsOutput, err := svc.ListTags(ctx.GetContext(), &cloudtrail.ListTagsInput{
				ResourceIdList: []string{*datastore.EventDataStoreArn},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to list tags for event data store", "error", err, "arn", *datastore.EventDataStoreArn)
			} else if len(tagsOutput.ResourceTagList) > 0 {
				for _, tag := range tagsOutput.ResourceTagList[0].TagsList {
					tags[ptr.ToString(tag.Key)] = append(tags[ptr.ToString(tag.Key)], ptr.ToString(tag.Value))
				}
			}

			resource := providers.Resource{
				Id:          aws.ToString(datastore.Name),
				ServiceName: ServiceNameCloudTrail,
				Name:        aws.ToString(datastore.Name),
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(datastore.EventDataStoreArn),
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameCloudTrail, "eventdatastore"),
			}
			resources = append(resources, resource)
		}
	}

	trailsOutput, err := svc.DescribeTrails(ctx.GetContext(), &cloudtrail.DescribeTrailsInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to describe cloudtrail trails", "error", err, "region", regionName)
		return resources, err
	}

	for _, trail := range trailsOutput.TrailList {
		if trail.Name == nil || trail.TrailARN == nil {
			continue
		}
		if trail.HomeRegion != nil && *trail.HomeRegion != regionName {
			continue
		}

		tags := make(map[string][]string)
		meta := structToMap(trail)
		status := providers.ResourceStatusUnknown

		tagsOutput, err := svc.ListTags(ctx.GetContext(), &cloudtrail.ListTagsInput{
			ResourceIdList: []string{*trail.TrailARN},
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to list tags for trail", "error", err, "arn", *trail.TrailARN)
		} else if len(tagsOutput.ResourceTagList) > 0 {
			for _, tag := range tagsOutput.ResourceTagList[0].TagsList {
				tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
			}
		}

		statusOutput, err := svc.GetTrailStatus(ctx.GetContext(), &cloudtrail.GetTrailStatusInput{
			Name: trail.TrailARN,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get trail status", "error", err, "arn", *trail.TrailARN)
		} else {
			meta["TrailStatus"] = structToMap(statusOutput)
			if aws.ToBool(statusOutput.IsLogging) {
				status = providers.ResourceStatusActive
			} else {
				status = providers.ResourceStatusInactive
			}
		}

		resource := providers.Resource{
			Id:          aws.ToString(trail.Name),
			ServiceName: ServiceNameCloudTrail,
			Name:        aws.ToString(trail.Name),
			Status:      status,
			Region:      regionName,
			Tags:        tags,
			Meta:        meta,
			Arn:         aws.ToString(trail.TrailARN),
			CreatedAt:   time.Now(), // Actual creation time not available
			Type:        getAwsServiceResourceType(ServiceNameCloudTrail, "trail"),
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (a *awsCloudTrail) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	multiRegionTrailExists := false
	trailsChecked := false

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameCloudTrail, "trail") {
			trailsChecked = true

			if isMultiRegion, ok := meta["IsMultiRegionTrail"].(bool); ok {
				if isMultiRegion {
					multiRegionTrailExists = true
				} else {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "aws_cloudtrail_multi_region",
						Severity:            providers.RecommendationSeverityHigh,
						Data:                map[string]any{"trail_name": resource.Name, "reason": "Trail is not multi-region."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			if validation, ok := meta["LogFileValidationEnabled"].(bool); ok && !validation {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_cloudtrail_log_validation",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"trail_name": resource.Name},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			if kmsKey, ok := meta["KmsKeyId"].(string); !ok || kmsKey == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_cloudtrail_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"trail_name": resource.Name, "reason": "Not encrypted with CMK."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			if cwLogsArn, ok := meta["CloudWatchLogsLogGroupArn"].(string); !ok || cwLogsArn == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_cloudtrail_cloudwatch_integration",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"trail_name": resource.Name, "reason": "Not integrated with CloudWatch Logs."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			if resource.Status != providers.ResourceStatusActive {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_cloudtrail_logging_enabled",
					Severity:            providers.RecommendationSeverityHigh,
					Data:                map[string]any{"trail_name": resource.Name, "reason": "Logging is disabled."},
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
					Data:                map[string]any{"trail_name": resource.Name},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameCloudTrail, "eventdatastore") {
			retentionDays := 0
			if retention, ok := meta["RetentionPeriod"].(float64); ok {
				retentionDays = int(retention)
			}
			termProtectionEnabled := false
			if termProt, ok := meta["TerminationProtectionEnabled"].(bool); ok {
				termProtectionEnabled = termProt
			}

			if retentionDays > 365 && !termProtectionEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "aws_cloudtrail_eds_retention",
					Severity:            providers.RecommendationSeverityLow,
					Data:                map[string]any{"eds_name": resource.Name, "retention_days": retentionDays, "reason": "Long retention without termination protection."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			if kmsKeyId, ok := meta["KmsKeyId"].(string); !ok || kmsKeyId == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_cloudtrail_eds_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"eds_name": resource.Name, "reason": "Not encrypted with CMK."},
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
					Data:                map[string]any{"eds_name": resource.Name},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	if trailsChecked && !multiRegionTrailExists {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "aws_cloudtrail_no_multi_region_trail",
			Severity:            providers.RecommendationSeverityHigh,
			Data:                map[string]any{"reason": "No multi-region CloudTrail trail found."},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: ServiceNameCloudTrail,
			ResourceId:          account.AccountNumber,
			ResourceType:        getAwsServiceResourceType(ServiceNameCloudTrail, "account-setting"),
			ResourceRegion:      "global",
		})
	}

	return recommendations, nil
}

func (a *awsCloudTrail) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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
			logGroupName := ptr.ToString(lg.LogGroupName)
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        ptr.String(logGroupName),
				LogStreamNamePrefix: ptr.String(resourceId),
				Limit:               ptr.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *awsCloudTrail) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "cloudtrail",
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
