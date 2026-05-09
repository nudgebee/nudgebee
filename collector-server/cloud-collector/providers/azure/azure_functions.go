package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type functionsService struct {
}

func (s *functionsService) Name() string {
	return "Microsoft.Web/sites/functions"
}

// Scope returns the service scope - this is a regional service
func (s *functionsService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *functionsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// First, get all Web Apps (Function Apps are a type of Web App)
		webAppsClient, err := armappservice.NewWebAppsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create web apps client: %w", err)
		}

		pager := webAppsClient.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, app := range page.Value {
				// Check if this is a Function App by looking at the kind property
				if app.Kind == nil || !strings.Contains(strings.ToLower(*app.Kind), "functionapp") {
					continue
				}

				status := providers.ResourceStatusUnknown
				if app.Properties != nil && app.Properties.State != nil {
					switch strings.ToLower(*app.Properties.State) {
					case "running":
						status = providers.ResourceStatusActive
					case "stopped":
						status = providers.ResourceStatusInactive
					}
				}

				createdAt := getCreatedAtFromTags(app.Tags)

				resourceID := *app.ID
				// Construct the function app resource representation
				allResources = append(allResources, providers.Resource{
					Id:          resourceID,
					Name:        *app.Name,
					Type:        s.Name(),
					Region:      normalizeAzureRegion(*app.Location),
					Tags:        toAzureTags(app.Tags),
					Meta:        structToMap(app),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         resourceID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *functionsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *functionsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and function app name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, functionAppName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "sites" && i+1 < len(parts) {
			functionAppName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || functionAppName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or function app name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armappservice.NewWebAppsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create web apps client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_function_https_only_disabled":
		// Enable HTTPS only
		logger.Info("applying command: enabling HTTPS only", "functionApp", functionAppName)

		appResp, err := client.Get(ctx.GetContext(), resourceGroup, functionAppName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get function app: %v", err),
			}, err
		}

		app := appResp.Site
		if app.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "function app properties are nil",
			}, fmt.Errorf("function app properties are nil")
		}

		app.Properties.HTTPSOnly = to.Ptr(true)

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, functionAppName, app, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update function app: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for function app update: %v", err),
			}, err
		}

		logger.Info("successfully enabled HTTPS only", "functionApp", functionAppName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled HTTPS only for function app '%s'", functionAppName),
		}, nil

	case "azure_function_enable_authentication":
		// Enable authentication/authorization
		logger.Info("applying command: enabling authentication", "functionApp", functionAppName)

		appResp, err := client.Get(ctx.GetContext(), resourceGroup, functionAppName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get function app: %v", err),
			}, err
		}

		app := appResp.Site
		if app.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "function app properties are nil",
			}, fmt.Errorf("function app properties are nil")
		}

		// Note: Full auth configuration requires the AuthSettings API
		// This is a simplified version that marks the intent
		logger.Info("authentication configuration requires AuthSettings API", "functionApp", functionAppName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("please configure authentication for function app '%s' via Azure Portal or AuthSettings API", functionAppName),
		}, nil

	case "start_function":
		// Start the function app
		logger.Info("applying command: starting function app", "functionApp", functionAppName)
		_, err := client.Start(ctx.GetContext(), resourceGroup, functionAppName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start function app: %v", err),
			}, err
		}
		logger.Info("successfully started function app", "functionApp", functionAppName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started function app '%s'", functionAppName),
		}, nil

	case "stop_function":
		// Stop the function app
		logger.Info("applying command: stopping function app", "functionApp", functionAppName)
		_, err := client.Stop(ctx.GetContext(), resourceGroup, functionAppName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop function app: %v", err),
			}, err
		}
		logger.Info("successfully stopped function app", "functionApp", functionAppName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped function app '%s'", functionAppName),
		}, nil

	case "restart_function":
		// Restart the function app
		logger.Info("applying command: restarting function app", "functionApp", functionAppName)
		_, err := client.Restart(ctx.GetContext(), resourceGroup, functionAppName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart function app: %v", err),
			}, err
		}
		logger.Info("successfully restarted function app", "functionApp", functionAppName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted function app '%s'", functionAppName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *functionsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *functionsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check HTTPS only
			if httpsOnly, ok := props["httpsOnly"].(bool); !ok || !httpsOnly {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_function_https_only_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Function App should enforce HTTPS-only traffic to ensure secure communication and protect data in transit"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if authentication is enabled
			// Note: This is a simplified check; real implementation would need AuthSettings API
			siteAuthEnabled := false
			if siteAuthEnabledProp, ok := props["siteAuthEnabled"].(bool); ok {
				siteAuthEnabled = siteAuthEnabledProp
			}
			if !siteAuthEnabled {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_function_authentication_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Consider enabling authentication for Function App to control access and prevent unauthorized use"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for old runtime version
			if siteConfigProp, ok := props["siteConfig"].(map[string]interface{}); ok {
				if netFrameworkVersion, ok := siteConfigProp["netFrameworkVersion"].(string); ok {
					if strings.Contains(netFrameworkVersion, "v4.0") {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryInfraUpgrade,
							RuleName:            "azure_function_old_runtime",
							Severity:            providers.RecommendationSeverityLow,
							Savings:             0,
							Data:                map[string]any{"reason": "Function App is using an older runtime version; consider upgrading to the latest version for improved performance, security, and features"},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}
		}
	}
	return allRecommendations, nil
}

func (s *functionsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "azure-function",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *functionsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
