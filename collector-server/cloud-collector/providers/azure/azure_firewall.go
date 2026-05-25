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

type firewallService struct {
}

func (s *firewallService) Name() string {
	return "Microsoft.Network/azureFirewalls"
}

// Scope returns the service scope - this is a regional service
func (s *firewallService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *firewallService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewAzureFirewallsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create firewall client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, firewall := range page.Value {
				status := providers.ResourceStatusUnknown
				if firewall.Properties != nil && firewall.Properties.ProvisioningState != nil {
					state := strings.ToLower(string(*firewall.Properties.ProvisioningState))
					switch state {
					case "succeeded":
						status = providers.ResourceStatusActive
					case "failed":
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *firewall.ID,
					Name:        *firewall.Name,
					Type:        *firewall.Type,
					Region:      *firewall.Location,
					Tags:        toAzureTags(firewall.Tags),
					Meta:        structToMap(firewall),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *firewall.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *firewallService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *firewallService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and firewall name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, firewallName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "azureFirewalls" && i+1 < len(parts) {
			firewallName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || firewallName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or firewall name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewAzureFirewallsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create firewall client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_firewall_enable_threat_intel":
		// Enable threat intelligence mode
		logger.Info("applying command: enabling threat intelligence", "firewallName", firewallName)

		firewallResp, err := client.Get(ctx.GetContext(), resourceGroup, firewallName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get firewall: %v", err),
			}, err
		}

		firewall := firewallResp.AzureFirewall
		if firewall.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "firewall properties are nil",
			}, fmt.Errorf("firewall properties are nil")
		}

		threatIntelMode := armnetwork.AzureFirewallThreatIntelModeAlert
		firewall.Properties.ThreatIntelMode = &threatIntelMode

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, firewallName, firewall, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update firewall: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for firewall update: %v", err),
			}, err
		}

		logger.Info("successfully enabled threat intelligence", "firewallName", firewallName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled threat intelligence for firewall '%s'", firewallName),
		}, nil

	case "azure_firewall_enable_dns_proxy":
		// Enable DNS proxy
		logger.Info("applying command: enabling DNS proxy", "firewallName", firewallName)

		firewallResp, err := client.Get(ctx.GetContext(), resourceGroup, firewallName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get firewall: %v", err),
			}, err
		}

		firewall := firewallResp.AzureFirewall
		if firewall.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "firewall properties are nil",
			}, fmt.Errorf("firewall properties are nil")
		}

		dnsProxyEnabled := true
		if firewall.Properties.AdditionalProperties == nil {
			firewall.Properties.AdditionalProperties = map[string]*string{}
		}
		firewall.Properties.AdditionalProperties["Network.DNS.EnableProxy"] = &[]string{"true"}[0]
		_ = dnsProxyEnabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, firewallName, firewall, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update firewall: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for firewall update: %v", err),
			}, err
		}

		logger.Info("successfully enabled DNS proxy", "firewallName", firewallName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled DNS proxy for firewall '%s'", firewallName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *firewallService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *firewallService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check threat intelligence mode
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if threatIntelMode, ok := props["threatIntelMode"].(string); !ok || strings.ToLower(threatIntelMode) == "off" {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_firewall_enable_threat_intel",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Azure Firewall should have threat intelligence mode enabled to detect and block traffic from known malicious IP addresses and domains"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check DNS proxy
			if additionalProps, ok := props["additionalProperties"].(map[string]interface{}); ok {
				if dnsProxy, ok := additionalProps["Network.DNS.EnableProxy"].(string); !ok || strings.ToLower(dnsProxy) != "true" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_firewall_enable_dns_proxy",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Consider enabling DNS proxy for Azure Firewall to enhance security, enable FQDN filtering, and provide better DNS-based threat protection"},
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
	return allRecommendations, nil
}

func (s *firewallService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "firewall",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *firewallService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
