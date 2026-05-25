package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type networkInterfaceService struct {
}

func (s *networkInterfaceService) Name() string {
	return "microsoft.network/networkinterfaces"
}

func (s *networkInterfaceService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *networkInterfaceService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewInterfacesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create network interface client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, nic := range page.Value {
				status := providers.ResourceStatusUnknown
				if nic.Properties != nil && nic.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*nic.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				// Mark NIC as inactive if not attached to any VM
				if nic.Properties != nil && nic.Properties.VirtualMachine == nil {
					status = providers.ResourceStatusInactive
				}

				allResources = append(allResources, providers.Resource{
					Id:          *nic.ID,
					Name:        *nic.Name,
					Type:        *nic.Type,
					Region:      *nic.Location,
					Tags:        toAzureTags(nic.Tags),
					Meta:        structToMap(nic),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *nic.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *networkInterfaceService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *networkInterfaceService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		// Check for orphaned NICs (not attached to any VM)
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "azure_nic_orphaned",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_type":   resource.Type,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"reason":          "Network Interface is not attached to any virtual machine. Orphaned NICs should be reviewed and deleted if no longer needed.",
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for NICs with public IPs
		if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if ipConfigs, ok := props["ipConfigurations"].([]interface{}); ok {
				for _, ipConfig := range ipConfigs {
					if configMap, ok := ipConfig.(map[string]interface{}); ok {
						if configProps, ok := configMap["properties"].(map[string]interface{}); ok {
							if _, hasPublicIP := configProps["publicIPAddress"]; hasPublicIP {
								recommendations = append(recommendations, providers.Recommendation{
									CategoryName: providers.RecommendationCategorySecurity,
									RuleName:     "azure_nic_public_ip_attached",
									Severity:     providers.RecommendationSeverityMedium,
									Savings:      0,
									Data: map[string]any{
										"resource_id":     resource.Id,
										"resource_name":   resource.Name,
										"resource_type":   resource.Type,
										"resource_region": resource.Region,
										"service_name":    resource.ServiceName,
										"reason":          "Network Interface has a public IP address attached. Review if direct public access is necessary. Consider using Azure Bastion or a load balancer instead for improved security.",
									},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								})
								break
							}
						}
					}
				}
			}
		}
	}
	return recommendations, nil
}

func (s *networkInterfaceService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *networkInterfaceService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, nicName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "networkInterfaces" && i+1 < len(parts) {
			nicName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || nicName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or NIC name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewInterfacesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create NIC client: %v", err),
		}, err
	}

	switch command.Command {
	case "azure_nic_orphaned":
		logger.Info("applying command: deleting orphaned NIC", "nicName", nicName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, nicName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete NIC: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for NIC deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted NIC", "nicName", nicName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted orphaned NIC '%s'", nicName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *networkInterfaceService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *networkInterfaceService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
			providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{
					Name:      rg,
					Kind:      "Microsoft.Resources/resourceGroups",
					Namespace: resource.Region,
				},
			}.ToUpstreamLink(),
		},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if vm, ok := props["virtualMachine"].(map[string]interface{}); ok {
			if id, ok := vm["id"].(string); ok {
				app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
					Id: providers.ServiceApplicationId{
						Name:      id,
						Kind:      "Microsoft.Compute/virtualMachines",
						Namespace: resource.Region,
					},
				}.ToUpstreamLink())
			}
		}
	}
	return app, nil
}
