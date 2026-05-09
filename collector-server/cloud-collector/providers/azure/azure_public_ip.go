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

type publicIpService struct {
}

func (s *publicIpService) Name() string {
	return "microsoft.network/publicipaddresses"
}

// Scope returns the service scope - this is a regional service
func (s *publicIpService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *publicIpService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewPublicIPAddressesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create public ip client: %w", err)
		}

		pager := client.NewListAllPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, pip := range page.Value {
				if pip == nil {
					continue
				}
				status := providers.ResourceStatusActive
				// If a PIP is not associated with any IPConfiguration, it's not being used.
				if pip.Properties == nil || pip.Properties.IPConfiguration == nil {
					status = providers.ResourceStatusInactive
				}

				allResources = append(allResources, providers.Resource{
					Id:          *pip.ID,
					Name:        *pip.Name,
					Type:        *pip.Type,
					Region:      *pip.Location,
					Tags:        toAzureTags(pip.Tags),
					Meta:        structToMap(pip),
					Status:      status,
					CreatedAt:   time.Time{}, // Public IPs don't have a creation timestamp
					Arn:         *pip.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *publicIpService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *publicIpService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "azure_unassociated_public_ip",
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
	}
	return recommendations, nil
}

func (s *publicIpService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *publicIpService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and public IP name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, publicIPName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "publicIPAddresses" && i+1 < len(parts) {
			publicIPName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || publicIPName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or public IP name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewPublicIPAddressesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create public IP client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_unassociated_public_ip":
		// Delete the unassociated public IP
		logger.Info("applying command: deleting unassociated public IP", "publicIPName", publicIPName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, publicIPName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete public IP: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for public IP deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted public IP", "publicIPName", publicIPName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted unassociated public IP '%s'", publicIPName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *publicIpService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *publicIpService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
		if ipConfig, ok := properties["ipConfiguration"].(map[string]interface{}); ok {
			if id, ok := ipConfig["id"].(string); ok {
				// We can't know the type of the resource from the ID, but we can make a guess
				// based on the provider. We'll assume it's a network interface for now.
				app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
					Id: providers.ServiceApplicationId{
						Name:      id,
						Kind:      "Microsoft.Network/networkInterfaces", // This is a guess
						Namespace: resource.Region,
					},
				}.ToUpstreamLink())
			}
		}
	}
	return app, nil
}
