package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type eventGridService struct {
}

func (s *eventGridService) Name() string {
	return "microsoft.eventgrid/topics"
}

// Scope returns the service scope - this is a regional service
func (s *eventGridService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *eventGridService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get Event Grid Topics
		topicsClient, err := armeventgrid.NewTopicsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create topics client: %w", err)
		}

		topicsPager := topicsClient.NewListBySubscriptionPager(nil)
		for topicsPager.More() {
			page, err := topicsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of topics: %w", err)
			}

			for _, topic := range page.Value {
				if topic.ID == nil || topic.Name == nil || topic.Type == nil || topic.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if topic.Properties != nil && topic.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*topic.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				meta := structToMap(topic.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				createdAt := time.Now()
				if topic.SystemData != nil && topic.SystemData.CreatedAt != nil {
					createdAt = *topic.SystemData.CreatedAt
				}

				allResources = append(allResources, providers.Resource{
					Id:          *topic.ID,
					Name:        *topic.Name,
					Type:        *topic.Type,
					Region:      normalizeAzureRegion(*topic.Location),
					Tags:        toAzureTags(topic.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *topic.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Event Grid Domains
		domainsClient, err := armeventgrid.NewDomainsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create domains client: %w", err)
		}

		domainsPager := domainsClient.NewListBySubscriptionPager(nil)
		for domainsPager.More() {
			page, err := domainsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of domains: %w", err)
			}

			for _, domain := range page.Value {
				if domain.ID == nil || domain.Name == nil || domain.Type == nil || domain.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if domain.Properties != nil && domain.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*domain.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				meta := structToMap(domain.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *domain.ID,
					Name:        *domain.Name,
					Type:        *domain.Type,
					Region:      normalizeAzureRegion(*domain.Location),
					Tags:        toAzureTags(domain.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *domain.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get System Topics
		systemTopicsClient, err := armeventgrid.NewSystemTopicsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create system topics client: %w", err)
		}

		systemTopicsPager := systemTopicsClient.NewListBySubscriptionPager(nil)
		for systemTopicsPager.More() {
			page, err := systemTopicsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of system topics: %w", err)
			}

			for _, systemTopic := range page.Value {
				if systemTopic.ID == nil || systemTopic.Name == nil || systemTopic.Type == nil || systemTopic.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if systemTopic.Properties != nil && systemTopic.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*systemTopic.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				meta := structToMap(systemTopic.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *systemTopic.ID,
					Name:        *systemTopic.Name,
					Type:        *systemTopic.Type,
					Region:      normalizeAzureRegion(*systemTopic.Location),
					Tags:        toAzureTags(systemTopic.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *systemTopic.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *eventGridService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *eventGridService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	topicCount := 0
	domainCount := 0

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for topics
		if strings.Contains(strings.ToLower(resource.Type), "topics") && !strings.Contains(strings.ToLower(resource.Type), "system") {
			topicCount++

			// Check for topics without event subscriptions
			if publicNetworkAccess, ok := properties["publicNetworkAccess"].(string); ok && publicNetworkAccess == "Enabled" {
				// Check if topic allows public access without IP filtering
				if inboundIPRules, ok := properties["inboundIpRules"].([]interface{}); !ok || len(inboundIPRules) == 0 {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_eventgrid_topic_public_access_no_ip_filter",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"reason": "Event Grid topic allows public access without IP filtering",
							"properties": map[string]any{
								"publicNetworkAccess": publicNetworkAccess,
								"inboundIpRules":      inboundIPRules,
							},
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

			// Check for topics without managed identity
			if identity, ok := properties["identity"]; !ok || identity == nil {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_eventgrid_topic_no_managed_identity",
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

		// Check for domains
		if strings.Contains(strings.ToLower(resource.Type), "domain") {
			domainCount++

			// Check for domains with public access enabled
			if publicNetworkAccess, ok := properties["publicNetworkAccess"].(string); ok && publicNetworkAccess == "Enabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_eventgrid_domain_public_access_enabled",
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

			// Check for domains without disable local auth
			if disableLocalAuth, ok := properties["disableLocalAuth"].(bool); !ok || !disableLocalAuth {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_eventgrid_domain_local_auth_enabled",
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

		// Check for failed provisioning state
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_eventgrid_resource_failed_provisioning",
				Severity:            providers.RecommendationSeverityHigh,
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

	return recommendations, nil
}

func (s *eventGridService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *eventGridService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Event Grid security changes require manual review
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Event Grid: %s requires manual review for security configuration", command.Command),
	}, errors.ErrUnsupported
}

func (s *eventGridService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *eventGridService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
