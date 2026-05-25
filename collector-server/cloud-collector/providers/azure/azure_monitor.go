package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type monitorService struct {
}

func (s *monitorService) Name() string {
	return "microsoft.insights"
}

// Scope returns the service scope - Monitor is a global service
func (s *monitorService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *monitorService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Note: Classic Alert Rules API (2016-03-01) has been deprecated by Azure
		// The armmonitor.NewAlertRulesClient uses this deprecated API version
		// We skip fetching classic alert rules to avoid 404 InvalidResourceType errors
		// Classic alerts have been replaced by Metric Alerts and Log Alerts (both fetched below)

		// Get Action Groups
		actionGroupsClient, err := armmonitor.NewActionGroupsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create action groups client: %w", err)
		}

		actionGroupsPager := actionGroupsClient.NewListBySubscriptionIDPager(nil)
		for actionGroupsPager.More() {
			page, err := actionGroupsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of action groups: %w", err)
			}

			for _, actionGroup := range page.Value {
				if actionGroup.ID == nil || actionGroup.Name == nil || actionGroup.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				if actionGroup.Properties != nil && actionGroup.Properties.Enabled != nil && !*actionGroup.Properties.Enabled {
					status = providers.ResourceStatusInactive
				}

				meta := structToMap(actionGroup.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *actionGroup.ID,
					Name:        *actionGroup.Name,
					Type:        *actionGroup.Type,
					Region:      normalizeAzureRegion(*actionGroup.Location),
					Tags:        toAzureTags(actionGroup.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *actionGroup.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Log Profiles (Activity Log settings)
		logProfilesClient, err := armmonitor.NewLogProfilesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create log profiles client: %w", err)
		}

		logProfilesPager := logProfilesClient.NewListPager(nil)
		for logProfilesPager.More() {
			page, err := logProfilesPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of log profiles: %w", err)
			}

			for _, logProfile := range page.Value {
				if logProfile.ID == nil || logProfile.Name == nil || logProfile.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				meta := structToMap(logProfile.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *logProfile.ID,
					Name:        *logProfile.Name,
					Type:        *logProfile.Type,
					Region:      "global", // Log profiles are subscription-level
					Tags:        toAzureTags(logProfile.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *logProfile.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Metric Alerts
		metricAlertsClient, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create metric alerts client: %w", err)
		}

		metricAlertsPager := metricAlertsClient.NewListBySubscriptionPager(nil)
		for metricAlertsPager.More() {
			page, err := metricAlertsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of metric alerts: %w", err)
			}

			for _, metricAlert := range page.Value {
				if metricAlert.ID == nil || metricAlert.Name == nil || metricAlert.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				if metricAlert.Properties != nil && metricAlert.Properties.Enabled != nil && !*metricAlert.Properties.Enabled {
					status = providers.ResourceStatusInactive
				}

				meta := structToMap(metricAlert.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *metricAlert.ID,
					Name:        *metricAlert.Name,
					Type:        *metricAlert.Type,
					Region:      normalizeAzureRegion(*metricAlert.Location),
					Tags:        toAzureTags(metricAlert.Tags),
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *metricAlert.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *monitorService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *monitorService) ListMetrics(_ providers.CloudProviderContext, _ providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	return listAzureMonitorMetrics(request)
}

func (s *monitorService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	// Count action groups and alert rules
	actionGroupCount := 0
	alertRuleCount := 0
	disabledAlertCount := 0
	disabledActionGroupCount := 0

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for disabled alert rules
		if strings.Contains(strings.ToLower(resource.Type), "alertrule") || strings.Contains(strings.ToLower(resource.Type), "metricalert") {
			alertRuleCount++
			if resource.Status == providers.ResourceStatusInactive {
				disabledAlertCount++
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_monitor_alert_rule_disabled",
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

			// Check for alert rules without action groups
			if actions, ok := properties["actions"].([]interface{}); ok && len(actions) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_monitor_alert_no_action_group",
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

		// Check for disabled action groups
		if strings.Contains(strings.ToLower(resource.Type), "actiongroup") {
			actionGroupCount++
			if resource.Status == providers.ResourceStatusInactive {
				disabledActionGroupCount++
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_monitor_action_group_disabled",
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

			// Check for action groups with no receivers
			hasReceivers := false
			if emailReceivers, ok := properties["emailReceivers"].([]interface{}); ok && len(emailReceivers) > 0 {
				hasReceivers = true
			}
			if smsReceivers, ok := properties["smsReceivers"].([]interface{}); ok && len(smsReceivers) > 0 {
				hasReceivers = true
			}
			if webhookReceivers, ok := properties["webhookReceivers"].([]interface{}); ok && len(webhookReceivers) > 0 {
				hasReceivers = true
			}

			if !hasReceivers {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_monitor_action_group_no_receivers",
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
	}

	// Check if there are no action groups at all
	if actionGroupCount == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "azure_monitor_no_action_groups",
			Severity:            providers.RecommendationSeverityHigh,
			Savings:             0,
			Data:                map[string]any{},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: s.Name(),
			ResourceId:          "subscription-level",
			ResourceType:        "Microsoft.Insights/actionGroups",
			ResourceRegion:      "global",
		})
	}

	// Check if there are no alert rules at all
	if alertRuleCount == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "azure_monitor_no_alert_rules",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             0,
			Data:                map[string]any{},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: s.Name(),
			ResourceId:          "subscription-level",
			ResourceType:        "Microsoft.Insights/alertRules",
			ResourceRegion:      "global",
		})
	}

	return recommendations, nil
}

func (s *monitorService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *monitorService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	subID := strings.Split(session.SubscriptionID, ",")[0]

	switch command.Command {
	case "azure_monitor_alert_rule_disabled":
		// Enable the alert rule
		logger.Info("attempting to enable alert rule", "resourceId", command.ResourceId)

		// Determine if it's a classic alert rule or metric alert
		if strings.Contains(command.ResourceId, "/alertRules/") {
			// Classic alert rule - would need specific implementation
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "enabling classic alert rules requires API update operation",
			}, errors.ErrUnsupported
		} else if strings.Contains(command.ResourceId, "/metricAlerts/") {
			// Metric alert - would need specific implementation
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "enabling metric alerts requires API update operation",
			}, errors.ErrUnsupported
		}

	case "azure_monitor_action_group_disabled":
		// Enable the action group
		logger.Info("attempting to enable action group", "resourceId", command.ResourceId)
		_ = subID
		_ = cred

		return providers.ApplyCommandResponse{
			Success: false,
			Message: "enabling action groups requires API update operation",
		}, errors.ErrUnsupported

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	return providers.ApplyCommandResponse{
		Success: false,
		Message: "command not implemented",
	}, errors.ErrUnsupported
}

func (s *monitorService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *monitorService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
