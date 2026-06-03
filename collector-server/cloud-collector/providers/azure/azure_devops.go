package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
)

type devopsService struct {
}

func (s *devopsService) Name() string {
	return "microsoft.devops/projects"
}

// Scope returns the service scope - DevOps is a global service
func (s *devopsService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *devopsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	// Azure DevOps uses a different authentication mechanism than Azure Resource Manager
	// It requires an organization URL and Personal Access Token (PAT)
	// For now, this service returns empty as it needs separate Azure DevOps-specific configuration
	// In a production system, you would:
	// 1. Parse account.Data for DevOps credentials (org URL + PAT)
	// 2. Use github.com/microsoft/azure-devops-go-api/azuredevops/v7 SDK
	// 3. Fetch projects, repositories, and pipelines

	return []providers.Resource{}, nil
}

func (s *devopsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Azure DevOps doesn't use Azure Monitor metrics
	return providers.QueryMetricsResponse{}, errors.New("metrics not available for Azure DevOps")
}

func (s *devopsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	projectCount := 0
	repoCount := 0
	pipelineCount := 0

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for projects
		if strings.Contains(strings.ToLower(resource.Type), "project") && !strings.Contains(strings.ToLower(resource.Type), "pipeline") {
			projectCount++

			// Check for projects without description
			if description, ok := properties["description"].(string); !ok || description == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_devops_project_no_description",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for public projects (potential security issue)
			if visibility, ok := properties["visibility"].(string); ok && visibility == "public" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_devops_project_public_visibility",
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

		// Check for repositories
		if strings.Contains(strings.ToLower(resource.Type), "repositor") {
			repoCount++

			if resource.Status == providers.ResourceStatusInactive {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_devops_repository_disabled",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for large repositories (potential performance issue)
			if size, ok := properties["size"].(int64); ok && size > 1000000000 { // > 1GB
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_devops_repository_large_size",
					Severity:            providers.RecommendationSeverityMedium,
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

		// Check for pipelines
		if strings.Contains(strings.ToLower(resource.Type), "pipeline") {
			pipelineCount++
		}
	}

	// Check if there are projects without pipelines
	if projectCount > 0 && pipelineCount == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "azure_devops_no_pipelines",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             0,
			Data:                map[string]any{},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: s.Name(),
			ResourceId:          "organization-level",
			ResourceType:        "Microsoft.DevOps/pipelines",
			ResourceRegion:      "global",
		})
	}

	return recommendations, nil
}

func (s *devopsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *devopsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// DevOps changes should be done through the Azure DevOps UI or API with proper permissions
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Azure DevOps: %s requires manual intervention through Azure DevOps portal", command.Command),
	}, errors.ErrUnsupported
}

func (s *devopsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Azure DevOps doesn't use Azure Monitor Log Analytics workspaces
	return "", errors.New("log groups not available for Azure DevOps")
}

func (s *devopsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
