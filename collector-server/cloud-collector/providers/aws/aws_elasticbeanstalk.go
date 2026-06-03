package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	//"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk"
	"github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk/types"
)

func elasticbeanstalkStatusToNbStatus(status string) providers.ResourceStatus {
	switch strings.ToLower(status) {
	case "launching", "updating", "ready":
		return providers.ResourceStatusActive
	case "terminating", "terminated":
		return providers.ResourceStatusDeleted
	case "terminationfailed", "invalidstate":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsElasticBeanstalk struct {
	DefaultAwsServiceImpl
}

func (a *awsElasticBeanstalk) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsElasticBeanstalk) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsElasticBeanstalk) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsElasticBeanstalk) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Elastic Beanstalk uses multiple log groups per environment
	return fmt.Sprintf("/aws/elasticbeanstalk/%s", resourceId), nil
}

func (a *awsElasticBeanstalk) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := elasticbeanstalk.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List all applications first
	appsResult, err := svc.DescribeApplications(context.TODO(), &elasticbeanstalk.DescribeApplicationsInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to fetch elasticbeanstalk applications", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return resources, err
	}

	// For each application, get its environments
	for _, app := range appsResult.Applications {
		if app.ApplicationName == nil {
			ctx.GetLogger().Warn("Skipping Elastic Beanstalk application due to missing name")
			continue
		}

		// Get environments for this application
		envsResult, err := svc.DescribeEnvironments(context.TODO(), &elasticbeanstalk.DescribeEnvironmentsInput{
			ApplicationName: app.ApplicationName,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch elasticbeanstalk environments", "error", err, "applicationName", *app.ApplicationName)
			continue
		}

		for _, env := range envsResult.Environments {
			if env.EnvironmentName == nil || env.EnvironmentId == nil {
				ctx.GetLogger().Warn("Skipping Elastic Beanstalk environment due to missing name or id")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the environment
			if env.EnvironmentArn != nil {
				tagsResult, err := svc.ListTagsForResource(context.TODO(), &elasticbeanstalk.ListTagsForResourceInput{
					ResourceArn: env.EnvironmentArn,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch elasticbeanstalk tags", "error", err, "environmentId", *env.EnvironmentId)
				} else if tagsResult.ResourceTags != nil {
					for _, tag := range tagsResult.ResourceTags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				}
			}

			// Get configuration settings for more details
			metaMap := structToMap(env)

			// Get environment resources (EC2 instances, load balancers, etc.)
			resourcesResult, err := svc.DescribeEnvironmentResources(context.TODO(), &elasticbeanstalk.DescribeEnvironmentResourcesInput{
				EnvironmentId: env.EnvironmentId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch elasticbeanstalk environment resources", "error", err, "environmentId", *env.EnvironmentId)
			} else if resourcesResult.EnvironmentResources != nil {
				metaMap["Resources"] = resourcesResult.EnvironmentResources
			}

			// Get environment health
			healthResult, err := svc.DescribeEnvironmentHealth(context.TODO(), &elasticbeanstalk.DescribeEnvironmentHealthInput{
				EnvironmentId: env.EnvironmentId,
				AttributeNames: []types.EnvironmentHealthAttribute{
					types.EnvironmentHealthAttributeAll,
				},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch elasticbeanstalk environment health", "error", err, "environmentId", *env.EnvironmentId)
			} else {
				metaMap["Health"] = structToMap(healthResult)
			}

			createdAt := time.Time{}
			if env.DateCreated != nil {
				createdAt = *env.DateCreated
			}

			status := providers.ResourceStatusUnknown
			if env.Status != "" {
				status = elasticbeanstalkStatusToNbStatus(string(env.Status))
			}

			arn := ""
			if env.EnvironmentArn != nil {
				arn = *env.EnvironmentArn
			}

			resource := providers.Resource{
				Id:          *env.EnvironmentId,
				ServiceName: ServiceNameElasticBeanstalk,
				Name:        *env.EnvironmentName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         arn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameElasticBeanstalk, "environment"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsElasticBeanstalk) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for outdated platform versions
		if platformArn, ok := meta["PlatformArn"].(string); ok && platformArn != "" {
			// Check if platform version is deprecated or outdated
			if strings.Contains(strings.ToLower(platformArn), "deprecated") {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "aws_elasticbeanstalk_outdated_platform",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":     "Elastic Beanstalk environment is using a deprecated platform version",
						"platformArn": platformArn,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}

		// Check for single instance environments in production (should use load-balanced)
		if health, ok := meta["Health"].(map[string]any); ok {
			if instancesHealth, ok := health["InstancesHealth"].(map[string]any); ok {
				if total, ok := instancesHealth["Ok"].(int32); ok && total <= 1 {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_elasticbeanstalk_single_instance",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "Elastic Beanstalk environment is running on a single instance",
							"recommendation": "Consider using a load-balanced environment for high availability",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}
		}

		// Check environment health status
		if healthStatus, ok := meta["HealthStatus"].(string); ok {
			if strings.ToLower(healthStatus) == "severe" || strings.ToLower(healthStatus) == "degraded" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_elasticbeanstalk_unhealthy",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":      "Elastic Beanstalk environment is in unhealthy state",
						"healthStatus": healthStatus,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}

		// Check for enhanced health reporting
		if resources, ok := meta["Resources"].(map[string]any); ok {
			// If enhanced health is disabled, recommend enabling it
			enhancedHealthEnabled := false
			if loadBalancers, ok := resources["LoadBalancers"].([]any); ok && len(loadBalancers) > 0 {
				enhancedHealthEnabled = true
			}

			if !enhancedHealthEnabled {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_elasticbeanstalk_enhanced_health_disabled",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        "Enhanced health reporting is not enabled",
						"recommendation": "Enable enhanced health reporting for better monitoring",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}
	}

	return recommendations, nil
}

func (a *awsElasticBeanstalk) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "elasticbeanstalk",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// Placeholder for fetching service map details.
	// add logic here to describe the environment and its relationships.

	return app, nil
}
