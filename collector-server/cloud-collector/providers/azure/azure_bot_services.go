package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type botServicesService struct {
}

func (s *botServicesService) Name() string {
	return "microsoft.botservice/botservices"
}

// Scope returns the service scope - this is a regional service
func (s *botServicesService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *botServicesService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armbotservice.NewBotsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create bot services client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, bot := range page.Value {
				status := providers.ResourceStatusUnknown
				if bot.Properties != nil && bot.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[*bot.Properties.ProvisioningState]; ok {
						status = val
					}
				}

				createdAt := getCreatedAtFromTags(bot.Tags)

				allResources = append(allResources, providers.Resource{
					Id:          *bot.ID,
					Name:        *bot.Name,
					Type:        *bot.Type,
					Region:      *bot.Location,
					Tags:        toAzureTags(bot.Tags),
					Meta:        structToMap(bot),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *bot.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *botServicesService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *botServicesService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check for public network access
			if publicAccess, ok := props["publicNetworkAccess"].(string); !ok || publicAccess != string(armbotservice.PublicNetworkAccessDisabled) {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_bot_service_public_network_access_enabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{"reason": "Bot Service should disable public network access.",
						"botService":          resource,
						"publicNetworkAccess": publicAccess,
						"properties":          props,
						"meta":                meta,
						"tags":                resource.Tags,
						"status":              resource.Status,
						"region":              resource.Region,
						"type":                resource.Type,
						"name":                resource.Name,
						"id":                  resource.Id,
						"arn":                 resource.Arn,
						"createdAt":           resource.CreatedAt,
						"serviceName":         resource.ServiceName,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for managed identity
		if identity, ok := meta["identity"].(map[string]interface{}); !ok || identity == nil || identity["type"] == "None" {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_bot_service_managed_identity_disabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{"reason": "Bot Service should use a managed identity.",
					"botService":  resource,
					"identity":    identity,
					"meta":        meta,
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
	return allRecommendations, nil
}

func (s *botServicesService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *botServicesService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and bot name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, botName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "botServices" && i+1 < len(parts) {
			botName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || botName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or bot name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armbotservice.NewBotsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create bot services client: %v", err),
		}, err
	}

	botResp, err := client.Get(ctx.GetContext(), resourceGroup, botName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to get bot service: %v", err)}, err
	}
	bot := botResp.Bot

	switch command.Command {
	case "azure_bot_service_public_network_access_enabled":
		logger.Info("applying command: disabling public network access", "botName", botName)

		updateParams := armbotservice.Bot{
			Location: bot.Location,
			Tags:     bot.Tags,
			SKU:      bot.SKU,
			Kind:     bot.Kind,
			Etag:     bot.Etag,
			Zones:    bot.Zones,
		}

		if bot.Properties != nil {
			updateParams.Properties = bot.Properties
		} else {
			updateParams.Properties = &armbotservice.BotProperties{}
		}

		disabled := armbotservice.PublicNetworkAccessDisabled
		updateParams.Properties.PublicNetworkAccess = &disabled

		_, err = client.Update(ctx.GetContext(), resourceGroup, botName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to update bot service: %v", err)}, err
		}

	case "azure_bot_service_managed_identity_disabled":
		logger.Info("applying command: enabling system-assigned managed identity", "botName", botName)

		// For Bot Service, we need to use CreateOrUpdate with full parameters to set identity
		updateParams := armbotservice.Bot{
			Location:   bot.Location,
			Tags:       bot.Tags,
			SKU:        bot.SKU,
			Kind:       bot.Kind,
			Properties: bot.Properties,
		}

		_, err = client.Update(ctx.GetContext(), resourceGroup, botName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to update bot service identity: %v", err)}, err
		}

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	logger.Info("successfully applied command", "command", command.Command, "botName", botName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on bot service '%s'", command.Command, botName),
	}, nil
}

func (s *botServicesService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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
	return "", errors.New("log group name not found")
}

func (s *botServicesService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "botservice",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      string(resource.Status),
	}
	return app, nil
}
