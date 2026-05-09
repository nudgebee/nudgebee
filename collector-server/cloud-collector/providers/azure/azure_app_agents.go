package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
)

type appAgentsService struct {
}

func (s *appAgentsService) Name() string {
	return "Microsoft.App/containerApps"
}

// Scope returns the service scope - this is a regional service
func (s *appAgentsService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *appAgentsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armappcontainers.NewContainerAppsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create container apps client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, app := range page.Value {
				status := providers.ResourceStatusUnknown
				if app.Properties != nil && app.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*app.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				createdAt := time.Time{}
				if app.SystemData != nil && app.SystemData.CreatedAt != nil {
					createdAt = *app.SystemData.CreatedAt
				}

				location := ""
				if app.Location != nil {
					location = *app.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *app.ID,
					Name:        *app.Name,
					Type:        *app.Type,
					Region:      location,
					Tags:        toAzureTags(app.Tags),
					Meta:        structToMap(app),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *app.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *appAgentsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *appAgentsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check if ingress is exposed publicly without authentication
			if configuration, ok := props["configuration"].(map[string]interface{}); ok {
				if ingress, ok := configuration["ingress"].(map[string]interface{}); ok {
					if external, ok := ingress["external"].(bool); ok && external {
						// Check if authentication is disabled
						if activeRevisionsMode, ok := props["activeRevisionsMode"].(string); ok {
							if activeRevisionsMode == "Single" {
								allRecommendations = append(allRecommendations, providers.Recommendation{
									CategoryName:        providers.RecommendationCategorySecurity,
									RuleName:            "azure_container_app_public_ingress_no_auth",
									Severity:            providers.RecommendationSeverityHigh,
									Savings:             0,
									Data:                map[string]any{"reason": "Container app has public ingress without authentication enabled"},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								})
							}
						}
					}

					// Check if HTTPS is not enforced
					if allowInsecure, ok := ingress["allowInsecure"].(bool); ok && allowInsecure {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategorySecurity,
							RuleName:            "azure_container_app_insecure_ingress",
							Severity:            providers.RecommendationSeverityHigh,
							Savings:             0,
							Data:                map[string]any{"reason": "Container app allows insecure HTTP traffic"},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}

				// Check if secrets are not using managed identity
				if secrets, ok := configuration["secrets"].([]interface{}); ok && len(secrets) > 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_container_app_secrets_not_keyvault",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Consider using Azure Key Vault for secrets management"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if managed identity is not enabled
			if identity, ok := meta["identity"].(map[string]interface{}); ok {
				if identityType, ok := identity["type"].(string); !ok || strings.ToLower(identityType) == "none" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_container_app_no_managed_identity",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Enable managed identity for secure access to Azure resources"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for minimum replicas
			if template, ok := props["template"].(map[string]interface{}); ok {
				if scale, ok := template["scale"].(map[string]interface{}); ok {
					if minReplicas, ok := scale["minReplicas"].(float64); ok && minReplicas < 2 {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "azure_container_app_low_min_replicas",
							Severity:            providers.RecommendationSeverityMedium,
							Savings:             0,
							Data:                map[string]any{"reason": "Consider setting minimum replicas to at least 2 for high availability", "currentMinReplicas": minReplicas},
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

		// Check if container app has tags for organization
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_container_app_missing_tags",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"reason": "Add tags to container apps for better organization and cost tracking"},
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

func (s *appAgentsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Extract subscription ID from resource ID
	parts := strings.Split(recommendation.ResourceId, "/")
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}
	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}

	client, err := armappcontainers.NewContainerAppsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create container apps client: %w", err)
	}

	// Extract resource group and app name from resource ID
	var resourceGroup, appName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "containerApps" && i+1 < len(parts) {
			appName = parts[i+1]
		}
	}

	if resourceGroup == "" || appName == "" {
		return fmt.Errorf("failed to extract resource group or app name from resource ID: %s", recommendation.ResourceId)
	}

	// Get the current container app configuration
	appResp, err := client.Get(ctx.GetContext(), resourceGroup, appName, nil)
	if err != nil {
		return fmt.Errorf("failed to get container app: %w", err)
	}

	app := appResp.ContainerApp
	if app.Properties == nil {
		return fmt.Errorf("container app properties are nil")
	}

	// Apply the recommendation based on the rule name
	modified := false
	switch recommendation.RuleName {
	case "azure_container_app_insecure_ingress":
		// Disable insecure HTTP traffic
		if app.Properties.Configuration != nil && app.Properties.Configuration.Ingress != nil {
			allowInsecure := false
			app.Properties.Configuration.Ingress.AllowInsecure = &allowInsecure
			modified = true
			logger.Info("applying recommendation: disabling insecure ingress", "appName", appName)
		}

	case "azure_container_app_no_managed_identity":
		// Enable system-assigned managed identity
		if app.Identity == nil {
			app.Identity = &armappcontainers.ManagedServiceIdentity{}
		}
		identityType := armappcontainers.ManagedServiceIdentityTypeSystemAssigned
		app.Identity.Type = &identityType
		modified = true
		logger.Info("applying recommendation: enabling managed identity", "appName", appName)

	case "azure_container_app_public_ingress_no_auth":
		// Cannot auto-fix this - requires user to configure authentication
		return fmt.Errorf("cannot auto-apply recommendation '%s': authentication configuration must be specified manually", recommendation.RuleName)

	case "azure_container_app_secrets_not_keyvault":
		// Cannot auto-fix this - requires user to set up Key Vault reference
		return fmt.Errorf("cannot auto-apply recommendation '%s': Key Vault reference must be configured manually", recommendation.RuleName)

	case "azure_container_app_low_min_replicas":
		// Cannot auto-fix this - requires user decision on replica count
		return fmt.Errorf("cannot auto-apply recommendation '%s': minimum replica count should be reviewed manually", recommendation.RuleName)

	case "azure_container_app_missing_tags":
		// Cannot auto-fix this - requires user to specify tags
		return fmt.Errorf("cannot auto-apply recommendation '%s': tags must be specified manually", recommendation.RuleName)

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}

	if !modified {
		return fmt.Errorf("no changes were made for recommendation: %s", recommendation.RuleName)
	}

	// Update the container app
	poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, appName, app, nil)
	if err != nil {
		return fmt.Errorf("failed to update container app: %w", err)
	}

	_, err = poller.PollUntilDone(ctx.GetContext(), nil)
	if err != nil {
		return fmt.Errorf("failed to wait for container app update: %w", err)
	}

	logger.Info("successfully applied recommendation", "ruleName", recommendation.RuleName, "appName", appName)
	return nil
}

func (s *appAgentsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}
	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}

	client, err := armappcontainers.NewContainerAppsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create container apps client: %v", err),
		}, err
	}

	// Extract resource group and app name from resource ID
	var resourceGroup, appName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "containerApps" && i+1 < len(parts) {
			appName = parts[i+1]
		}
	}

	if resourceGroup == "" || appName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or app name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	// Get the current container app configuration
	appResp, err := client.Get(ctx.GetContext(), resourceGroup, appName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get container app: %v", err),
		}, err
	}

	app := appResp.ContainerApp
	if app.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "container app properties are nil",
		}, fmt.Errorf("container app properties are nil")
	}

	// Execute the command
	switch command.Command {
	case "update_min_replicas":
		if minReplicas, ok := command.Args["min_replicas"].(float64); ok {
			if app.Properties.Template == nil {
				app.Properties.Template = &armappcontainers.Template{}
			}
			if app.Properties.Template.Scale == nil {
				app.Properties.Template.Scale = &armappcontainers.Scale{}
			}
			minReplicasInt := int32(minReplicas)
			app.Properties.Template.Scale.MinReplicas = &minReplicasInt
			logger.Info("executing command: updating min replicas", "appName", appName, "minReplicas", minReplicasInt)

			// Update the container app
			poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, appName, app, nil)
			if err != nil {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("failed to update container app: %v", err),
				}, err
			}
			_, err = poller.PollUntilDone(ctx.GetContext(), nil)
			if err != nil {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("failed to wait for container app update: %v", err),
				}, err
			}
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "min_replicas argument is required and must be a number",
			}, fmt.Errorf("missing or invalid min_replicas argument")
		}

	case "update_max_replicas":
		if maxReplicas, ok := command.Args["max_replicas"].(float64); ok {
			if app.Properties.Template == nil {
				app.Properties.Template = &armappcontainers.Template{}
			}
			if app.Properties.Template.Scale == nil {
				app.Properties.Template.Scale = &armappcontainers.Scale{}
			}
			maxReplicasInt := int32(maxReplicas)
			app.Properties.Template.Scale.MaxReplicas = &maxReplicasInt
			logger.Info("executing command: updating max replicas", "appName", appName, "maxReplicas", maxReplicasInt)

			// Update the container app
			poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, appName, app, nil)
			if err != nil {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("failed to update container app: %v", err),
				}, err
			}
			_, err = poller.PollUntilDone(ctx.GetContext(), nil)
			if err != nil {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("failed to wait for container app update: %v", err),
				}, err
			}
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "max_replicas argument is required and must be a number",
			}, fmt.Errorf("missing or invalid max_replicas argument")
		}

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s. Supported commands: update_min_replicas, update_max_replicas", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	logger.Info("successfully executed command", "command", command.Command, "appName", appName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on container app '%s'", command.Command, appName),
	}, nil
}

