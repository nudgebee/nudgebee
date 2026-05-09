package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type ddosProtectionService struct {
}

func (s *ddosProtectionService) Name() string {
	return "microsoft.network/ddosprotectionplans"
}

// Scope returns the service scope - this is a regional service
func (s *ddosProtectionService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *ddosProtectionService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get DDoS Protection Plans
		ddosClient, err := armnetwork.NewDdosProtectionPlansClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create ddos protection plans client: %w", err)
		}

		pager := ddosClient.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, plan := range page.Value {
				if plan.ID == nil || plan.Name == nil || plan.Type == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if plan.Properties != nil && plan.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*plan.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *plan.ID,
					Name:        *plan.Name,
					Type:        *plan.Type,
					Region:      normalizeAzureRegion(*plan.Location),
					Tags:        toAzureTags(plan.Tags),
					Meta:        structToMap(plan.Properties),
					Status:      status,
					Arn:         *plan.ID,
					ServiceName: s.Name(),
				})
			}
		}

	}

	return allResources, nil
}

func (s *ddosProtectionService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *ddosProtectionService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check if DDoS Protection Plan has no associated VNets
		if resourceType := resource.Type; strings.Contains(strings.ToLower(resourceType), "ddosprotectionplan") {
			if virtualNetworks, ok := properties["virtualNetworks"].([]interface{}); !ok || len(virtualNetworks) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_ddos_protection_plan_no_vnets",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason":      "DDoS Protection Plan has no associated Virtual Networks",
						"meta":        properties,
						"tags":        resource.Tags,
						"status":      resource.Status,
						"region":      resource.Region,
						"type":        resource.Type,
						"name":        resource.Name,
						"id":          resource.Id,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
						"serviceName": resource.ServiceName,
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

func (s *ddosProtectionService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *ddosProtectionService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	_, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	switch command.Command {
	case "azure_ddos_protection_plan_no_vnets":
		// These require manual configuration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("cannot auto-apply command: %s requires manual configuration and has cost implications", command.Command),
		}, errors.ErrUnsupported

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *ddosProtectionService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *ddosProtectionService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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

	return app, nil
}
