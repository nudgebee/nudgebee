package aws

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

type amazonEcr struct {
	DefaultAwsServiceImpl
}

func (a *amazonEcr) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonEcr) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonEcr) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonEcr) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameECR)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := ecr.NewFromConfig(cfg)
	resources := []providers.Resource{}
	paginator := ecr.NewDescribeRepositoriesPaginator(svc, &ecr.DescribeRepositoriesInput{})

	for paginator.HasMorePages() {
		repositoriesOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch ecr resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, repository := range repositoriesOutput.Repositories {
			if repository.RepositoryName == nil || repository.RepositoryArn == nil {
				ctx.GetLogger().Warn("Skipping ECR repository due to missing RepositoryName or RepositoryArn", "repository", repository, "region", regionName)
				continue
			}
			if repository.CreatedAt == nil {
				ctx.GetLogger().Warn("CreatedAt is nil for ECR repository", "repositoryArn", *repository.RepositoryArn, "region", regionName)
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &ecr.ListTagsForResourceInput{
				ResourceArn: repository.RepositoryArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch ecr tags", "error", err, "repositoryArn", *repository.RepositoryArn, "region", regionName)
			} else {
				if tagsOutput.Tags != nil {
					for _, v := range tagsOutput.Tags {
						if v.Key != nil && v.Value != nil {
							tags[*v.Key] = append(tags[*v.Key], *v.Value)
						}
					}
				} else {
					ctx.GetLogger().Info("tagsOutput.Tags is nil for repository", "repositoryArn", *repository.RepositoryArn, "region", regionName)
				}
			}

			createdAt := time.Time{}
			if repository.CreatedAt != nil {
				createdAt = *repository.CreatedAt
			}

			resource := providers.Resource{
				Id:          *repository.RepositoryName,
				ServiceName: ServiceNameECR,
				Name:        *repository.RepositoryName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(repository),
				Arn:         *repository.RepositoryArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameECR, "repository"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *amazonEcr) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for ECR repository", "repositoryArn", resource.Arn, "region", resource.Region)
			continue
		}

		// Check if repos have tags immutability enabled
		imageTagMutabilityStr, itmOk := meta["ImageTagMutability"].(string)
		if !itmOk {
			// If the key doesn't exist or is not a string, it implies mutability (default) or an unknown state.
			// For safety, or to align with a policy that requires explicit IMMUTABLE, recommend.
			ctx.GetLogger().Warn("ImageTagMutability attribute missing or not a string for ECR repository", "repositoryArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", meta["ImageTagMutability"]))
			// Proceed to recommend, as non-immutable is the concern.
		}
		// ecr.ImageTagMutabilityImmutable is "IMMUTABLE"
		if imageTagMutabilityStr != string(types.ImageTagMutabilityImmutable) {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_ecr_tag_immutable",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"repository_name":    resource.Name,
					"repository_arn":     resource.Arn,
					"current_mutability": imageTagMutabilityStr, // Provide current value if available
					"reason":             "Image tag mutability is not set to IMMUTABLE.",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check if repos have scan enabled on push
		scanningEnabled := false
		scanningConfigAny, scOk := meta["ImageScanningConfiguration"]
		if scOk {
			scanningConfig, scMapOk := scanningConfigAny.(map[string]any)
			if scMapOk {
				scanOnPushAny, sopOk := scanningConfig["ScanOnPush"]
				if sopOk {
					scanOnPushBool, sopTypeOk := scanOnPushAny.(bool)
					if sopTypeOk {
						scanningEnabled = scanOnPushBool
					} else {
						ctx.GetLogger().Warn("ImageScanningConfiguration.ScanOnPush attribute is not a boolean for ECR repository", "repositoryArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", scanOnPushAny))
					}
				} else {
					ctx.GetLogger().Warn("ImageScanningConfiguration.ScanOnPush attribute missing for ECR repository", "repositoryArn", resource.Arn, "region", resource.Region)
				}
			} else {
				ctx.GetLogger().Warn("ImageScanningConfiguration attribute is not a map for ECR repository", "repositoryArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", scanningConfigAny))
			}
		} else {
			ctx.GetLogger().Info("ImageScanningConfiguration attribute missing for ECR repository, assuming scan on push is not enabled.", "repositoryArn", resource.Arn, "region", resource.Region)
		}

		if !scanningEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "aws_ecr_pushscan_enabled",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"repository_name": resource.Name,
					"repository_arn":  resource.Arn,
					"reason":          "Image scanning on push is not enabled.",
				},
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

func (a *amazonEcr) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonEcr) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ecr",
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
