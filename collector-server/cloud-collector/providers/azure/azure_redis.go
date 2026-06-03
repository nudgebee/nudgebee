package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

type redisService struct {
}

func (s *redisService) Name() string {
	return "Microsoft.Cache/redis"
}

// Scope returns the service scope - this is a regional service
func (s *redisService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *redisService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		client, err := armredis.NewClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create redis client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, redisCache := range page.Value {
				status := providers.ResourceStatusUnknown
				if redisCache.Properties != nil && redisCache.Properties.ProvisioningState != nil {
					provisioningState := string(*redisCache.Properties.ProvisioningState)
					if val, ok := nbStatusFromAzureProvisioningState[provisioningState]; ok {
						status = val
					}
				}

				createdAt := getCreatedAtFromTags(redisCache.Tags)

				allResources = append(allResources, providers.Resource{
					Id:          *redisCache.ID,
					Name:        *redisCache.Name,
					Type:        *redisCache.Type,
					Region:      normalizeAzureRegion(*redisCache.Location),
					Tags:        toAzureTags(redisCache.Tags),
					Meta:        structToMap(redisCache),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *redisCache.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *redisService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *redisService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and redis cache name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, redisCacheName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "redis" && i+1 < len(parts) {
			redisCacheName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || redisCacheName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or redis cache name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armredis.NewClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create redis client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_redis_non_ssl_port_enabled":
		logger.Info("applying command: disabling non-SSL port", "redisCache", redisCacheName)

		redisResp, err := client.Get(ctx.GetContext(), resourceGroup, redisCacheName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get redis cache: %v", err),
			}, err
		}

		redisCache := redisResp.ResourceInfo
		if redisCache.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "redis cache properties are nil",
			}, fmt.Errorf("redis cache properties are nil")
		}

		updateParams := armredis.UpdateParameters{
			Properties: &armredis.UpdateProperties{
				EnableNonSSLPort: to.Ptr(false),
			},
		}

		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, redisCacheName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update redis cache: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update redis cache: %v", err),
			}, err
		}

		logger.Info("successfully disabled non-SSL port", "redisCache", redisCacheName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully disabled non-SSL port for redis cache '%s'", redisCacheName),
		}, nil

	case "azure_redis_set_minimum_tls_version":
		// Set minimum TLS version to 1.2
		logger.Info("applying command: setting minimum TLS version to 1.2", "redisCache", redisCacheName)

		redisResp, err := client.Get(ctx.GetContext(), resourceGroup, redisCacheName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get redis cache: %v", err),
			}, err
		}

		redisCache := redisResp.ResourceInfo
		if redisCache.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "redis cache properties are nil",
			}, fmt.Errorf("redis cache properties are nil")
		}

		updateParams := armredis.UpdateParameters{
			Properties: &armredis.UpdateProperties{
				MinimumTLSVersion: to.Ptr(armredis.TLSVersionOne2),
			},
		}

		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, redisCacheName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update redis cache: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for redis cache update: %v", err),
			}, err
		}

		logger.Info("successfully set minimum TLS version", "redisCache", redisCacheName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully set minimum TLS version to 1.2 for redis cache '%s'", redisCacheName),
		}, nil

	case "azure_redis_enable_firewall":
		// Enable firewall rules
		logger.Info("applying command: enabling firewall", "redisCache", redisCacheName)

		// Note: This would require creating firewall rules
		// This is a placeholder showing the intent
		logger.Info("firewall configuration requires creating specific rules", "redisCache", redisCacheName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("please configure firewall rules for redis cache '%s' via Azure Portal or FirewallRules API", redisCacheName),
		}, nil

	case "regenerate_primary_key":
		// Regenerate primary access key
		logger.Info("applying command: regenerating primary key", "redisCache", redisCacheName)

		regenerateParams := armredis.RegenerateKeyParameters{
			KeyType: to.Ptr(armredis.RedisKeyTypePrimary),
		}

		_, err := client.RegenerateKey(ctx.GetContext(), resourceGroup, redisCacheName, regenerateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to regenerate key: %v", err),
			}, err
		}

		logger.Info("successfully regenerated primary key", "redisCache", redisCacheName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully regenerated primary key for redis cache '%s'", redisCacheName),
		}, nil

	case "regenerate_secondary_key":
		// Regenerate secondary access key
		logger.Info("applying command: regenerating secondary key", "redisCache", redisCacheName)

		regenerateParams := armredis.RegenerateKeyParameters{
			KeyType: to.Ptr(armredis.RedisKeyTypeSecondary),
		}

		_, err := client.RegenerateKey(ctx.GetContext(), resourceGroup, redisCacheName, regenerateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to regenerate key: %v", err),
			}, err
		}

		logger.Info("successfully regenerated secondary key", "redisCache", redisCacheName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully regenerated secondary key for redis cache '%s'", redisCacheName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *redisService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *redisService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check if non-SSL port is enabled (security risk)
			if enableNonSSLPort, ok := props["enableNonSslPort"].(bool); ok && enableNonSSLPort {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_redis_non_ssl_port_enabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Redis cache has non-SSL port enabled, which poses a security risk"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check minimum TLS version
			if minimumTLSVersion, ok := props["minimumTlsVersion"].(string); ok {
				if minimumTLSVersion != "1.2" && minimumTLSVersion != "" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_redis_old_tls_version",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": fmt.Sprintf("Redis cache uses TLS version %s, should use 1.2 for better security", minimumTLSVersion)},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if firewall rules are configured
			// Note: This would require additional API call to list firewall rules
			// For now, we'll just check if public network access is enabled
			if publicNetworkAccess, ok := props["publicNetworkAccess"].(string); ok {
				if strings.ToLower(publicNetworkAccess) == "enabled" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_redis_public_network_access",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Public network access is enabled; consider restricting access with firewall rules to improve security"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check SKU for cost optimization
			if sku, ok := props["sku"].(map[string]interface{}); ok {
				if family, ok := sku["family"].(string); ok {
					if capacity, ok := sku["capacity"].(float64); ok {
						// If using Premium with low capacity, recommend Basic/Standard
						if family == "P" && capacity <= 1 {
							allRecommendations = append(allRecommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryRightSizing,
								RuleName:            "azure_redis_overprovisioned_sku",
								Severity:            providers.RecommendationSeverityLow,
								Savings:             0,
								Data:                map[string]any{"reason": "Premium tier with low capacity detected; consider using Standard tier for low-capacity workloads to reduce costs"},
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
		}
	}
	return allRecommendations, nil
}

func (s *redisService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "redis-cache",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *redisService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
