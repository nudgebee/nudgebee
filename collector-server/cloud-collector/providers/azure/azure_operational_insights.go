package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

type operationalInsightsService struct {
}

func (s *operationalInsightsService) Name() string {
	return "microsoft.operationalinsights/workspaces"
}

// Scope returns the service scope - this is a regional service
func (s *operationalInsightsService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *operationalInsightsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armoperationalinsights.NewWorkspacesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create operational insights client: %w", err)
		}

		pager := client.NewListPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, workspace := range page.Value {
				status := providers.ResourceStatusUnknown
				if workspace.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*workspace.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				createdAt := time.Time{}
				if workspace.Properties.CreatedDate != nil {
					if t, err := time.Parse(time.RFC3339, *workspace.Properties.CreatedDate); err == nil {
						createdAt = t
					}
				}

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

func (s *operationalInsightsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *operationalInsightsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			if retention, ok := properties["retentionInDays"].(float64); !ok || retention < 30 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_operationalinsights_workspace_low_retention",
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
	}
	return recommendations, nil
}

func (s *operationalInsightsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *operationalInsightsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	client, err := armoperationalinsights.NewWorkspacesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create operational insights client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_operationalinsights_workspace_low_retention":
		// Increase retention to 30 days
		logger.Info("applying command: updating workspace retention to 30 days", "workspaceName", workspaceName)

		// Get current workspace
		workspaceResp, err := client.Get(ctx.GetContext(), resourceGroup, workspaceName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get workspace: %v", err),
			}, err
		}

		workspace := workspaceResp.Workspace
		if workspace.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "workspace properties are nil",
			}, fmt.Errorf("workspace properties are nil")
		}

		// Set retention to 30 days
		retentionDays := int32(30)
		workspace.Properties.RetentionInDays = &retentionDays

		// Update workspace
		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, workspaceName, workspace, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update workspace: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for workspace update: %v", err),
			}, err
		}

		logger.Info("successfully updated workspace retention", "workspaceName", workspaceName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully updated workspace '%s' retention to 30 days", workspaceName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *operationalInsightsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	subID, err := extractSubscriptionID(resourceId)
	if err != nil {
		subID = strings.Split(session.SubscriptionID, ",")[0]
	}

	rg, err := extractResourceGroup(resourceId)
	if err != nil {
		return "", fmt.Errorf("failed to extract resource group from resource id: %w", err)
	}

	// Extract workspace name from resource ID
	parts := strings.Split(resourceId, "/")
	workspaceName := ""
	for i, p := range parts {
		if strings.EqualFold(p, "workspaces") && i+1 < len(parts) {
			workspaceName = parts[i+1]
			break
		}
	}
	if workspaceName == "" {
		return "", fmt.Errorf("failed to extract workspace name from resource id: %s", resourceId)
	}

	client, err := armoperationalinsights.NewWorkspacesClient(subID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to create operational insights client: %w", err)
	}

	workspace, err := client.Get(ctx.GetContext(), rg, workspaceName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get workspace: %w", err)
	}

	if workspace.Properties == nil || workspace.Properties.CustomerID == nil || *workspace.Properties.CustomerID == "" {
		return "", fmt.Errorf("workspace %s has no customer ID", workspaceName)
	}

	return *workspace.Properties.CustomerID, nil
}

func (s *operationalInsightsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
