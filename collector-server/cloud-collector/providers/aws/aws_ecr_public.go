package aws

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
)

type amazonEcrPublic struct {
	DefaultAwsServiceImpl
}

func (a *amazonEcrPublic) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonEcrPublic) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonEcrPublic) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonEcrPublic) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameECRPublic)
		return []providers.Resource{}, err
	}

	// ECR Public API is only available in us-east-1
	if regionName != "us-east-1" {
		return []providers.Resource{}, nil
	}

	cfg.Region = regionName
	svc := ecrpublic.NewFromConfig(cfg)
	resources := []providers.Resource{}
	paginator := ecrpublic.NewDescribeRepositoriesPaginator(svc, &ecrpublic.DescribeRepositoriesInput{})

	for paginator.HasMorePages() {
		repositoriesOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch ecrpublic resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, repository := range repositoriesOutput.Repositories {
			if repository.RepositoryName == nil || repository.RepositoryArn == nil {
				ctx.GetLogger().Warn("Skipping ECR Public repository due to missing RepositoryName or RepositoryArn", "repository", repository, "region", regionName)
				continue
			}
			if repository.CreatedAt == nil {
				ctx.GetLogger().Warn("CreatedAt is nil for ECR Public repository", "repositoryArn", *repository.RepositoryArn, "region", regionName)
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &ecrpublic.ListTagsForResourceInput{
				ResourceArn: repository.RepositoryArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch ecrpublic tags", "error", err, "repositoryArn", *repository.RepositoryArn, "region", regionName)
			} else {
				if tagsOutput.Tags != nil {
					for _, v := range tagsOutput.Tags {
						if v.Key != nil && v.Value != nil {
							tags[*v.Key] = append(tags[*v.Key], *v.Value)
						}
					}
				} else {
					ctx.GetLogger().Info("tagsOutput.Tags is nil for ECR Public repository", "repositoryArn", *repository.RepositoryArn, "region", regionName)
				}
			}

			createdAt := time.Time{}
			if repository.CreatedAt != nil {
				createdAt = *repository.CreatedAt
			}

			resource := providers.Resource{
				Id:          *repository.RepositoryName,
				ServiceName: ServiceNameECRPublic,
				Name:        *repository.RepositoryName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(repository),
				Arn:         *repository.RepositoryArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameECRPublic, "repository"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *amazonEcrPublic) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		// Ensure we are looking at ECR Public Repositories
		if resource.Type != getAwsServiceResourceType(ServiceNameECRPublic, "repository") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for ECR Public repository", "repositoryArn", resource.Arn, "region", resource.Region)
			continue
		}

		// Check 1: Image Tag Immutability
		isImmutable := false
		var currentMutability string

		imageTagMutabilityAny, itmOk := meta["ImageTagMutability"]
		if itmOk {
			immutabilityStr, typeOk := imageTagMutabilityAny.(string)
			if typeOk {
				currentMutability = immutabilityStr
				if immutabilityStr == "IMMUTABLE" { // Use SDK constant
					isImmutable = true
				}
			} else {
				ctx.GetLogger().Warn("ImageTagMutability attribute is not a string for ECR Public repository", "repositoryArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", imageTagMutabilityAny))
				currentMutability = fmt.Sprintf("INVALID_TYPE (%T)", imageTagMutabilityAny)
			}
		} else {
			ctx.GetLogger().Info("ImageTagMutability attribute missing for ECR Public repository, assuming MUTABLE.", "repositoryArn", resource.Arn, "region", resource.Region)
			currentMutability = "MUTABLE (default/missing)"
		}

		if !isImmutable {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_ecrpublic_tag_immutable",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"repository_name":    resource.Name,
					"repository_arn":     resource.Arn,
					"current_mutability": currentMutability,
					"reason":             "Image tag mutability is not set to IMMUTABLE.",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"repository_name": resource.Name, "repository_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region, // Always us-east-1
			})
		}

		// Note: Encryption is always enabled by AWS for ECR Public.
		// Note: Image scanning configuration is not part of the repository settings for ECR Public.
	}

	return recommendations, nil
}

func (a *amazonEcrPublic) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonEcrPublic) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ecr-public",
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
