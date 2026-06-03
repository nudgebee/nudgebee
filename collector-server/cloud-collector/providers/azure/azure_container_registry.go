package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type containerRegistryService struct {
}

func (s *containerRegistryService) Name() string {
	return "microsoft.containerregistry/registries"
}

// Scope returns the service scope - this is a regional service
func (s *containerRegistryService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *containerRegistryService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armcontainerregistry.NewRegistriesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create container registry client: %w", err)
		}

		pager := client.NewListPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, reg := range page.Value {
				if reg.ID == nil || reg.Name == nil || reg.Type == nil || reg.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				createdAt := time.Time{}
				if reg.Properties != nil {
					if reg.Properties.ProvisioningState != nil {
						if val, ok := nbStatusFromAzureProvisioningState[string(*reg.Properties.ProvisioningState)]; ok {
							status = val
						}
					}
					if reg.Properties.CreationDate != nil {
						createdAt = *reg.Properties.CreationDate
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *reg.ID,
					Name:        *reg.Name,
					Type:        *reg.Type,
					Region:      *reg.Location,
					Tags:        toAzureTags(reg.Tags),
					Meta:        structToMap(reg.Properties),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *reg.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *containerRegistryService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *containerRegistryService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		properties := resource.Meta
		// Check for Admin User Enabled
		if adminUserEnabled, ok := properties["adminUserEnabled"].(bool); ok && adminUserEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_container_registry_admin_user_enabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"reason": "Disable admin user to enhance security",
					"meta":   resource.Meta,
					"tags":   resource.Tags,
					"status": resource.Status,
					"region": resource.Region,
					"type":   resource.Type,
					"name":   resource.Name,
					"id":     resource.Id,
					"arn":    resource.Arn,
					"createdAt": map[string]any{
						"date": resource.CreatedAt,
					},
					"serviceName": resource.ServiceName,
					"properties":  properties,
					"resource":    resource,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for Public Network Access
		if publicNetworkAccess, ok := properties["publicNetworkAccess"].(string); ok && publicNetworkAccess != "Disabled" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_container_registry_public_network_access_enabled",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"reason": "Disable public network access to enhance security",
					"meta":   resource.Meta,
					"tags":   resource.Tags,
					"status": resource.Status,
					"region": resource.Region,
					"type":   resource.Type,
					"name":   resource.Name,
					"id":     resource.Id,
					"arn":    resource.Arn,
					"createdAt": map[string]any{
						"date": resource.CreatedAt,
					},
					"serviceName": resource.ServiceName,
					"properties":  properties,
					"resource":    resource,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
		// Check for CMK Encryption
		if encryption, ok := properties["encryption"].(map[string]interface{}); ok {
			if status, ok := encryption["status"].(string); ok && status != "enabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_container_registry_cmk_encryption_disabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"reason": "Enable customer-managed key (CMK) encryption for enhanced security",
						"meta":   resource.Meta,
						"tags":   resource.Tags,
						"status": resource.Status,
						"region": resource.Region,
						"type":   resource.Type,
						"name":   resource.Name,
						"id":     resource.Id,
						"arn":    resource.Arn,
						"createdAt": map[string]any{
							"date": resource.CreatedAt,
						},
						"serviceName":    resource.ServiceName,
						"properties":     properties,
						"resource":       resource,
						"encryption":     encryption,
						"networkRuleSet": properties["networkRuleSet"],
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for IP Network Rules
		if networkRuleSet, ok := properties["networkRuleSet"].(map[string]interface{}); ok {
			if defaultAction, ok := networkRuleSet["defaultAction"].(string); ok && defaultAction != "Deny" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_container_registry_no_ip_rules",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason": "Configure IP network rules to restrict access",
						"meta":   resource.Meta,
						"tags":   resource.Tags,
						"status": resource.Status,
						"region": resource.Region,
						"type":   resource.Type,
						"name":   resource.Name,
						"id":     resource.Id,
						"arn":    resource.Arn,
						"createdAt": map[string]any{
							"date": resource.CreatedAt,
						},
						"serviceName":    resource.ServiceName,
						"properties":     properties,
						"resource":       resource,
						"networkRuleSet": networkRuleSet,
						"defaultAction":  defaultAction,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
			if bypass, ok := networkRuleSet["bypass"].(string); ok && bypass != "AzureServices" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_container_registry_trusted_ms_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason": "Enable trusted Microsoft services bypass for improved connectivity",
						"meta":   resource.Meta,
						"tags":   resource.Tags,
						"status": resource.Status,
						"region": resource.Region,
						"type":   resource.Type,
						"name":   resource.Name,
						"id":     resource.Id,
						"arn":    resource.Arn,
						"createdAt": map[string]any{
							"date": resource.CreatedAt,
						},
						"serviceName":    resource.ServiceName,
						"properties":     properties,
						"resource":       resource,
						"networkRuleSet": networkRuleSet,
						"bypass":         bypass,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for ARM Audience Token Authentication
		if azureADAuth, ok := properties["azureADAuthenticationAsArmPolicy"].(map[string]interface{}); ok {
			if status, ok := azureADAuth["status"].(string); ok && status == "enabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_container_registry_arm_token_auth_enabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason": "Disable ARM audience token authentication to enhance security",
						"meta":   resource.Meta,
						"tags":   resource.Tags,
						"status": resource.Status,
						"region": resource.Region,
						"type":   resource.Type,
						"name":   resource.Name,
						"id":     resource.Id,
						"arn":    resource.Arn,
						"createdAt": map[string]any{
							"date": resource.CreatedAt,
						},
						"serviceName":              resource.ServiceName,
						"properties":               properties,
						"resource":                 resource,
						"azureADAuth":              azureADAuth,
						"networkRuleSet":           properties["networkRuleSet"],
						"networkRuleBypassOptions": properties["networkRuleBypassOptions"],
						"publicNetworkAccess":      properties["publicNetworkAccess"],
						"adminUserEnabled":         properties["adminUserEnabled"],
						"encryption":               properties["encryption"],
						"identity":                 resource.Meta["identity"],
						"privateEndpoints":         properties["privateEndpointConnections"],
						"zoneRedundancy":           properties["zoneRedundancy"],
						"retentionPolicy":          properties["retentionPolicy"],
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for Soft Delete
		if retentionPolicy, ok := properties["retentionPolicy"].(map[string]interface{}); ok {
			if status, ok := retentionPolicy["status"].(string); ok && status != "enabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_container_registry_soft_delete_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason": "Enable soft delete to prevent accidental data loss",
						"meta":   resource.Meta,
						"tags":   resource.Tags,
						"status": resource.Status,
						"region": resource.Region,
						"type":   resource.Type,
						"name":   resource.Name,
						"id":     resource.Id,
						"arn":    resource.Arn,
						"createdAt": map[string]any{
							"date": resource.CreatedAt,
						},
						"serviceName": resource.ServiceName,
						"properties":  properties,
						"resource":    resource,
						"retentionPolicy": map[string]any{
							"status": retentionPolicy["status"],
						},
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for Managed Identity
		if identity, ok := resource.Meta["identity"].(map[string]interface{}); !ok || identity == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_container_registry_no_managed_identity",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"reason": "Enable managed identity for better security and access management",
					"meta":   resource.Meta,
					"tags":   resource.Tags,
					"status": resource.Status,
					"region": resource.Region,
					"type":   resource.Type,
					"name":   resource.Name,
					"id":     resource.Id,
					"arn":    resource.Arn,
					"createdAt": map[string]any{
						"date": resource.CreatedAt,
					},
					"serviceName": resource.ServiceName,
					"properties":  properties,
					"resource":    resource,
					"identity":    identity,
					"privateEndpoints": map[string]any{
						"privateEndpointConnections": properties["privateEndpointConnections"],
					},
					"zoneRedundancy": map[string]any{
						"zoneRedundancy": properties["zoneRedundancy"],
					},
					"retentionPolicy": map[string]any{
						"retentionPolicy": properties["retentionPolicy"],
					},
					"publicNetworkAccess": map[string]any{
						"publicNetworkAccess": properties["publicNetworkAccess"],
					},
					"adminUserEnabled": map[string]any{
						"adminUserEnabled": properties["adminUserEnabled"],
					},
					"encryption": map[string]any{
						"encryption": properties["encryption"],
					},
					"networkRuleSet": map[string]any{
						"networkRuleSet": properties["networkRuleSet"],
					},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for Private Endpoints
		if privateEndpoints, ok := properties["privateEndpointConnections"].([]interface{}); !ok || len(privateEndpoints) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_container_registry_no_private_endpoints",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"reason": "Configure private endpoints to enhance security",
					"meta":   resource.Meta,
					"tags":   resource.Tags,
					"status": resource.Status,
					"region": resource.Region,
					"type":   resource.Type,
					"name":   resource.Name,
					"id":     resource.Id,
					"arn":    resource.Arn,
					"createdAt": map[string]any{
						"date": resource.CreatedAt,
					},
					"serviceName": resource.ServiceName,
					"properties":  properties,
					"resource":    resource,
					"identity":    resource.Meta["identity"],
					"privateEndpoints": map[string]any{
						"privateEndpointConnections": properties["privateEndpointConnections"],
					},
					"zoneRedundancy": map[string]any{
						"zoneRedundancy": properties["zoneRedundancy"],
					},
					"retentionPolicy": map[string]any{
						"retentionPolicy": properties["retentionPolicy"],
					},
					"publicNetworkAccess": map[string]any{
						"publicNetworkAccess": properties["publicNetworkAccess"],
					},
					"adminUserEnabled": map[string]any{
						"adminUserEnabled": properties["adminUserEnabled"],
					},
					"encryption": map[string]any{
						"encryption": properties["encryption"],
					},
					"networkRuleSet": map[string]any{
						"networkRuleSet": properties["networkRuleSet"],
					},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for Zone Redundancy
		if zoneRedundancy, ok := properties["zoneRedundancy"].(string); ok && zoneRedundancy != "Enabled" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_container_registry_zone_redundancy_disabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"reason": "Enable zone redundancy for higher availability",
					"meta":   resource.Meta,
					"tags":   resource.Tags,
					"status": resource.Status,
					"region": resource.Region,
					"type":   resource.Type,
					"name":   resource.Name,
					"id":     resource.Id,
					"arn":    resource.Arn,
					"createdAt": map[string]any{
						"date": resource.CreatedAt,
					},
					"serviceName": resource.ServiceName,
					"properties":  properties,
					"resource":    resource,
					"identity":    resource.Meta["identity"],
					"privateEndpoints": map[string]any{
						"privateEndpointConnections": properties["privateEndpointConnections"],
					},
					"zoneRedundancy": map[string]any{
						"zoneRedundancy": properties["zoneRedundancy"],
					},
					"retentionPolicy": map[string]any{
						"retentionPolicy": properties["retentionPolicy"],
					},
					"publicNetworkAccess": map[string]any{
						"publicNetworkAccess": properties["publicNetworkAccess"],
					},
					"adminUserEnabled": map[string]any{
						"adminUserEnabled": properties["adminUserEnabled"],
					},
					"encryption": map[string]any{
						"encryption": properties["encryption"],
					},
					"networkRuleSet": map[string]any{
						"networkRuleSet": properties["networkRuleSet"],
					},
				},
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

func (s *containerRegistryService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *containerRegistryService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	// Extract resource group and registry name from resource ID
	var resourceGroup, registryName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "registries" && i+1 < len(parts) {
			registryName = parts[i+1]
		}
	}

	if resourceGroup == "" || registryName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or registry name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create container registry client: %v", err),
		}, err
	}

	// Get the current registry configuration
	registryResp, err := client.Get(ctx.GetContext(), resourceGroup, registryName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get container registry: %v", err),
		}, err
	}

	registry := registryResp.Registry
	if registry.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "container registry properties are nil",
		}, fmt.Errorf("container registry properties are nil")
	}

	// Apply the command based on the recommendation rule name
	modified := false
	switch command.Command {
	case "azure_container_registry_admin_user_enabled":
		// Disable admin user
		adminUserEnabled := false
		registry.Properties.AdminUserEnabled = &adminUserEnabled
		modified = true
		logger.Info("applying command: disabling admin user", "registryName", registryName)

	case "azure_container_registry_public_network_access_enabled":
		// Disable public network access
		publicNetworkAccess := armcontainerregistry.PublicNetworkAccessDisabled
		registry.Properties.PublicNetworkAccess = &publicNetworkAccess
		modified = true
		logger.Info("applying command: disabling public network access", "registryName", registryName)

	case "azure_container_registry_cmk_encryption_disabled":
		// Cannot auto-fix - requires customer-managed key configuration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: CMK encryption requires manual setup with Azure Key Vault",
		}, fmt.Errorf("CMK encryption requires manual configuration")

	case "azure_container_registry_no_ip_rules":
		// Set default network action to Deny
		if registry.Properties.NetworkRuleSet == nil {
			registry.Properties.NetworkRuleSet = &armcontainerregistry.NetworkRuleSet{}
		}
		defaultAction := armcontainerregistry.DefaultActionDeny
		registry.Properties.NetworkRuleSet.DefaultAction = &defaultAction
		modified = true
		logger.Info("applying command: setting default network action to Deny", "registryName", registryName)

	case "azure_container_registry_trusted_ms_disabled":
		// Enable trusted Azure services bypass
		if registry.Properties.NetworkRuleSet == nil {
			registry.Properties.NetworkRuleSet = &armcontainerregistry.NetworkRuleSet{}
		}
		// Set network rule bypass to AzureServices
		networkRuleBypassOptions := armcontainerregistry.NetworkRuleBypassOptionsAzureServices
		registry.Properties.NetworkRuleBypassOptions = &networkRuleBypassOptions
		modified = true
		logger.Info("applying command: enabling trusted Azure services bypass", "registryName", registryName)

	case "azure_container_registry_soft_delete_disabled":
		// Cannot auto-fix - soft delete policy requires specific retention configuration
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: soft delete policy configuration requires manual setup",
		}, fmt.Errorf("soft delete policy requires manual configuration")

	case "azure_container_registry_no_managed_identity":
		// Cannot auto-fix - requires user to configure managed identity
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: managed identity configuration requires manual setup",
		}, fmt.Errorf("managed identity requires manual configuration")

	case "azure_container_registry_no_private_endpoints":
		// Cannot auto-fix - requires user to create private endpoints
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: private endpoints must be created manually",
		}, fmt.Errorf("private endpoints require manual configuration")

	case "azure_container_registry_zone_redundancy_disabled":
		// Cannot auto-fix - zone redundancy can only be set at creation time
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: zone redundancy can only be enabled during registry creation",
		}, fmt.Errorf("zone redundancy requires registry recreation")

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	if !modified {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("no changes were made for command: %s", command.Command),
		}, fmt.Errorf("no changes made")
	}

	// Update the container registry - create update parameters from registry properties
	updateParams := armcontainerregistry.RegistryUpdateParameters{}
	if registry.Properties.AdminUserEnabled != nil {
		updateParams.Properties = &armcontainerregistry.RegistryPropertiesUpdateParameters{
			AdminUserEnabled:         registry.Properties.AdminUserEnabled,
			PublicNetworkAccess:      registry.Properties.PublicNetworkAccess,
			NetworkRuleSet:           registry.Properties.NetworkRuleSet,
			NetworkRuleBypassOptions: registry.Properties.NetworkRuleBypassOptions,
		}
	}

	poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, registryName, updateParams, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update container registry: %v", err),
		}, err
	}

	_, err = poller.PollUntilDone(ctx.GetContext(), nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to wait for container registry update: %v", err),
		}, err
	}

	logger.Info("successfully applied command", "command", command.Command, "registryName", registryName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on container registry '%s'", command.Command, registryName),
	}, nil
}

func extractSubscriptionID(resourceId string) (string, error) {
	// Resource ID format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroup}/providers/...
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", errors.New("subscription ID not found in resource ID")
}

func (s *containerRegistryService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *containerRegistryService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
