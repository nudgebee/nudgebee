package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

type amazonCloudwatch struct {
	DefaultAwsServiceImpl
}

func (a *amazonCloudwatch) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonCloudwatch) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonCloudwatch) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonCloudwatch) ListMetrics(_ providers.CloudProviderContext, _ providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	return listAwsCloudwatchMetrics(request)
}

func (a *amazonCloudwatch) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameCloudWatch)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := cloudwatchlogs.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(svc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		logGroupsOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch cloudwatch log groups", "error", err, "region", regionName)
			return resources, err
		}

		for _, logGroup := range logGroupsOutput.LogGroups {
			if logGroup.LogGroupName == nil || *logGroup.LogGroupName == "" || logGroup.Arn == nil {
				continue
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &cloudwatchlogs.ListTagsForResourceInput{
				ResourceArn: logGroup.Arn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for log group", "error", err, "logGroupArn", *logGroup.Arn)
			} else {
				for k, v := range tagsOutput.Tags {
					tags[k] = append(tags[k], v)
				}
			}

			resource := providers.Resource{
				Id:          *logGroup.LogGroupName,
				ServiceName: ServiceNameCloudWatch,
				Name:        *logGroup.LogGroupName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(logGroup),
				Arn:         *logGroup.Arn,
				CreatedAt:   time.UnixMilli(*logGroup.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameCloudWatch, "log-group"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonCloudwatch) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	for _, resource := range existingResources {
		if resource.Type != getAwsServiceResourceType(ServiceNameCloudWatch, "log-group") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		retentionSet := false
		if retentionDaysAny, rdOk := meta["RetentionInDays"]; rdOk && retentionDaysAny != nil {
			// Handle both *int32 (from AWS SDK via structToMap) and float64 (from JSON unmarshal)
			switch v := retentionDaysAny.(type) {
			case *int32:
				retentionSet = *v > 0
			case float64:
				retentionSet = v > 0
			case int32:
				retentionSet = v > 0
			case int:
				retentionSet = v > 0
			}
		}

		if !retentionSet {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "aws_cloudwatch_log_group_retention",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"log_group_name": resource.Name, "reason": "Retention period not set."},
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
				RuleName:            "aws_cloudwatch_log_group_encryption_cmk",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"log_group_name": resource.Name, "reason": "Not encrypted with CMK."},
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
				Data:                map[string]any{"log_group_name": resource.Name},
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

func (a *amazonCloudwatch) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *amazonCloudwatch) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	cfg.Region = region
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "cloudwatch",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	cwSvc := cloudwatch.NewFromConfig(cfg)

	alarmName := resourceId
	if strings.HasPrefix(resourceId, "arn:aws:cloudwatch:") {
		parts := strings.Split(resourceId, ":")
		if len(parts) > 6 && parts[5] == "alarm" {
			alarmName = parts[6]
		}
	}
	describeAlarmsOutput, err := cwSvc.DescribeAlarms(ctx.GetContext(), &cloudwatch.DescribeAlarmsInput{AlarmNames: []string{alarmName}})
	if err != nil {
		return app, err
	}
	if len(describeAlarmsOutput.MetricAlarms) > 0 {
		alarm := describeAlarmsOutput.MetricAlarms[0]
		app.Id.Name = *alarm.AlarmArn
		app.Status = string(alarm.StateValue)
		if alarm.Namespace != nil && len(alarm.Dimensions) > 0 {
			nsParts := strings.Split(*alarm.Namespace, "/")
			svcName := ""
			if len(nsParts) > 1 {
				svcName = strings.ToLower(nsParts[1])
			}
			for _, dim := range alarm.Dimensions {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *dim.Value, Kind: svcName, Namespace: region}}.ToDownstreamLink())
			}
		}
		for _, actionArn := range alarm.AlarmActions {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: actionArn, Kind: "sns", Namespace: region}}.ToDownstreamLink())
		}
	}

	return app, nil
}
