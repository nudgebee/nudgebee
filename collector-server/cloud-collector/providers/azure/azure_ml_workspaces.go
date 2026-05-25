package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type mlWorkspacesService struct {
}

func (s *mlWorkspacesService) Name() string {
	return "microsoft.machinelearningservices/workspaces"
}

// Scope returns the service scope - this is a regional service
func (s *mlWorkspacesService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *mlWorkspacesService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armmachinelearning.NewWorkspacesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create ml workspaces client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, workspace := range page.Value {
				status := providers.ResourceStatusUnknown
				if workspace.Properties != nil && workspace.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*workspace.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				createdAt := getCreatedAtFromTags(workspace.Tags)

				allResources = append(allResources, providers.Resource{
					Id:          *workspace.ID,
					Name:        *workspace.Name,
					Type:        *workspace.Type,
					Region:      *workspace.Location,
					Tags:        toAzureTags(workspace.Tags),
					Meta:        structToMap(workspace),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *workspace.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *mlWorkspacesService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *mlWorkspacesService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check for public network access
			if publicAccess, ok := props["publicNetworkAccess"].(string); !ok || publicAccess != "Disabled" {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_ml_workspace_public_network_access_enabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "ML Workspace should disable public network access to enhance security and prevent unauthorized access"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for high business impact
			if hbi, ok := props["hbiWorkspace"].(bool); !ok || !hbi {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_ml_workspace_hbi_not_enabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Consider enabling High Business Impact (HBI) mode for sensitive workloads to enforce stricter compliance and data protection"},
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
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "azure_ml_workspace_managed_identity_disabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"reason": "ML Workspace should use a managed identity for secure authentication without storing credentials"},
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

func (s *mlWorkspacesService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *mlWorkspacesService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and workspace name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, workspaceName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "workspaces" && i+1 < len(parts) {
			workspaceName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || workspaceName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or workspace name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armmachinelearning.NewWorkspacesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create ml workspaces client: %v", err),
		}, err
	}

	workspaceResp, err := client.Get(ctx.GetContext(), resourceGroup, workspaceName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to get ml workspace: %v", err)}, err
	}
	workspace := workspaceResp.Workspace

	switch command.Command {
	case "azure_ml_workspace_public_network_access_enabled":
		logger.Info("applying command: disabling public network access", "workspaceName", workspaceName)

		updateParams := armmachinelearning.WorkspaceUpdateParameters{
			Tags: workspace.Tags,
			SKU:  workspace.SKU,
		}

		// Set the public network access property
		disabled := armmachinelearning.PublicNetworkAccessDisabled
		updateParams.Properties = &armmachinelearning.WorkspacePropertiesUpdateParameters{
			PublicNetworkAccess: &disabled,
		}

		_, err = client.Update(ctx.GetContext(), resourceGroup, workspaceName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to update ml workspace: %v", err)}, err
		}

	case "azure_ml_workspace_hbi_not_enabled":
		// Cannot auto-fix - HBI mode cannot be changed after workspace creation
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "cannot auto-apply command: High Business Impact mode can only be set during workspace creation",
		}, fmt.Errorf("HBI mode requires workspace recreation")

	case "azure_ml_workspace_managed_identity_disabled":
		logger.Info("applying command: enabling system-assigned managed identity", "workspaceName", workspaceName)

		// For ML Workspace, identity is set at the workspace level
		updateParams := armmachinelearning.WorkspaceUpdateParameters{
			Tags: workspace.Tags,
			SKU:  workspace.SKU,
			Identity: &armmachinelearning.Identity{
				Type: to.Ptr(armmachinelearning.ResourceIdentityTypeSystemAssigned),
			},
		}

		// Note: Identity update for ML workspaces may require BeginCreateOrUpdate
		// This is a placeholder that may need adjustment based on actual SDK behavior
		_, err = client.Update(ctx.GetContext(), resourceGroup, workspaceName, updateParams, nil)
		if err != nil {
			return providers.ApplyCommandResponse{Success: false, Message: fmt.Sprintf("failed to update ml workspace identity: %v", err)}, err
		}

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	logger.Info("successfully applied command", "command", command.Command, "workspaceName", workspaceName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on ml workspace '%s'", command.Command, workspaceName),
	}, nil
}

func (s *mlWorkspacesService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *mlWorkspacesService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "mlworkspace",
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

	// Extract storage account, key vault, and app insights as downstreams
	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if storageAccountID, ok := props["storageAccount"].(string); ok && storageAccountID != "" {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{
					Name:      storageAccountID,
					Kind:      "Microsoft.Storage/storageAccounts",
					Namespace: resource.Region,
				},
			}.ToDownstreamLink())
		}

		if keyVaultID, ok := props["keyVault"].(string); ok && keyVaultID != "" {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{
					Name:      keyVaultID,
					Kind:      "Microsoft.KeyVault/vaults",
					Namespace: resource.Region,
				},
			}.ToDownstreamLink())
		}

		if appInsightsID, ok := props["applicationInsights"].(string); ok && appInsightsID != "" {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{
					Name:      appInsightsID,
					Kind:      "Microsoft.Insights/components",
					Namespace: resource.Region,
				},
			}.ToDownstreamLink())
		}
	}

	return app, nil
}
