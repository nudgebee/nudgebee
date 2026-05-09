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

type loadBalancersService struct {
}

func (s *loadBalancersService) Name() string {
	return "microsoft.network/loadbalancers"
}

// Scope returns the service scope - this is a regional service
func (s *loadBalancersService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *loadBalancersService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewLoadBalancersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create load balancers client: %w", err)
		}

		pager := client.NewListAllPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, lb := range page.Value {
				status := providers.ResourceStatusUnknown
				if lb.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*lb.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *lb.ID,
					Name:        *lb.Name,
					Type:        *lb.Type,
					Region:      *lb.Location,
					Tags:        toAzureTags(lb.Tags),
					Meta:        structToMap(lb),
					Status:      status,
					CreatedAt:   time.Time{}, // Load Balancers don't have a creation timestamp
					Arn:         *lb.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *loadBalancersService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *loadBalancersService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if backendPools, ok := properties["backendAddressPools"].([]interface{}); ok && len(backendPools) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "azure_unused_load_balancer",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0, // TODO: Calculate savings
					Data:                map[string]any{},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			if probes, ok := properties["probes"].([]interface{}); ok && len(probes) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_load_balancer_no_health_probes",
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

			if outboundRules, ok := properties["outboundRules"].([]interface{}); ok && len(outboundRules) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_load_balancer_no_outbound_rules",
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
		if sku, ok := resource.Meta["sku"].(map[string]interface{}); ok {
			if name, ok := sku["name"].(string); ok && name != "Standard" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryInfraUpgrade,
					RuleName:            "azure_load_balancer_basic_sku",
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
	}
	return recommendations, nil
}

func (s *loadBalancersService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *loadBalancersService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and load balancer name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, lbName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "loadBalancers" && i+1 < len(parts) {
			lbName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || lbName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or load balancer name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewLoadBalancersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create load balancers client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_unused_load_balancer":
		// Delete the unused load balancer
		logger.Info("applying command: deleting unused load balancer", "lbName", lbName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, lbName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete load balancer: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for load balancer deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted load balancer", "lbName", lbName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted unused load balancer '%s'", lbName),
		}, nil

	case "azure_load_balancer_no_health_probes":
		// Cannot auto-fix - requires user to configure health probes
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: health probes must be configured manually with appropriate intervals and thresholds",
		}, fmt.Errorf("health probes require manual configuration")

	case "azure_load_balancer_no_outbound_rules":
		// Cannot auto-fix - requires user to configure outbound rules
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: outbound rules must be configured manually based on network requirements",
		}, fmt.Errorf("outbound rules require manual configuration")

	case "azure_load_balancer_basic_sku":
		// Cannot auto-fix - SKU upgrade requires recreation
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: upgrading from Basic to Standard SKU requires load balancer recreation",
		}, fmt.Errorf("SKU upgrade requires manual recreation")

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *loadBalancersService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	_, err = extractSubscriptionID(resourceId)
	if err != nil {
		return "", fmt.Errorf("failed to extract subscription id from resource id: %w", err)
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

	return "", errors.New("log group name not found")
}

func (s *loadBalancersService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams: []providers.UpstreamLink{
			{
				Id: providers.ServiceApplicationId{
					Name:      rg,
					Kind:      "Microsoft.Resources/resourceGroups",
					Namespace: resource.Region,
				}.Key(),
			},
		},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if frontendConfigs, ok := properties["frontendIPConfigurations"].([]interface{}); ok {
			for _, fc := range frontendConfigs {
				if fcMap, ok := fc.(map[string]interface{}); ok {
					if props, ok := fcMap["properties"].(map[string]interface{}); ok {
						if publicIp, ok := props["publicIPAddress"].(map[string]interface{}); ok {
							if id, ok := publicIp["id"].(string); ok {
								app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
									Id: providers.ServiceApplicationId{
										Name:      id,
										Kind:      "Microsoft.Network/publicIPAddresses",
										Namespace: resource.Region,
									},
								}.ToUpstreamLink())
							}
						}
					}
				}
			}
		}
		if backendPools, ok := properties["backendAddressPools"].([]interface{}); ok {
			for _, bp := range backendPools {
				if bpMap, ok := bp.(map[string]interface{}); ok {
					if props, ok := bpMap["properties"].(map[string]interface{}); ok {
						if backendIps, ok := props["backendIPConfigurations"].([]interface{}); ok {
							for _, bip := range backendIps {
								if bipMap, ok := bip.(map[string]interface{}); ok {
									if id, ok := bipMap["id"].(string); ok {
										// This ID points to a network interface, which is part of a VM
										// We can't get the VM directly, but we can link to the NIC
										app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
											Id: providers.ServiceApplicationId{
												Name:      id,
												Kind:      "Microsoft.Network/networkInterfaces",
												Namespace: resource.Region,
											},
										}.ToDownstreamLink())
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return app, nil
}
