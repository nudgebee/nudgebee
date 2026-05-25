package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/smithy-go"
)

type amazonMsk struct {
	DefaultAwsServiceImpl
}

func (a *amazonMsk) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonMsk) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonMsk) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonMsk) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameMSK)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	resources := []providers.Resource{}
	svc := kafka.NewFromConfig(cfg)
	paginator := kafka.NewListClustersV2Paginator(svc, &kafka.ListClustersV2Input{})
	for paginator.HasMorePages() {
		result, err := paginator.NextPage(context.TODO())
		if err != nil {
			var apiErr *smithy.GenericAPIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region")) {
				ctx.GetLogger().Warn("MSK API might not be available or fully supported in this region", "region", regionName, "error", apiErr.ErrorCode())
				return resources, nil // Return empty list if region not supported
			}
			ctx.GetLogger().Error("failed to list msk clusters", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, cluster := range result.ClusterInfoList {
			clusterDetail, err := svc.DescribeClusterV2(context.TODO(), &kafka.DescribeClusterV2Input{
				ClusterArn: cluster.ClusterArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe msk cluster", "error", err, "clusterArn", *cluster.ClusterArn, "accountNumber", account.AccountNumber, "region", regionName)
				// Skip this cluster if describe fails
				continue
			}
			tags := make(map[string][]string)
			for k, v := range clusterDetail.ClusterInfo.Tags {
				tags[k] = append(tags[k], v)
			}

			// Map MSK state to standard status
			status := providers.ResourceStatusUnknown
			if clusterDetail.ClusterInfo.State != "" {
				switch clusterDetail.ClusterInfo.State {
				case types.ClusterStateActive, types.ClusterStateUpdating, types.ClusterStateMaintenance:
					status = providers.ResourceStatusActive
				case types.ClusterStateCreating:
					status = providers.ResourceStatusActive
				case types.ClusterStateDeleting:
					status = providers.ResourceStatusDeleted
				case types.ClusterStateFailed:
					status = providers.ResourceStatusInactive // Or Error
				default:
					status = providers.ResourceStatusUnknown
				}
			}

			resource := providers.Resource{
				Id:          *clusterDetail.ClusterInfo.ClusterArn, // Use ARN as it's unique
				ServiceName: ServiceNameMSK,
				Name:        *clusterDetail.ClusterInfo.ClusterName,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(clusterDetail.ClusterInfo), // Store ClusterInfo in Meta
				Arn:         *clusterDetail.ClusterInfo.ClusterArn,
				CreatedAt:   *clusterDetail.ClusterInfo.CreationTime,
				Type:        getAwsServiceResourceType(ServiceNameMSK, "cluster"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonMsk) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameMSK)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		// Ensure we are looking at MSK Clusters
		if resource.Type != getAwsServiceResourceType(ServiceNameMSK, "cluster") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Encryption in Transit (ClientBroker setting)
		encryptionInTransitEnabled := false // Default to false
		if encInfo, ok := meta["EncryptionInfo"].(map[string]any); ok {
			if encTransit, ok := encInfo["EncryptionInTransit"].(map[string]any); ok {
				if clientBroker, ok := encTransit["ClientBroker"].(string); ok {
					// Consider enabled if TLS or TLS_PLAINTEXT is set
					if clientBroker == string(types.ClientBrokerTls) || clientBroker == string(types.ClientBrokerTlsPlaintext) {
						encryptionInTransitEnabled = true
					}
				}
			}
		}
		if !encryptionInTransitEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_msk_encryption_in_transit",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Encryption in transit (TLS) between clients and brokers is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Encryption at Rest (DataVolumeKMSKeyId presence)
		encryptionAtRestEnabled := false
		if encInfo, ok := meta["EncryptionInfo"].(map[string]any); ok {
			if encRest, ok := encInfo["EncryptionAtRest"].(map[string]any); ok {
				if keyId, ok := encRest["DataVolumeKMSKeyId"].(string); ok && keyId != "" {
					encryptionAtRestEnabled = true
				}
			}
		}
		if !encryptionAtRestEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_msk_encryption_at_rest",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Encryption at rest for data volumes is not enabled (or not using KMS)."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Public Access Disabled
		publicAccessEnabled := false
		if brokerInfo, ok := meta["BrokerNodeGroupInfo"].(map[string]any); ok {
			if connInfo, ok := brokerInfo["ConnectivityInfo"].(map[string]any); ok {
				if pubAccess, ok := connInfo["PublicAccess"].(map[string]any); ok {
					if accessType, ok := pubAccess["Type"].(string); ok && accessType != "DISABLED" {
						publicAccessEnabled = true
					}
				}
			}
		}
		if publicAccessEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_msk_public_access_disabled",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Public access to brokers is enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Enhanced Monitoring Enabled
		// Check if EnhancedMonitoring field exists and is not DEFAULT
		enhancedMonitoringLevel := types.EnhancedMonitoringDefault // Default level
		if level, ok := meta["EnhancedMonitoring"].(string); ok {
			enhancedMonitoringLevel = types.EnhancedMonitoring(level)
		}
		if enhancedMonitoringLevel == types.EnhancedMonitoringDefault {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Monitoring
				RuleName:            "aws_msk_enhanced_monitoring",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Enhanced monitoring is set to DEFAULT. Consider PER_BROKER or PER_TOPIC_PER_BROKER."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 5: CloudWatch Logs Publishing Enabled
		logsToCloudWatchEnabled := false
		if logInfo, ok := meta["LoggingInfo"].(map[string]any); ok {
			if brokerLogs, ok := logInfo["BrokerLogs"].(map[string]any); ok {
				if cwLogs, ok := brokerLogs["CloudWatchLogs"].(map[string]any); ok {
					if enabled, ok := cwLogs["Enabled"].(bool); ok && enabled {
						logsToCloudWatchEnabled = true
					}
				}
			}
		}
		if !logsToCloudWatchEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Monitoring/Auditing
				RuleName:            "aws_msk_cloudwatch_logs",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Broker logs are not configured to publish to CloudWatch Logs."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 6: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Idle/Underutilized check based on metrics (CPU, Memory, Network, Disk)
		// - Broker instance type generation/right-sizing
		// - Kafka version check
	}

	return recommendations, nil
}

func (a *amazonMsk) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonMsk) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "msk",
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
