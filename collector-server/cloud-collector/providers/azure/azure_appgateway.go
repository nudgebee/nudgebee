package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type appGatewayService struct {
}

func (s *appGatewayService) Name() string {
	return "Microsoft.Network/applicationGateways"
}

// Scope returns the service scope - this is a regional service
func (s *appGatewayService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *appGatewayService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewApplicationGatewaysClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create application gateway client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, gateway := range page.Value {
				status := providers.ResourceStatusUnknown
				if gateway.Properties != nil && gateway.Properties.ProvisioningState != nil {
					switch *gateway.Properties.ProvisioningState {
					case armnetwork.ProvisioningStateSucceeded:
						// Check operational state
						if gateway.Properties.OperationalState != nil {
							switch *gateway.Properties.OperationalState {
							case armnetwork.ApplicationGatewayOperationalStateRunning:
								status = providers.ResourceStatusActive
							case armnetwork.ApplicationGatewayOperationalStateStopped:
								status = providers.ResourceStatusInactive
							case armnetwork.ApplicationGatewayOperationalStateStopping, armnetwork.ApplicationGatewayOperationalStateStarting:
								status = providers.ResourceStatusUnknown
							}
						} else {
							status = providers.ResourceStatusActive
						}
					case armnetwork.ProvisioningStateFailed:
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *gateway.ID,
					Name:        *gateway.Name,
					Type:        *gateway.Type,
					Region:      *gateway.Location,
					Tags:        toAzureTags(gateway.Tags),
					Meta:        structToMap(gateway),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *gateway.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *appGatewayService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *appGatewayService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and gateway name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, gatewayName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "applicationGateways" && i+1 < len(parts) {
			gatewayName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || gatewayName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or gateway name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewApplicationGatewaysClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create application gateway client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_appgateway_waf_disabled":
		// Enable WAF
		logger.Info("applying command: enabling WAF", "gatewayName", gatewayName)

		gatewayResp, err := client.Get(ctx.GetContext(), resourceGroup, gatewayName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get application gateway: %v", err),
			}, err
		}

		gateway := gatewayResp.ApplicationGateway
		if gateway.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "application gateway properties are nil",
			}, fmt.Errorf("application gateway properties are nil")
		}

		// Enable WAF
		if gateway.Properties.WebApplicationFirewallConfiguration == nil {
			gateway.Properties.WebApplicationFirewallConfiguration = &armnetwork.ApplicationGatewayWebApplicationFirewallConfiguration{}
		}
		enabled := true
		gateway.Properties.WebApplicationFirewallConfiguration.Enabled = &enabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, gatewayName, gateway, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update application gateway: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for application gateway update: %v", err),
			}, err
		}

		logger.Info("successfully enabled WAF", "gatewayName", gatewayName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled WAF for application gateway '%s'", gatewayName),
		}, nil

	case "azure_appgateway_http2_disabled":
		// Enable HTTP/2
		logger.Info("applying command: enabling HTTP/2", "gatewayName", gatewayName)

		gatewayResp, err := client.Get(ctx.GetContext(), resourceGroup, gatewayName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get application gateway: %v", err),
			}, err
		}

		gateway := gatewayResp.ApplicationGateway
		if gateway.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "application gateway properties are nil",
			}, fmt.Errorf("application gateway properties are nil")
		}

		// Enable HTTP/2
		enabled := true
		gateway.Properties.EnableHTTP2 = &enabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, gatewayName, gateway, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update application gateway: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for application gateway update: %v", err),
			}, err
		}

		logger.Info("successfully enabled HTTP/2", "gatewayName", gatewayName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled HTTP/2 for application gateway '%s'", gatewayName),
		}, nil

	case "start_gateway":
		// Start the application gateway
		logger.Info("applying command: starting application gateway", "gatewayName", gatewayName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, gatewayName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start application gateway: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for application gateway start: %v", err),
			}, err
		}
		logger.Info("successfully started application gateway", "gatewayName", gatewayName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started application gateway '%s'", gatewayName),
		}, nil

	case "stop_gateway":
		// Stop the application gateway
		logger.Info("applying command: stopping application gateway", "gatewayName", gatewayName)
		poller, err := client.BeginStop(ctx.GetContext(), resourceGroup, gatewayName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop application gateway: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for application gateway stop: %v", err),
			}, err
		}
		logger.Info("successfully stopped application gateway", "gatewayName", gatewayName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped application gateway '%s'", gatewayName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *appGatewayService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *appGatewayService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check WAF configuration
			if wafConfig, ok := props["webApplicationFirewallConfiguration"].(map[string]interface{}); ok {
				if enabled, ok := wafConfig["enabled"].(bool); !ok || !enabled {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_appgateway_waf_disabled",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "Application Gateway should have Web Application Firewall (WAF) enabled"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			} else {
				// WAF configuration not present - recommend enabling it
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_appgateway_waf_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Application Gateway should have Web Application Firewall (WAF) enabled"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check HTTP/2 support
			if enableHTTP2, ok := props["enableHttp2"].(bool); !ok || !enableHTTP2 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_appgateway_http2_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Consider enabling HTTP/2 for better performance"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if gateway is stopped for cost optimization
			if operationalState, ok := props["operationalState"].(string); ok && operationalState == string(armnetwork.ApplicationGatewayOperationalStateStopped) {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "azure_appgateway_stopped",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"reason": "Application Gateway is stopped, consider deleting if not needed"},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}
	return allRecommendations, nil
}

func (s *appGatewayService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "applicationgateway",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *appGatewayService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
