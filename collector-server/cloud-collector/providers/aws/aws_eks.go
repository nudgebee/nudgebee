package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

type amazonEks struct {
	DefaultAwsServiceImpl
}

func (a *amazonEks) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonEks) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonEks) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func eksStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*status)
	switch s {
	case "active":
		return providers.ResourceStatusActive
	case "creating":
		return providers.ResourceStatusActive
	case "deleting":
		return providers.ResourceStatusDeleted // Or Inactive
	case "failed":
		return providers.ResourceStatusInactive // Or Error
	case "updating":
		return providers.ResourceStatusActive // Or Pending, depending on definition
	default:
		return providers.ResourceStatusUnknown
	}
}
func (a *amazonEks) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameEKS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := eks.NewFromConfig(cfg)
	resources := []providers.Resource{}
	paginator := eks.NewListClustersPaginator(svc, &eks.ListClustersInput{})

	for paginator.HasMorePages() {
		clustersOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list eks clusters", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, clusterName := range clustersOutput.Clusters {
			tags := make(map[string][]string)

			clusterDetails, err := svc.DescribeCluster(ctx.GetContext(), &eks.DescribeClusterInput{
				Name: &clusterName,
			})
			if err != nil {
				ctx.GetLogger().Error("failed to fetch eks cluster details", "error", err, "clusterName", clusterName, "accountNumber", account.AccountNumber, "region", regionName)
				continue
			}

			if clusterDetails.Cluster == nil || clusterDetails.Cluster.Arn == nil || clusterDetails.Cluster.CreatedAt == nil || clusterDetails.Cluster.Status == "" {
				ctx.GetLogger().Warn("Skipping EKS cluster due to missing essential fields in describe response", "clusterName", clusterName)
				continue
			}

			for k, v := range clusterDetails.Cluster.Tags {
				tags[k] = append(tags[k], v)
			}

			meta := structToMap(clusterDetails.Cluster)

			resource := providers.Resource{
				Id:          clusterName,
				ServiceName: ServiceNameEKS,
				Name:        clusterName,
				Status:      eksStatusToNbStatus((*string)(&clusterDetails.Cluster.Status)),
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *clusterDetails.Cluster.Arn,
				CreatedAt:   *clusterDetails.Cluster.CreatedAt,
				Type:        getAwsServiceResourceType(ServiceNameEKS, "cluster"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *amazonEks) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// EKS Cluster Endpoint Public Access
		if resourcesVpcConfigAny, ok := meta["resourcesVpcConfig"]; ok {
			resourceVpcConfig, _ := resourcesVpcConfigAny.(map[string]interface{})
			if resourceVpcConfig != nil {
				cidrs, _ := resourceVpcConfig["publicAccessCidrs"].([]any)
				if resourceVpcConfig["endpointPublicAccess"] == true && slices.Contains(cidrs, "0.0.0.0/0") {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "aws_eks_public_access",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		// EKS Cluster Logging
		if loggingAny, ok := meta["logging"]; ok {
			logging, _ := loggingAny.(map[string]interface{})
			if logging != nil {
				clusterLogging, _ := logging["clusterLogging"].([]any)
				loggingDisabled := len(clusterLogging) == 0
				if !loggingDisabled {
					if firstEntry, ok := clusterLogging[0].(map[string]interface{}); ok {
						loggingDisabled = firstEntry["enabled"] == false
					}
				}
				if loggingDisabled {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "aws_eks_logging",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		// Enable Envelope Encryption for EKS Kubernetes Secrets
		if _, ok := meta["encryptionConfig"]; !ok {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_eks_secret_encryption",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
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

func (a *amazonEks) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonEks) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "eks",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}

	return app, nil
}
