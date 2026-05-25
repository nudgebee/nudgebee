package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type activityLogAlertsService struct {
}

func (s *activityLogAlertsService) Name() string {
	return "microsoft.insights/activitylogalerts"
}

// Scope returns the service scope - this is a global service
func (s *activityLogAlertsService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *activityLogAlertsService) GetResources(
	ctx providers.CloudProviderContext,
	account providers.Account,
	region string,
) ([]providers.Resource, error) {
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
		client, err := armmonitor.NewActivityLogAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create activity log alerts client: %w", err)
		}

		pager := client.NewListBySubscriptionIDPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, alert := range page.Value {
				status := providers.ResourceStatusUnknown
				// Activity log alerts have an enabled property
				if alert.Properties != nil && alert.Properties.Enabled != nil {
					if *alert.Properties.Enabled {
						status = providers.ResourceStatusActive
					} else {
						status = providers.ResourceStatusInactive
					}
				}

				// Extract created time if available from tags or set to zero time
				createdAt := getCreatedAtFromTags(alert.Tags)
				// Determine the region/location
				location := ""
				if alert.Location != nil {
					location = *alert.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *alert.ID,
					Name:        *alert.Name,
					Type:        *alert.Type,
					Region:      location,
					Tags:        toAzureTags(alert.Tags),
					Meta:        structToMap(alert),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *alert.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *activityLogAlertsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Activity log alerts themselves don't have metrics to query - they are configurations that monitor activity log events
	// However, we can query metrics from the resources that the alerts are monitoring
	// Use the common Azure Monitor metrics function
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *activityLogAlertsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if alert is disabled
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if enabled, ok := props["enabled"].(bool); ok && !enabled {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_activity_log_alert_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Activity log alert is disabled and not monitoring activity log events; enable it to ensure proper monitoring"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if alert has no action groups configured
			if actions, ok := props["actions"].(map[string]interface{}); ok {
				if actionGroups, ok := actions["actionGroups"].([]interface{}); !ok || len(actionGroups) == 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_activity_log_alert_no_action_group",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "Activity log alert has no action groups configured; add action groups to receive notifications when alerts trigger"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if alert has no conditions configured
			if condition, ok := props["condition"].(map[string]interface{}); ok {
				if allOf, ok := condition["allOf"].([]interface{}); !ok || len(allOf) == 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_activity_log_alert_no_conditions",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "Activity log alert has no conditions configured; add conditions to define when the alert should trigger"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				} else {
					// Check if conditions have empty values
					for _, cond := range allOf {
						if condMap, ok := cond.(map[string]interface{}); ok {
							if equals, ok := condMap["equals"].(string); ok && strings.TrimSpace(equals) == "" {
								allRecommendations = append(allRecommendations, providers.Recommendation{
									CategoryName:        providers.RecommendationCategoryConfiguration,
									RuleName:            "azure_activity_log_alert_empty_condition",
									Severity:            providers.RecommendationSeverityMedium,
									Savings:             0,
									Data:                map[string]any{"reason": "Activity log alert has a condition with an empty value; configure valid conditions for proper monitoring"},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								})
								break
							}
						}
					}
				}
			}

			// Check if alert scope is too broad (all subscriptions)
			if scopes, ok := props["scopes"].([]interface{}); ok {
				if len(scopes) > 10 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_activity_log_alert_broad_scope",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             0,
						Data:                map[string]any{"reason": "Alert monitors many scopes; consider splitting into multiple alerts for better granularity and manageability", "scopeCount": len(scopes)},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if alert has no scopes configured
			if scopes, ok := props["scopes"].([]interface{}); !ok || len(scopes) == 0 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_activity_log_alert_no_scopes",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Activity log alert has no scopes configured; add scopes to define which resources to monitor"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check if alert has tags for organization
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_activity_log_alert_missing_tags",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"reason": "Activity log alert has no tags; add tags for better organization, cost tracking, and resource management"},
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

func (s *activityLogAlertsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
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

	client, err := armmonitor.NewActivityLogAlertsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create activity log alerts client: %w", err)
	}

	// Extract resource group and alert name from resource ID
	var resourceGroup, alertName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "activityLogAlerts" && i+1 < len(parts) {
			alertName = parts[i+1]
		}
	}

	if resourceGroup == "" || alertName == "" {
		return fmt.Errorf("failed to extract resource group or alert name from resource ID: %s", recommendation.ResourceId)
	}

	// Get the current alert configuration
	alertResp, err := client.Get(ctx.GetContext(), resourceGroup, alertName, nil)
	if err != nil {
		return fmt.Errorf("failed to get activity log alert: %w", err)
	}

	alert := alertResp.ActivityLogAlertResource
	if alert.Properties == nil {
		return fmt.Errorf("alert properties are nil")
	}

	// Apply the recommendation based on the rule name
	modified := false
	switch recommendation.RuleName {
	case "azure_activity_log_alert_disabled":
		// Enable the alert
		enabled := true
		alert.Properties.Enabled = &enabled
		modified = true
		logger.Info("applying recommendation: enabling activity log alert", "alertName", alertName)

	case "azure_activity_log_alert_no_action_group":
		// Cannot auto-fix this - requires user to specify which action group to add
		return fmt.Errorf("cannot auto-apply recommendation '%s': action group must be specified manually", recommendation.RuleName)

	case "azure_activity_log_alert_no_conditions":
		// Cannot auto-fix this - requires user to specify conditions
		return fmt.Errorf("cannot auto-apply recommendation '%s': conditions must be specified manually", recommendation.RuleName)

	case "azure_activity_log_alert_empty_condition":
		// Cannot auto-fix this - requires user to specify valid conditions
		return fmt.Errorf("cannot auto-apply recommendation '%s': condition values must be specified manually", recommendation.RuleName)

	case "azure_activity_log_alert_broad_scope":
		// Cannot auto-fix this - requires user decision on how to split
		return fmt.Errorf("cannot auto-apply recommendation '%s': requires manual review and splitting", recommendation.RuleName)

	case "azure_activity_log_alert_no_scopes":
		// Cannot auto-fix this - requires user to specify scopes
		return fmt.Errorf("cannot auto-apply recommendation '%s': scopes must be specified manually", recommendation.RuleName)

	case "azure_activity_log_alert_missing_tags":
		// Cannot auto-fix this - requires user to specify tags
		return fmt.Errorf("cannot auto-apply recommendation '%s': tags must be specified manually", recommendation.RuleName)

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}

	if !modified {
		return fmt.Errorf("no changes were made for recommendation: %s", recommendation.RuleName)
	}

	// Update the alert
	_, err = client.CreateOrUpdate(ctx.GetContext(), resourceGroup, alertName, alert, nil)
	if err != nil {
		return fmt.Errorf("failed to update activity log alert: %w", err)
	}

	logger.Info("successfully applied recommendation", "ruleName", recommendation.RuleName, "alertName", alertName)
	return nil
}

func (s *activityLogAlertsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	client, err := armmonitor.NewActivityLogAlertsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create activity log alerts client: %v", err),
		}, err
	}

	// Extract resource group and alert name from resource ID
	var resourceGroup, alertName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "activityLogAlerts" && i+1 < len(parts) {
			alertName = parts[i+1]
		}
	}

	if resourceGroup == "" || alertName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or alert name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	// Get the current alert configuration
	alertResp, err := client.Get(ctx.GetContext(), resourceGroup, alertName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get activity log alert: %v", err),
		}, err
	}

	alert := alertResp.ActivityLogAlertResource
	if alert.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "alert properties are nil",
		}, fmt.Errorf("alert properties are nil")
	}

	// Execute the command
	switch command.Command {
	case "enable":
		enabled := true
		alert.Properties.Enabled = &enabled
		logger.Info("executing command: enabling activity log alert", "alertName", alertName)

	case "disable":
		enabled := false
		alert.Properties.Enabled = &enabled
		logger.Info("executing command: disabling activity log alert", "alertName", alertName)

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s. Supported commands: enable, disable", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	// Update the alert
	_, err = client.CreateOrUpdate(ctx.GetContext(), resourceGroup, alertName, alert, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update activity log alert: %v", err),
		}, err
	}

	logger.Info("successfully executed command", "command", command.Command, "alertName", alertName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on activity log alert '%s'", command.Command, alertName),
	}, nil
}

