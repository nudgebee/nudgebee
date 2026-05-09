package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
)

type appServicePlanService struct {
}

func (s *appServicePlanService) Name() string {
	return "microsoft.web/serverfarms"
}

func (s *appServicePlanService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *appServicePlanService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	var subscriptionIDs = strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}
		client, err := armappservice.NewPlansClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create app service plan client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, plan := range page.Value {
				status := providers.ResourceStatusUnknown
				if plan.Properties != nil && plan.Properties.ProvisioningState != nil {
					if *plan.Properties.ProvisioningState == "Succeeded" {
						status = providers.ResourceStatusActive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *plan.ID,
					Name:        *plan.Name,
					Type:        *plan.Type,
					Region:      *plan.Location,
					Tags:        toAzureTags(plan.Tags),
					Meta:        structToMap(plan),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *plan.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *appServicePlanService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *appServicePlanService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check for plans with no apps
			numberOfSites, _ := props["numberOfSites"].(float64)
			if numberOfSites == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "azure_app_service_plan_empty",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":     resource.Id,
						"resource_name":   resource.Name,
						"resource_type":   resource.Type,
						"resource_region": resource.Region,
						"service_name":    resource.ServiceName,
						"reason":          "App Service Plan has no apps deployed. Empty plans still incur charges and should be deleted if no longer needed.",
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if sku, ok := meta["sku"].(map[string]interface{}); ok {
			tier, _ := sku["tier"].(string)
			capacity, _ := sku["capacity"].(float64)

			// Check for oversized plans (high capacity, low usage)
			if capacity > 1 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "azure_app_service_plan_overprovisioned",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"resource_id":       resource.Id,
						"resource_name":     resource.Name,
						"resource_type":     resource.Type,
						"resource_region":   resource.Region,
						"service_name":      resource.ServiceName,
						"current_tier":      tier,
						"current_instances": capacity,
						"reason":            "App Service Plan has multiple instances provisioned. Review if the current capacity is needed based on actual traffic patterns. Consider using autoscaling instead of fixed instance counts.",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for premium plans that could use standard
			if tier == "Premium" || tier == "PremiumV2" || tier == "PremiumV3" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "azure_app_service_plan_premium_review",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"resource_id":     resource.Id,
						"resource_name":   resource.Name,
						"resource_type":   resource.Type,
						"resource_region": resource.Region,
						"service_name":    resource.ServiceName,
						"current_tier":    tier,
						"reason":          "App Service Plan is using a Premium tier. Review if Premium features (VNet integration, more memory/CPU, deployment slots) are being utilized. Standard tier may be sufficient and more cost-effective.",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}
	return recommendations, nil
}

func (s *appServicePlanService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *appServicePlanService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, planName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "serverfarms" && i+1 < len(parts) {
			planName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || planName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or plan name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armappservice.NewPlansClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create app service plan client: %v", err),
		}, err
	}

	switch command.Command {
	case "azure_app_service_plan_empty":
		logger.Info("applying command: deleting empty app service plan", "planName", planName)
		_, err := client.Delete(ctx.GetContext(), resourceGroup, planName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete app service plan: %v", err),
			}, err
		}
		logger.Info("successfully deleted app service plan", "planName", planName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted empty app service plan '%s'", planName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *appServicePlanService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *appServicePlanService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "serverfarm",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
