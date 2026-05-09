package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type networkSecurityGroupService struct {
}

func (s *networkSecurityGroupService) Name() string {
	return "microsoft.network/networksecuritygroups"
}

func (s *networkSecurityGroupService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *networkSecurityGroupService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewSecurityGroupsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create network security group client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, nsg := range page.Value {
				status := providers.ResourceStatusUnknown
				if nsg.Properties != nil && nsg.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*nsg.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *nsg.ID,
					Name:        *nsg.Name,
					Type:        *nsg.Type,
					Region:      *nsg.Location,
					Tags:        toAzureTags(nsg.Tags),
					Meta:        structToMap(nsg),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *nsg.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *networkSecurityGroupService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *networkSecurityGroupService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check for default allow-all inbound rules (overly permissive)
			if securityRules, ok := props["securityRules"].([]interface{}); ok {
				hasOverlyPermissiveRule := false
				for _, rule := range securityRules {
					if ruleMap, ok := rule.(map[string]interface{}); ok {
						if ruleProps, ok := ruleMap["properties"].(map[string]interface{}); ok {
							srcPrefix, _ := ruleProps["sourceAddressPrefix"].(string)
							destPortRange, _ := ruleProps["destinationPortRange"].(string)
							access, _ := ruleProps["access"].(string)
							direction, _ := ruleProps["direction"].(string)

							if direction == "Inbound" && access == "Allow" && srcPrefix == "*" {
								// Check for sensitive ports open to the world
								if destPortRange == "*" || destPortRange == "22" || destPortRange == "3389" || destPortRange == "3306" || destPortRange == "1433" || destPortRange == "5432" {
									hasOverlyPermissiveRule = true
									break
								}
							}
						}
					}
				}
				if hasOverlyPermissiveRule {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_nsg_overly_permissive_inbound",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"resource_id":     resource.Id,
							"resource_name":   resource.Name,
							"resource_type":   resource.Type,
							"resource_region": resource.Region,
							"service_name":    resource.ServiceName,
							"reason":          "Network Security Group has overly permissive inbound rules allowing traffic from any source (0.0.0.0/0) to sensitive ports (SSH/22, RDP/3389, SQL/3306/1433/5432, or all ports). Restrict source addresses to specific IP ranges.",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}

				// Check for NSG with no custom rules (only default rules)
				if len(securityRules) == 0 {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_nsg_no_custom_rules",
						Severity:     providers.RecommendationSeverityLow,
						Savings:      0,
						Data: map[string]any{
							"resource_id":     resource.Id,
							"resource_name":   resource.Name,
							"resource_type":   resource.Type,
							"resource_region": resource.Region,
							"service_name":    resource.ServiceName,
							"reason":          "Network Security Group has no custom security rules configured. Only Azure default rules are in effect. Consider adding explicit rules to control traffic flow.",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if NSG is not associated with any subnet or NIC
			subnets, _ := props["subnets"].([]interface{})
			nics, _ := props["networkInterfaces"].([]interface{})
			if len(subnets) == 0 && len(nics) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "azure_nsg_unassociated",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"resource_id":     resource.Id,
						"resource_name":   resource.Name,
						"resource_type":   resource.Type,
						"resource_region": resource.Region,
						"service_name":    resource.ServiceName,
						"reason":          "Network Security Group is not associated with any subnet or network interface. Unassociated NSGs provide no security benefit and should be reviewed for cleanup.",
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
	return recommendations, nil
}

func (s *networkSecurityGroupService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *networkSecurityGroupService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, nsgName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "networkSecurityGroups" && i+1 < len(parts) {
			nsgName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || nsgName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or NSG name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewSecurityGroupsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create NSG client: %v", err),
		}, err
	}

	switch command.Command {
	case "azure_nsg_unassociated":
		logger.Info("applying command: deleting unassociated NSG", "nsgName", nsgName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, nsgName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete NSG: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for NSG deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted NSG", "nsgName", nsgName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted unassociated NSG '%s'", nsgName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *networkSecurityGroupService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *networkSecurityGroupService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "networksecuritygroup",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		// Link to associated subnets
		if subnets, ok := props["subnets"].([]interface{}); ok {
			for _, subnet := range subnets {
				if subnetMap, ok := subnet.(map[string]interface{}); ok {
					if id, ok := subnetMap["id"].(string); ok {
						app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
							Id: providers.ServiceApplicationId{
								Name:      id,
								Kind:      "Microsoft.Network/virtualNetworks/subnets",
								Namespace: resource.Region,
							},
						}.ToDownstreamLink())
					}
				}
			}
		}
		// Link to associated NICs
		if nics, ok := props["networkInterfaces"].([]interface{}); ok {
			for _, nic := range nics {
				if nicMap, ok := nic.(map[string]interface{}); ok {
					if id, ok := nicMap["id"].(string); ok {
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
	return app, nil
}