func (s *activityLogAlertsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Activity log alerts write to Azure Monitor Activity Log
	// The resourceId itself can be used to query alert firing history via Azure Monitor
	return resourceId, nil
}

func (s *activityLogAlertsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "activitylogalert",
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

	// Extract scopes (monitored resources) as downstreams
	if props, ok := resource.Meta["properties"].(map[string]interface{}); ok {
		if scopes, ok := props["scopes"].([]interface{}); ok {
			for _, scope := range scopes {
				if scopeStr, ok := scope.(string); ok {
					// Extract resource type from scope
					parts := strings.Split(scopeStr, "/providers/")
					resourceType := "unknown"
					if len(parts) > 1 {
						typeParts := strings.Split(parts[1], "/")
						if len(typeParts) >= 2 {
							resourceType = typeParts[0] + "/" + typeParts[1]
						}
					} else {
						// Could be a subscription-level scope
						if strings.Contains(scopeStr, "/subscriptions/") {
							resourceType = "Microsoft.Resources/subscriptions"
						}
					}

					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
						Id: providers.ServiceApplicationId{
							Name:      scopeStr,
							Kind:      resourceType,
							Namespace: resource.Region,
						},
					}.ToDownstreamLink())
				}
			}
		}

		// Extract action groups as downstreams
		if actions, ok := props["actions"].(map[string]interface{}); ok {
			if actionGroups, ok := actions["actionGroups"].([]interface{}); ok {
				for _, actionGroup := range actionGroups {
					if actionGroupMap, ok := actionGroup.(map[string]interface{}); ok {
						if actionGroupID, ok := actionGroupMap["actionGroupId"].(string); ok {
							app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
								Id: providers.ServiceApplicationId{
									Name:      actionGroupID,
									Kind:      "Microsoft.Insights/actionGroups",
									Namespace: resource.Region,
								},
							}.ToDownstreamLink())
						}
					}
				}
			}
		}
	}

	return app, nil
}