func (s *appAgentsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Azure Container Apps logs are typically found in Log Analytics workspace
	// The resourceId itself can be used to query container app logs via Azure Monitor
	return resourceId, nil
}

func (s *appAgentsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "containerapp",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      string(resource.Status),
	}

	// Extract resource group as upstream dependency
	if rg, err := extractResourceGroup(resource.Id); err == nil {
		app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      rg,
				Kind:      "Microsoft.Resources/resourceGroups",
				Namespace: resource.Region,
			},
		}.ToUpstreamLink())
	}

	// Extract managed environment as upstream dependency
	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if managedEnvironmentID, ok := props["managedEnvironmentId"].(string); ok {
			app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{
					Name:      managedEnvironmentID,
					Kind:      "Microsoft.App/managedEnvironments",
					Namespace: resource.Region,
				},
			}.ToUpstreamLink())
		}

		// Extract container registry as downstream dependency
		if template, ok := props["template"].(map[string]interface{}); ok {
			if containers, ok := template["containers"].([]interface{}); ok {
				for _, container := range containers {
					if containerMap, ok := container.(map[string]interface{}); ok {
						if image, ok := containerMap["image"].(string); ok {
							// Extract registry from image
							if strings.Contains(image, ".azurecr.io") {
								registryName := strings.Split(image, ".azurecr.io")[0]
								app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
									Id: providers.ServiceApplicationId{
										Name:      registryName,
										Kind:      "Microsoft.ContainerRegistry/registries",
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

	return app, nil
}
