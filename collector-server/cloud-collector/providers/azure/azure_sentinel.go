package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/securityinsights/armsecurityinsights/v2"
)

type sentinelService struct {
}

func (s *sentinelService) Name() string {
	return "microsoft.securityinsights"
}

// Scope returns the service scope - Sentinel is a global service
func (s *sentinelService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *sentinelService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// First, list all Log Analytics workspaces to find Sentinel-enabled workspaces
		workspacesClient, err := armoperationalinsights.NewWorkspacesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create workspaces client: %w", err)
		}

		workspacesPager := workspacesClient.NewListPager(nil)
		var workspaces []struct {
			resourceGroup string
			name          string
		}

		for workspacesPager.More() {
			page, err := workspacesPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of workspaces: %w", err)
			}

			for _, workspace := range page.Value {
				if workspace.ID == nil || workspace.Name == nil {
					continue
				}

				// Extract resource group from workspace ID
				// ID format: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.OperationalInsights/workspaces/{name}
				parts := strings.Split(*workspace.ID, "/")
				var resourceGroup string
				for i, part := range parts {
					if part == "resourceGroups" && i+1 < len(parts) {
						resourceGroup = parts[i+1]
						break
					}
				}

				if resourceGroup != "" {
					workspaces = append(workspaces, struct {
						resourceGroup string
						name          string
					}{
						resourceGroup: resourceGroup,
						name:          *workspace.Name,
					})
				}
			}
		}

		// Now get Sentinel alert rules from each workspace
		alertRulesClient, err := armsecurityinsights.NewAlertRulesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create alert rules client: %w", err)
		}

		for _, ws := range workspaces {
			// List alert rules for this workspace
			alertRulesPager := alertRulesClient.NewListPager(ws.resourceGroup, ws.name, nil)

			for alertRulesPager.More() {
				page, err := alertRulesPager.NextPage(ctx.GetContext())
				if err != nil {
					// Workspace might not have Sentinel enabled, skip it
					continue
				}

				for _, alertRule := range page.Value {
					basicRule := alertRule.GetAlertRule()
					if basicRule.ID == nil || basicRule.Name == nil || basicRule.Type == nil {
						continue
					}

					status := providers.ResourceStatusActive
					meta := structToMap(alertRule)

					allResources = append(allResources, providers.Resource{
						Id:          *basicRule.ID,
						Name:        *basicRule.Name,
						Type:        *basicRule.Type,
						Region:      "global",
						Tags:        map[string][]string{},
						Meta:        meta,
						Status:      status,
						CreatedAt:   time.Now(),
						Arn:         *basicRule.ID,
						ServiceName: s.Name(),
					})
				}
			}

			// Also get incidents for recommendations
			incidentsClient, err := armsecurityinsights.NewIncidentsClient(subID, cred, getAzureAuditOpts(ctx))
			if err != nil {
				continue
			}

			incidentsPager := incidentsClient.NewListPager(ws.resourceGroup, ws.name, nil)
			for incidentsPager.More() {
				page, err := incidentsPager.NextPage(ctx.GetContext())
				if err != nil {
					continue
				}

				for _, incident := range page.Value {
					if incident.ID == nil || incident.Name == nil || incident.Type == nil {
						continue
					}

					status := providers.ResourceStatusActive
					if incident.Properties != nil && incident.Properties.Status != nil {
						if *incident.Properties.Status == armsecurityinsights.IncidentStatusClosed {
							status = providers.ResourceStatusInactive
						}
					}

					allResources = append(allResources, providers.Resource{
						Id:          *incident.ID,
						Name:        *incident.Name,
						Type:        *incident.Type,
						Region:      "global",
						Tags:        map[string][]string{},
						Meta:        structToMap(incident.Properties),
						Status:      status,
						CreatedAt:   time.Now(),
						Arn:         *incident.ID,
						ServiceName: s.Name(),
					})
				}
			}
		}
	}

	return allResources, nil
}

func (s *sentinelService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *sentinelService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check if alert rules are disabled
		if enabled, ok := properties["enabled"].(bool); ok && !enabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "azure_sentinel_alert_rule_disabled",
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

		// Check for missing automation rules
		if properties["automationRules"] == nil || properties["automationRules"] == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_sentinel_no_automation_rules",
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

		// Check for incidents without owner
		if severity, ok := properties["severity"].(string); ok {
			if owner, ok := properties["owner"].(map[string]interface{}); !ok || owner == nil {
				incidentSeverity := providers.RecommendationSeverityMedium
				if strings.ToLower(severity) == "high" || strings.ToLower(severity) == "critical" {
					incidentSeverity = providers.RecommendationSeverityHigh
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_sentinel_incident_no_owner",
					Severity:            incidentSeverity,
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

		// Check for stale incidents (open for too long)
		if status, ok := properties["status"].(string); ok && strings.ToLower(status) == "active" {
			if createdTime, ok := properties["createdTimeUtc"].(string); ok {
				created, err := time.Parse(time.RFC3339, createdTime)
				if err == nil {
					daysSinceCreation := time.Since(created).Hours() / 24
					if daysSinceCreation > 30 {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "azure_sentinel_stale_incident",
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
		}

		// Check for missing data connectors
		if properties["dataConnectors"] == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_sentinel_no_data_connectors",
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

		// Check for missing threat intelligence connectors
		if properties["threatIntelligence"] == nil || properties["threatIntelligence"] == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "azure_sentinel_no_threat_intel",
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

	return recommendations, nil
}

func (s *sentinelService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *sentinelService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	_, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// All Sentinel commands require manual configuration due to complexity
	// and the need for workspace context
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command: %s requires manual configuration in Microsoft Sentinel", command.Command),
	}, errors.ErrUnsupported
}

func (s *sentinelService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *sentinelService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
