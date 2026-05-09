package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
)

type pipelinesService struct {
}

func (s *pipelinesService) Name() string {
	return "microsoft.devops/pipelines"
}

// Scope returns the service scope - Pipelines is a global service
func (s *pipelinesService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

// DevOpsConfig represents Azure DevOps connection configuration
type DevOpsConfig struct {
	OrganizationURL string `json:"organization_url"`
	PAT             string `json:"pat"`
}

func (s *pipelinesService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	// Parse Azure DevOps credentials from account.Data
	if account.Data == nil || *account.Data == "" {
		// No DevOps configuration found, return empty list (not an error)
		return []providers.Resource{}, nil
	}

	var devopsConfig DevOpsConfig
	if err := json.Unmarshal([]byte(*account.Data), &devopsConfig); err != nil {
		// If Data is not in the expected format, try to use it as organization URL
		// and look for PAT in environment or return empty
		return []providers.Resource{}, nil
	}

	if devopsConfig.OrganizationURL == "" || devopsConfig.PAT == "" {
		// Incomplete configuration, return empty
		return []providers.Resource{}, nil
	}

	// Create Azure DevOps connection
	connection := azuredevops.NewPatConnection(devopsConfig.OrganizationURL, devopsConfig.PAT)

	var allResources []providers.Resource

	// Get all projects first
	coreClient, err := core.NewClient(ctx.GetContext(), connection)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client: %w", err)
	}

	projectArgs := core.GetProjectsArgs{}
	projectsResult, err := coreClient.GetProjects(ctx.GetContext(), projectArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to get projects: %w", err)
	}

	if projectsResult == nil {
		return []providers.Resource{}, nil
	}

	// For each project, get pipelines
	for _, project := range projectsResult.Value {
		if project.Name == nil {
			continue
		}

		projectName := *project.Name
		projectID := ""
		if project.Id != nil {
			projectID = project.Id.String()
		}

		// Get Pipelines using the pipelines client
		pipelinesClient := pipelines.NewClient(ctx.GetContext(), connection)

		listArgs := pipelines.ListPipelinesArgs{
			Project: &projectName,
		}

		pipelinesList, err := pipelinesClient.ListPipelines(ctx.GetContext(), listArgs)
		if err != nil {
			// Project might not have pipelines configured, continue
			continue
		}

		if pipelinesList != nil {
			for _, pipeline := range *pipelinesList {
				if pipeline.Id == nil || pipeline.Name == nil {
					continue
				}

				status := providers.ResourceStatusActive

				meta := make(map[string]any)
				meta["project"] = projectName
				meta["projectId"] = projectID

				if pipeline.Folder != nil {
					meta["folder"] = *pipeline.Folder
				}
				if pipeline.Revision != nil {
					meta["revision"] = *pipeline.Revision
				}

				pipelineID := fmt.Sprintf("%d", *pipeline.Id)
				resourceID := fmt.Sprintf("%s/_apis/pipelines/%s", devopsConfig.OrganizationURL, pipelineID)

				allResources = append(allResources, providers.Resource{
					Id:          resourceID,
					Name:        *pipeline.Name,
					Type:        "Microsoft.DevOps/pipelines",
					Region:      "global",
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         resourceID,
					ServiceName: s.Name(),
				})
			}
		}

		// Also get recent build runs to check for failures
		buildClient, err := build.NewClient(ctx.GetContext(), connection)
		if err != nil {
			continue
		}

		buildArgs := build.GetBuildsArgs{
			Project: &projectName,
			Top:     func() *int { i := 100; return &i }(), // Get last 100 builds
		}

		builds, err := buildClient.GetBuilds(ctx.GetContext(), buildArgs)
		if err != nil {
			continue
		}

		if builds != nil {
			for _, buildItem := range builds.Value {
				if buildItem.Id == nil || buildItem.Definition == nil || buildItem.Definition.Name == nil {
					continue
				}

				status := providers.ResourceStatusActive
				if buildItem.Status != nil {
					switch *buildItem.Status {
					case build.BuildStatusValues.Completed:
						if buildItem.Result != nil {
							switch *buildItem.Result {
							case build.BuildResultValues.Succeeded:
								status = providers.ResourceStatusActive
							case build.BuildResultValues.Failed:
								status = providers.ResourceStatusInactive
							case build.BuildResultValues.Canceled:
								status = providers.ResourceStatusInactive
							}
						}
					case build.BuildStatusValues.InProgress:
						status = providers.ResourceStatusActive
					}
				}

				meta := make(map[string]any)
				meta["project"] = projectName
				meta["definitionName"] = *buildItem.Definition.Name
				meta["buildNumber"] = ""
				if buildItem.BuildNumber != nil {
					meta["buildNumber"] = *buildItem.BuildNumber
				}
				if buildItem.SourceBranch != nil {
					meta["sourceBranch"] = *buildItem.SourceBranch
				}
				if buildItem.StartTime != nil {
					meta["startTime"] = buildItem.StartTime.Time.Format(time.RFC3339)
				}
				if buildItem.FinishTime != nil {
					meta["finishTime"] = buildItem.FinishTime.Time.Format(time.RFC3339)
				}

				buildID := fmt.Sprintf("%d", *buildItem.Id)
				resourceID := fmt.Sprintf("%s/%s/_build/results?buildId=%s", devopsConfig.OrganizationURL, projectName, buildID)

				allResources = append(allResources, providers.Resource{
					Id:          resourceID,
					Name:        fmt.Sprintf("%s - Build %s", *buildItem.Definition.Name, buildID),
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					CreatedAt:   buildItem.FinishTime.Time,
					Arn:         resourceID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *pipelinesService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Azure Pipelines doesn't use Azure Monitor metrics
	return providers.QueryMetricsResponse{}, errors.New("metrics not available for Azure Pipelines")
}

func (s *pipelinesService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	pipelineCount := 0
	buildCount := 0
	failedBuildCount := 0

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for pipeline definitions
		if strings.Contains(strings.ToLower(resource.Type), "pipeline") && !strings.Contains(strings.ToLower(resource.Type), "build") {
			pipelineCount++

			// Check if pipeline has no recent runs (check if there are corresponding builds)
			// This would require cross-referencing with builds, which is complex
			// For now, we'll just note the pipeline exists
		}

		// Check for build runs
		if strings.Contains(strings.ToLower(resource.Type), "build") {
			buildCount++

			// Check for failed builds
			if resource.Status == providers.ResourceStatusInactive {
				failedBuildCount++
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_pipeline_build_failed",
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

			// Check for builds without branch information
			if sourceBranch, ok := properties["sourceBranch"].(string); !ok || sourceBranch == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_pipeline_build_no_branch",
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
		}
	}

	// Check if there's a high failure rate
	if buildCount > 0 && failedBuildCount > 0 {
		failureRate := float64(failedBuildCount) / float64(buildCount)
		if failureRate > 0.3 { // More than 30% failure rate
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_pipeline_high_failure_rate",
				Severity:            providers.RecommendationSeverityCritical,
				Savings:             0,
				Data:                map[string]any{"failureRate": failureRate},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: s.Name(),
				ResourceId:          "organization-level",
				ResourceType:        "Microsoft.DevOps/pipelines",
				ResourceRegion:      "global",
			})
		}
	}

	return recommendations, nil
}

func (s *pipelinesService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *pipelinesService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Pipelines changes should be done through Azure DevOps UI or API
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Azure Pipelines: %s requires manual intervention through Azure DevOps portal", command.Command),
	}, errors.ErrUnsupported
}

func (s *pipelinesService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Azure Pipelines doesn't use Azure Monitor Log Analytics workspaces
	return "", errors.New("log groups not available for Azure Pipelines")
}

func (s *pipelinesService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
