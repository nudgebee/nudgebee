package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact"
	"github.com/aws/aws-sdk-go-v2/service/codeartifact/types"
	"github.com/aws/smithy-go"
)

type awsCodeArtifact struct {
	DefaultAwsServiceImpl
}

func (a *awsCodeArtifact) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsCodeArtifact) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsCodeArtifact) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsCodeArtifact) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameCodeArtifact)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := codeartifact.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := codeartifact.NewListRepositoriesPaginator(svc, &codeartifact.ListRepositoriesInput{})
	for paginator.HasMorePages() {
		repositoriesOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region") || apiErr.ErrorCode() == "AccessDeniedException") {
				ctx.GetLogger().Warn("CodeArtifact API not available/enabled in this region", "region", regionName, "error", apiErr.ErrorCode())
				return resources, nil
			}
			ctx.GetLogger().Error("failed to fetch codeartifact resources", "error", err, "region", regionName)
			return resources, err
		}

		for _, repository := range repositoriesOutput.Repositories {
			if repository.Name == nil || repository.Arn == nil || repository.CreatedTime == nil {
				continue
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &codeartifact.ListTagsForResourceInput{
				ResourceArn: repository.Arn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch codeartifact tags", "error", err, "repositoryArn", *repository.Arn)
			} else {
				for _, tag := range tagsOutput.Tags {
					tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
				}
			}

			resource := providers.Resource{
				Id:          aws.ToString(repository.Name),
				ServiceName: ServiceNameCodeArtifact,
				Name:        aws.ToString(repository.Name),
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(repository),
				Arn:         aws.ToString(repository.Arn),
				CreatedAt:   aws.ToTime(repository.CreatedTime),
				Type:        getAwsServiceResourceType(ServiceNameCodeArtifact, "repository"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *awsCodeArtifact) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config for recommendations", "error", err, "service", ServiceNameCodeArtifact)
		return recommendations, err
	}

	for _, resource := range existingResources {
		if resource.Type != getAwsServiceResourceType(ServiceNameCodeArtifact, "repository") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		cfg.Region = resource.Region
		svc := codeartifact.NewFromConfig(cfg)

		domainName, dnOK := meta["DomainName"].(string)
		domainOwner, doOK := meta["DomainOwner"].(string)
		repositoryName, rnOK := meta["Name"].(string)

		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags",
				Severity:            providers.RecommendationSeverityLow,
				Data:                map[string]any{"repository_name": resource.Name, "repository_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if dnOK && doOK && rnOK {
			_, err := svc.GetRepositoryPermissionsPolicy(ctx.GetContext(), &codeartifact.GetRepositoryPermissionsPolicyInput{
				Domain:      aws.String(domainName),
				DomainOwner: aws.String(domainOwner),
				Repository:  aws.String(repositoryName),
			})
			if err != nil {
				var rnfe *types.ResourceNotFoundException
				if errors.As(err, &rnfe) {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "aws_codeartifact_repository_policy",
						Severity:            providers.RecommendationSeverityLow,
						Data:                map[string]any{"repository_name": resource.Name, "reason": "No resource policy found."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				} else {
					ctx.GetLogger().Warn("failed to get codeartifact repository policy", "error", err, "repositoryName", resource.Name)
				}
			}
		}
	}

	return recommendations, nil
}

func (a *awsCodeArtifact) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsCodeArtifact) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "codeartifact",
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
