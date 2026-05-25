package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/frontdoor/armfrontdoor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type frontDoorService struct{}

func (s *frontDoorService) Name() string {
	return "Microsoft.Network/frontDoors"
}

// Scope returns the service scope - Front Door (Classic) is a global service
func (s *frontDoorService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *frontDoorService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armfrontdoor.NewFrontDoorsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create front door client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, frontDoor := range page.Value {
				status := providers.ResourceStatusUnknown
				if frontDoor.Properties != nil && frontDoor.Properties.ResourceState != nil {
					state := strings.ToLower(string(*frontDoor.Properties.ResourceState))
					switch state {
					case "enabled":
						status = providers.ResourceStatusActive
					case "disabled":
						status = providers.ResourceStatusInactive
					}
				}

				location := "global"
				if frontDoor.Location != nil {
					location = *frontDoor.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *frontDoor.ID,
					Name:        *frontDoor.Name,
					Type:        *frontDoor.Type,
					Region:      location,
					Tags:        toAzureTags(frontDoor.Tags),
					Meta:        structToMap(frontDoor),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *frontDoor.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *frontDoorService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *frontDoorService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and front door name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, frontDoorName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "frontDoors" && i+1 < len(parts) {
			frontDoorName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || frontDoorName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or front door name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armfrontdoor.NewFrontDoorsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create front door client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_frontdoor_enable_waf":
		// Enable Web Application Firewall
		logger.Info("applying command: enabling WAF", "frontDoorName", frontDoorName)

		frontDoorResp, err := client.Get(ctx.GetContext(), resourceGroup, frontDoorName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get front door: %v", err),
			}, err
		}

		frontDoor := frontDoorResp.FrontDoor
		if frontDoor.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "front door properties are nil",
			}, fmt.Errorf("front door properties are nil")
		}

		// Note: Actual WAF policy needs to be created separately and linked
		// This is a simplified example showing that WAF configuration is required
		logger.Info("WAF policy needs to be created and linked separately", "frontDoorName", frontDoorName)

		return providers.ApplyCommandResponse{
			Success: false,
			Message: "WAF policy needs to be created separately using Azure Portal or CLI and linked to Front Door",
		}, errors.New("WAF policy configuration required")

	case "azure_frontdoor_enable_https_redirect":
		// Enable HTTPS redirect
		logger.Info("applying command: enabling HTTPS redirect", "frontDoorName", frontDoorName)

		frontDoorResp, err := client.Get(ctx.GetContext(), resourceGroup, frontDoorName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get front door: %v", err),
			}, err
		}

		frontDoor := frontDoorResp.FrontDoor
		if frontDoor.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "front door properties are nil",
			}, fmt.Errorf("front door properties are nil")
		}

		// Update routing rules to redirect HTTP to HTTPS
		var modified bool
		if frontDoor.Properties.RoutingRules != nil {
			for _, rule := range frontDoor.Properties.RoutingRules {
				if rule.Properties == nil {
					continue
				}

				// Check if the rule accepts HTTP and is a forwarding rule
				isHTTPForwarding := false
				for _, protocol := range rule.Properties.AcceptedProtocols {
					if *protocol == armfrontdoor.FrontDoorProtocolHTTP {
						if _, ok := rule.Properties.RouteConfiguration.(*armfrontdoor.ForwardingConfiguration); ok {
							isHTTPForwarding = true
							break
						}
					}
				}

				if isHTTPForwarding {
					logger.Info("updating routing rule to enforce HTTPS redirect", "ruleName", *rule.Name)
					rule.Properties.RouteConfiguration = &armfrontdoor.RedirectConfiguration{
						ODataType:        to.Ptr("#Microsoft.Azure.FrontDoor.Models.FrontdoorRedirectConfiguration"),
						RedirectType:     to.Ptr(armfrontdoor.FrontDoorRedirectTypeMoved),
						RedirectProtocol: to.Ptr(armfrontdoor.FrontDoorRedirectProtocolHTTPSOnly),
					}
					modified = true
				}
			}
		}

		if !modified {
			return providers.ApplyCommandResponse{
				Success: true,
				Message: fmt.Sprintf("No HTTP forwarding rules found to update for Front Door '%s'. HTTPS redirect may already be in place.", frontDoorName),
			}, nil
		}

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, frontDoorName, frontDoor, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update front door: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for front door update: %v", err),
			}, err
		}

		logger.Info("successfully configured HTTPS redirect", "frontDoorName", frontDoorName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully configured HTTPS redirect for Front Door '%s'", frontDoorName),
		}, nil

	case "delete_frontdoor":
		// Delete Front Door
		logger.Info("applying command: deleting Front Door", "frontDoorName", frontDoorName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, frontDoorName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete front door: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for front door deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted Front Door", "frontDoorName", frontDoorName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted Front Door '%s'", frontDoorName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *frontDoorService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *frontDoorService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if WAF is enabled
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			hasWAF := false
			if webAppFirewallPolicyLink, ok := props["webApplicationFirewallPolicyLink"].(map[string]interface{}); ok {
				if id, ok := webAppFirewallPolicyLink["id"].(string); ok && id != "" {
					hasWAF = true
				}
			}

			if !hasWAF {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_frontdoor_enable_waf",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Front Door should have Web Application Firewall (WAF) enabled to protect against common web vulnerabilities and attacks such as SQL injection, cross-site scripting, and other OWASP top 10 threats"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check HTTPS redirect in routing rules
			isVulnerableToHTTP := false
			if routingRules, ok := props["routingRules"].([]interface{}); ok {
				for _, rule := range routingRules {
					if ruleMap, ok := rule.(map[string]interface{}); ok {
						if ruleProps, ok := ruleMap["properties"].(map[string]interface{}); ok {
							// Check if the rule accepts HTTP
							acceptsHTTP := false
							if acceptedProtocols, ok := ruleProps["acceptedProtocols"].([]interface{}); ok {
								for _, protocol := range acceptedProtocols {
									if protocolStr, ok := protocol.(string); ok && strings.EqualFold(protocolStr, "Http") {
										acceptsHTTP = true
										break
									}
								}
							}

							// If it accepts HTTP, check if it's a forwarding rule (which is a vulnerability)
							if acceptsHTTP {
								if routeConfig, ok := ruleProps["routeConfiguration"].(map[string]interface{}); ok {
									if odataType, ok := routeConfig["@odata.type"].(string); ok && strings.Contains(odataType, "ForwardingConfiguration") {
										isVulnerableToHTTP = true
										break
									}
								}
							}
						}
					}
				}
			}

			if isVulnerableToHTTP {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_frontdoor_enable_https_redirect",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Front Door routing rules accept HTTP traffic without redirecting to HTTPS; consider enabling HTTPS redirect to ensure all traffic is encrypted and secure"},
					Action:              providers.RecommendationActionModify,
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

func (s *frontDoorService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "frontdoor",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *frontDoorService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
