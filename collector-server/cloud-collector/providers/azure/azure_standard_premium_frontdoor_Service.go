package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type frontDoorCdnService struct {
}

func (s *frontDoorCdnService) Name() string {
	return "Microsoft.Cdn/profiles"
}

// Scope returns the service scope - Front Door Standard/Premium is a global service
func (s *frontDoorCdnService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *frontDoorCdnService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	subscriptionIDs := strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}

		client, err := armcdn.NewProfilesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create cdn profiles client: %w", err)
		}
		pager := client.NewListPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, profile := range page.Value {
				if profile.Kind == nil || *profile.Kind != "frontdoor" {
					continue
				}
				status := providers.ResourceStatusUnknown
				if profile.Properties != nil && profile.Properties.ResourceState != nil {
					switch *profile.Properties.ResourceState {
					case "enabled":
						status = providers.ResourceStatusActive
					case "disabled":
						status = providers.ResourceStatusInactive
					}
				}

				location := "global"
				if profile.Location != nil {
					location = *profile.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *profile.ID,
					Name:        *profile.Name,
					Type:        *profile.Type,
					Region:      location,
					Tags:        toAzureTags(profile.Tags),
					Meta:        structToMap(profile),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *profile.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *frontDoorCdnService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *frontDoorCdnService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, profileName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "profiles" && i+1 < len(parts) {
			profileName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || profileName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or profile name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armcdn.NewProfilesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to create cdn profiles client: %s", err)}, err
	}

	// Handle commands
	switch command.Command {
	case "azure_frontdoor_enable_waf":
		logger.Info("applying command: enabling WAF", "frontDoorName", profileName)

		profileResp, err := client.Get(ctx.GetContext(), resourceGroup, profileName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get front door: %v", err),
			}, err
		}

		if profileResp.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "front door properties are nil",
			}, errors.New("front door properties are nil")
		}

		logger.Info("WAF policy needs to be created and linked separately", "frontDoorName", profileName)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "WAF policy needs to be created separately using Azure Portal or CLI and linked to Front Door",
		}, errors.New("WAF policy configuration required")

	case "azure_frontdoor_enable_https_redirect":
		logger.Info("applying command: enabling HTTPS redirect", "frontDoorName", profileName)
		// For Front Door Standard/Premium, HTTPS redirect is configured on each Route.
		// This requires iterating through endpoints and routes, which is a more complex operation.
		logger.Info("HTTPS redirect for Front Door Standard/Premium must be configured on each route individually.", "profileName", profileName)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "Enabling HTTPS redirect for Front Door Standard/Premium requires updating individual routes. This must be done via the Azure Portal or CLI.",
		}, errors.New("manual route configuration required")

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *frontDoorCdnService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *frontDoorCdnService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation

	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Warn("azure:frontDoorCdnService:GetRecommendations failed to create azure credential, skipping recommendations", "error", err)
		return allRecommendations, nil
	}

	for _, resource := range existingResources {
		parts := strings.Split(resource.Id, "/")
		var subscriptionID, resourceGroup, profileName string
		for i, part := range parts {
			if part == "subscriptions" && i+1 < len(parts) {
				subscriptionID = parts[i+1]
			}
			if part == "resourceGroups" && i+1 < len(parts) {
				resourceGroup = parts[i+1]
			}
			if part == "profiles" && i+1 < len(parts) {
				profileName = parts[i+1]
			}
		}

		if subscriptionID == "" {
			subscriptionID = session.SubscriptionID
		}
		if resourceGroup == "" || profileName == "" {
			continue
		}

		endpointsClient, err := armcdn.NewAFDEndpointsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Warn("failed to create AFD endpoints client", "error", err, "profile", profileName)
			continue
		}

		pager := endpointsClient.NewListByProfilePager(resourceGroup, profileName, nil)
		hasEndpoints := false
		if pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("failed to list endpoints for Front Door profile, skipping recommendation", "error", err, "profile", profileName)
				continue
			}
			if len(page.Value) > 0 {
				hasEndpoints = true
			}
		}
		if !hasEndpoints {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_frontdoor_profile_no_endpoints",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"reason": "Front Door profile has no endpoints configured and is not serving any traffic."},
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

func (s *frontDoorCdnService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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

func (s *frontDoorCdnService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "frontdoor-cdn",
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
		Status:      string(resource.Status),
	}
	return app, nil
}
