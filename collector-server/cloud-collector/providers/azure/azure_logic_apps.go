package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type logicAppsService struct {
}

func (s *logicAppsService) Name() string {
	return "microsoft.logic/workflows"
}

// Scope returns the service scope - this is a regional service
func (s *logicAppsService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *logicAppsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get Logic App Workflows
		workflowsClient, err := armlogic.NewWorkflowsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create workflows client: %w", err)
		}

		workflowsPager := workflowsClient.NewListBySubscriptionPager(nil)
		for workflowsPager.More() {
			page, err := workflowsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of workflows: %w", err)
			}

			for _, workflow := range page.Value {
				if workflow.ID == nil || workflow.Name == nil || workflow.Type == nil || workflow.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if workflow.Properties != nil && workflow.Properties.State != nil {
					switch *workflow.Properties.State {
					case armlogic.WorkflowStateEnabled:
						status = providers.ResourceStatusActive
					case armlogic.WorkflowStateDisabled:
						status = providers.ResourceStatusInactive
					case armlogic.WorkflowStateSuspended:
						status = providers.ResourceStatusInactive
					case armlogic.WorkflowStateDeleted:
						status = providers.ResourceStatusDeleted
					}
				}

				meta := structToMap(workflow.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				// Add additional metadata
				createdAt := time.Time{}
				if workflow.Properties != nil {
					if workflow.Properties.CreatedTime != nil {
						meta["createdTime"] = workflow.Properties.CreatedTime.Format(time.RFC3339)
						createdAt = *workflow.Properties.CreatedTime
					}
					if workflow.Properties.ChangedTime != nil {
						meta["changedTime"] = workflow.Properties.ChangedTime.Format(time.RFC3339)
					}
					if workflow.Properties.Version != nil {
						meta["version"] = *workflow.Properties.Version
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *workflow.ID,
					Name:        *workflow.Name,
					Type:        *workflow.Type,
					Region:      normalizeAzureRegion(*workflow.Location),
					Tags:        toAzureTags(workflow.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *workflow.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Integration Accounts
		integrationAccountsClient, err := armlogic.NewIntegrationAccountsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create integration accounts client: %w", err)
		}

		integrationAccountsPager := integrationAccountsClient.NewListBySubscriptionPager(nil)
		for integrationAccountsPager.More() {
			page, err := integrationAccountsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of integration accounts: %w", err)
			}

			for _, account := range page.Value {
				if account.ID == nil || account.Name == nil || account.Type == nil || account.Location == nil {
					continue
				}

				status := providers.ResourceStatusActive
				meta := structToMap(account.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *account.ID,
					Name:        *account.Name,
					Type:        *account.Type,
					Region:      normalizeAzureRegion(*account.Location),
					Tags:        toAzureTags(account.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *account.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *logicAppsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *logicAppsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for disabled workflows
		if strings.Contains(strings.ToLower(resource.Type), "workflow") {
			if resource.Status == providers.ResourceStatusInactive {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_logic_app_workflow_disabled",
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

			// Check for workflows without triggers
			if triggers, ok := properties["triggers"].(map[string]interface{}); !ok || len(triggers) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_logic_app_no_triggers",
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

			// Check for workflows without actions
			if actions, ok := properties["actions"].(map[string]interface{}); !ok || len(actions) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_logic_app_no_actions",
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

			// Check for old workflow versions (not updated in > 1 year)
			if changedTime, ok := properties["changedTime"].(string); ok {
				if t, err := time.Parse(time.RFC3339, changedTime); err == nil {
					if time.Since(t) > 365*24*time.Hour {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "azure_logic_app_outdated_workflow",
							Severity:            providers.RecommendationSeverityLow,
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
		}
	}

	return recommendations, nil
}

func (s *logicAppsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *logicAppsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Logic Apps workflow changes require manual review
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Logic Apps: %s requires manual review of workflow logic", command.Command),
	}, errors.ErrUnsupported
}

func (s *logicAppsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *logicAppsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
