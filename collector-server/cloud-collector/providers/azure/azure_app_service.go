package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type appServiceService struct {
}

func (s *appServiceService) Name() string {
	return "Microsoft.Web/sites"
}

// Scope returns the service scope - this is a regional service
func (s *appServiceService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *appServiceService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armappservice.NewWebAppsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create app service client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, app := range page.Value {
				status := providers.ResourceStatusUnknown
				if app.Properties != nil && app.Properties.State != nil {
					if strings.ToLower(*app.Properties.State) == "running" {
						status = providers.ResourceStatusActive
					} else if strings.ToLower(*app.Properties.State) == "stopped" {
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *app.ID,
					Name:        *app.Name,
					Type:        *app.Type,
					Region:      *app.Location,
					Tags:        toAzureTags(app.Tags),
					Meta:        structToMap(app),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *app.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *appServiceService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *appServiceService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and app name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, appName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "sites" && i+1 < len(parts) {
			appName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || appName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or app name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armappservice.NewWebAppsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create app service client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_app_service_https_only_disabled":
		// Enable HTTPS only
		logger.Info("applying command: enabling HTTPS only", "appName", appName)

		appResp, err := client.Get(ctx.GetContext(), resourceGroup, appName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get app service: %v", err),
			}, err
		}

		app := appResp.Site
		if app.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "app service properties are nil",
			}, fmt.Errorf("app service properties are nil")
		}

		httpsOnly := true
		app.Properties.HTTPSOnly = &httpsOnly

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, appName, app, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update app service: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for app service update: %v", err),
			}, err
		}

		logger.Info("successfully enabled HTTPS only", "appName", appName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled HTTPS only for app service '%s'", appName),
		}, nil

	case "azure_app_service_client_cert_disabled":
		// Enable client certificates
		logger.Info("applying command: enabling client certificates", "appName", appName)

		appResp, err := client.Get(ctx.GetContext(), resourceGroup, appName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get app service: %v", err),
			}, err
		}

		app := appResp.Site
		if app.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "app service properties are nil",
			}, fmt.Errorf("app service properties are nil")
		}

		clientCertEnabled := true
		app.Properties.ClientCertEnabled = &clientCertEnabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, appName, app, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update app service: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for app service update: %v", err),
			}, err
		}

		logger.Info("successfully enabled client certificates", "appName", appName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled client certificates for app service '%s'", appName),
		}, nil

	case "start_app":
		// Start the app service
		logger.Info("applying command: starting app service", "appName", appName)
		_, err := client.Start(ctx.GetContext(), resourceGroup, appName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start app service: %v", err),
			}, err
		}
		logger.Info("successfully started app service", "appName", appName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started app service '%s'", appName),
		}, nil

	case "stop_app":
		// Stop the app service
		logger.Info("applying command: stopping app service", "appName", appName)
		_, err := client.Stop(ctx.GetContext(), resourceGroup, appName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop app service: %v", err),
			}, err
		}
		logger.Info("successfully stopped app service", "appName", appName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped app service '%s'", appName),
		}, nil

	case "restart_app":
		// Restart the app service
		logger.Info("applying command: restarting app service", "appName", appName)
		_, err := client.Restart(ctx.GetContext(), resourceGroup, appName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart app service: %v", err),
			}, err
		}
		logger.Info("successfully restarted app service", "appName", appName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted app service '%s'", appName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *appServiceService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *appServiceService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check HTTPS only
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if httpsOnly, ok := props["httpsOnly"].(bool); !ok || !httpsOnly {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_app_service_https_only_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "App Service should enforce HTTPS only"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check client certificate mode
			if clientCertEnabled, ok := props["clientCertEnabled"].(bool); !ok || !clientCertEnabled {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_app_service_client_cert_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Consider enabling client certificates"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for SKU rightsizing opportunities based on state
			if state, ok := props["state"].(string); ok {
				if strings.ToLower(state) == "stopped" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "azure_app_service_stopped_app",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      80.0, // Approximate cost of a typical S1 plan
						Data: map[string]any{
							"resource_id":     resource.Id,
							"resource_name":   resource.Name,
							"resource_type":   resource.Type,
							"resource_region": resource.Region,
							"service_name":    resource.ServiceName,
							"current_state":   state,
							"reason":          "App Service is in stopped state but still consuming resources. Consider deleting if no longer needed, or switching to a consumption-based plan for dev/test scenarios.",
							"benefits": []string{
								"Eliminate ongoing costs for unused resources",
								"Free up App Service Plan capacity",
								"Better resource utilization",
							},
							"estimated_savings_type": "monthly",
							"savings_source":         "Stopped apps on dedicated plans still incur charges",
							"recommendation":         "Delete if permanently unused, or use Free/Shared tier for occasional testing",
						},
						Action:              providers.RecommendationActionDelete,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}
	}
	return allRecommendations, nil
}

// getAppServicePlanOptimization returns App Service Plan SKU optimization recommendations
func (s *appServiceService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "appservice",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *appServiceService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	client, err := armmonitor.NewDiagnosticSettingsClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to create diagnostic settings client: %w", err)
	}

	pager := client.NewListPager(resourceId, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			return "", fmt.Errorf("failed to get next page of diagnostic settings: %w", err)
		}

		for _, setting := range page.Value {
			if setting.Properties != nil && setting.Properties.WorkspaceID != nil && *setting.Properties.WorkspaceID != "" {
				return *setting.Properties.WorkspaceID, nil
			}
		}
	}

	return "", errors.New("log analytics workspace not found for resource")
}
