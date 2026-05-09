package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type metricAlertsService struct {
}

func (s *metricAlertsService) Name() string {
	return "microsoft.insights/metricalerts"
}

// Scope returns the service scope - this is a regional service
func (s *metricAlertsService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *metricAlertsService) GetResources(
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
		client, err := armmonitor.NewMetricAlertsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create metric alerts client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, alert := range page.Value {
				status := providers.ResourceStatusUnknown
				// Metric alerts don't have a provisioning state, but they have an enabled property
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

func (s *metricAlertsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Metric alerts themselves don't have metrics to query - they are configurations that monitor other resources
	// However, we can query metrics from the resources that the alerts are monitoring
	// Use the common Azure Monitor metrics function
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *metricAlertsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
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
					RuleName:            "azure_metric_alert_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Metric alert is disabled and not monitoring resources; enable it to ensure proper monitoring"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if alert has no action groups configured
			if actions, ok := props["actions"].([]interface{}); !ok || len(actions) == 0 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_metric_alert_no_action_group",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Metric alert has no action groups configured; add action groups to receive notifications when alerts trigger"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if alert has auto-mitigation disabled (if supported)
			if autoMitigate, ok := props["autoMitigate"].(bool); ok && !autoMitigate {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_metric_alert_auto_mitigation_disabled",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"reason": "Auto-mitigation is disabled; consider enabling it to automatically resolve alerts when conditions return to normal"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if alert scope is too broad (monitoring all resources)
			if scopes, ok := props["scopes"].([]interface{}); ok {
				if len(scopes) > 20 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_metric_alert_broad_scope",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             0,
						Data:                map[string]any{"reason": "Alert monitors many resources; consider splitting into multiple alerts for better granularity and manageability", "scopeCount": len(scopes)},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check evaluation frequency and window size
			if evaluationFrequency, ok := props["evaluationFrequency"].(string); ok {
				if windowSize, ok := props["windowSize"].(string); ok {
					// Parse durations to check if evaluation frequency > window size (inefficient)
					evalDur, evalErr := time.ParseDuration(strings.Replace(evaluationFrequency, "PT", "", 1))
					windowDur, windowErr := time.ParseDuration(strings.Replace(windowSize, "PT", "", 1))
					if evalErr == nil && windowErr == nil && evalDur > windowDur {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "azure_metric_alert_inefficient_evaluation",
							Severity:            providers.RecommendationSeverityLow,
							Savings:             0,
							Data:                map[string]any{"reason": "Evaluation frequency exceeds window size; adjust the values for better efficiency and accurate monitoring"},
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

		// Check if alert has tags for organization
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_metric_alert_missing_tags",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"reason": "Metric alert has no tags; add tags for better organization, cost tracking, and resource management"},
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

func (s *metricAlertsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
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

	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create metric alerts client: %w", err)
	}

	// Extract resource group and alert name from resource ID
	var resourceGroup, alertName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "metricAlerts" && i+1 < len(parts) {
			alertName = parts[i+1]
		}
	}

	if resourceGroup == "" || alertName == "" {
		return fmt.Errorf("failed to extract resource group or alert name from resource ID: %s", recommendation.ResourceId)
	}

	// Get the current alert configuration
	alertResp, err := client.Get(ctx.GetContext(), resourceGroup, alertName, nil)
	if err != nil {
		return fmt.Errorf("failed to get metric alert: %w", err)
	}

	alert := alertResp.MetricAlertResource
	if alert.Properties == nil {
		return fmt.Errorf("alert properties are nil")
	}

	// Apply the recommendation based on the rule name
	modified := false
	switch recommendation.RuleName {
	case "azure_metric_alert_disabled":
		// Enable the alert
		enabled := true
		alert.Properties.Enabled = &enabled
		modified = true
		logger.Info("applying recommendation: enabling metric alert", "alertName", alertName)

	case "azure_metric_alert_auto_mitigation_disabled":
		// Enable auto-mitigation
		autoMitigate := true
		alert.Properties.AutoMitigate = &autoMitigate
		modified = true
		logger.Info("applying recommendation: enabling auto-mitigation", "alertName", alertName)

	case "azure_metric_alert_no_action_group":
		// Cannot auto-fix this - requires user to specify which action group to add
		return fmt.Errorf("cannot auto-apply recommendation '%s': action group must be specified manually", recommendation.RuleName)

	case "azure_metric_alert_broad_scope":
		// Cannot auto-fix this - requires user decision on how to split
		return fmt.Errorf("cannot auto-apply recommendation '%s': requires manual review and splitting", recommendation.RuleName)

	case "azure_metric_alert_inefficient_evaluation":
		// Cannot auto-fix this - requires user decision on proper values
		return fmt.Errorf("cannot auto-apply recommendation '%s': requires manual adjustment of evaluation frequency and window size", recommendation.RuleName)

	case "azure_metric_alert_missing_tags":
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
		return fmt.Errorf("failed to update metric alert: %w", err)
	}

	logger.Info("successfully applied recommendation", "ruleName", recommendation.RuleName, "alertName", alertName)
	return nil
}

func (s *metricAlertsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create metric alerts client: %v", err),
		}, err
	}

	// Extract resource group and alert name from resource ID
	var resourceGroup, alertName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "metricAlerts" && i+1 < len(parts) {
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
			Message: fmt.Sprintf("failed to get metric alert: %v", err),
		}, err
	}

	alert := alertResp.MetricAlertResource
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
		logger.Info("executing command: enabling metric alert", "alertName", alertName)

	case "disable":
		enabled := false
		alert.Properties.Enabled = &enabled
		logger.Info("executing command: disabling metric alert", "alertName", alertName)

	case "enable_auto_mitigation":
		autoMitigate := true
		alert.Properties.AutoMitigate = &autoMitigate
		logger.Info("executing command: enabling auto-mitigation", "alertName", alertName)

	case "disable_auto_mitigation":
		autoMitigate := false
		alert.Properties.AutoMitigate = &autoMitigate
		logger.Info("executing command: disabling auto-mitigation", "alertName", alertName)

	case "update_evaluation_frequency":
		if freq, ok := command.Args["evaluation_frequency"].(string); ok {
			alert.Properties.EvaluationFrequency = &freq
			logger.Info("executing command: updating evaluation frequency", "alertName", alertName, "frequency", freq)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "evaluation_frequency argument is required and must be a string (e.g., 'PT1M', 'PT5M')",
			}, fmt.Errorf("missing or invalid evaluation_frequency argument")
		}

	case "update_window_size":
		if windowSize, ok := command.Args["window_size"].(string); ok {
			alert.Properties.WindowSize = &windowSize
			logger.Info("executing command: updating window size", "alertName", alertName, "windowSize", windowSize)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "window_size argument is required and must be a string (e.g., 'PT5M', 'PT15M')",
			}, fmt.Errorf("missing or invalid window_size argument")
		}

	case "update_severity":
		if severity, ok := command.Args["severity"].(float64); ok {
			severityInt := int32(severity)
			alert.Properties.Severity = &severityInt
			logger.Info("executing command: updating severity", "alertName", alertName, "severity", severityInt)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "severity argument is required and must be a number (0-4)",
			}, fmt.Errorf("missing or invalid severity argument")
		}

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s. Supported commands: enable, disable, enable_auto_mitigation, disable_auto_mitigation, update_evaluation_frequency, update_window_size, update_severity", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	// Update the alert
	_, err = client.CreateOrUpdate(ctx.GetContext(), resourceGroup, alertName, alert, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update metric alert: %v", err),
		}, err
	}

	logger.Info("successfully executed command", "command", command.Command, "alertName", alertName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on metric alert '%s'", command.Command, alertName),
	}, nil
}

func (s *metricAlertsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Metric alerts write to Azure Monitor Activity Log
	// The resourceId itself can be used to query alert firing history via Azure Monitor
	return resourceId, nil
}

func (s *metricAlertsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "metricalert",
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
		if actions, ok := props["actions"].([]interface{}); ok {
			for _, action := range actions {
				if actionMap, ok := action.(map[string]interface{}); ok {
					if actionGroupID, ok := actionMap["actionGroupId"].(string); ok {
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

	return app, nil
}
