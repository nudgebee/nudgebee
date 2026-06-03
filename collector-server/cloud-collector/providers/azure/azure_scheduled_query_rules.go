package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type scheduledQueryRulesService struct {
}

func (s *scheduledQueryRulesService) Name() string {
	return "microsoft.insights/scheduledqueryrules"
}

// Scope returns the service scope - this is a regional service
func (s *scheduledQueryRulesService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *scheduledQueryRulesService) GetResources(
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
		client, err := armmonitor.NewScheduledQueryRulesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create scheduled query rules client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, rule := range page.Value {
				status := providers.ResourceStatusUnknown
				// Scheduled query rules have an enabled property
				if rule.Properties != nil && rule.Properties.Enabled != nil {
					if *rule.Properties.Enabled {
						status = providers.ResourceStatusActive
					} else {
						status = providers.ResourceStatusInactive
					}
				}

				// Extract created time if available from tags or set to zero time
				createdAt := getCreatedAtFromTags(rule.Tags)

				// Determine the region/location
				location := ""
				if rule.Location != nil {
					location = *rule.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *rule.ID,
					Name:        *rule.Name,
					Type:        *rule.Type,
					Region:      location,
					Tags:        toAzureTags(rule.Tags),
					Meta:        structToMap(rule),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *rule.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *scheduledQueryRulesService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Scheduled query rules themselves don't have metrics to query - they are configurations that monitor log data
	// However, we can query metrics from the resources that the rules are monitoring
	// Use the common Azure Monitor metrics function
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *scheduledQueryRulesService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if rule is disabled
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if enabled, ok := props["enabled"].(bool); ok && !enabled {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_scheduled_query_rule_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Scheduled query rule is disabled and not monitoring log data; enable it to ensure proper monitoring"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check if rule has no action groups configured
			if actions, ok := props["actions"].(map[string]interface{}); ok {
				if actionGroups, ok := actions["actionGroups"].([]interface{}); !ok || len(actionGroups) == 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_scheduled_query_rule_no_action_group",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "Scheduled query rule has no action groups configured; add action groups to receive notifications when alerts trigger"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if rule has auto-mitigation disabled
			if autoMitigate, ok := props["autoMitigate"].(bool); ok && !autoMitigate {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_scheduled_query_rule_auto_mitigation_disabled",
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

			// Check evaluation frequency
			if criteria, ok := props["criteria"].(map[string]interface{}); ok {
				if allOf, ok := criteria["allOf"].([]interface{}); ok && len(allOf) > 0 {
					// Check if query is well-formed
					for _, condition := range allOf {
						if condMap, ok := condition.(map[string]interface{}); ok {
							if query, ok := condMap["query"].(string); ok && strings.TrimSpace(query) == "" {
								allRecommendations = append(allRecommendations, providers.Recommendation{
									CategoryName:        providers.RecommendationCategoryConfiguration,
									RuleName:            "azure_scheduled_query_rule_empty_query",
									Severity:            providers.RecommendationSeverityHigh,
									Savings:             0,
									Data:                map[string]any{"reason": "Scheduled query rule has an empty query; configure a valid query to monitor log data"},
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

			// Check if rule has no scopes configured
			if scopes, ok := props["scopes"].([]interface{}); !ok || len(scopes) == 0 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_scheduled_query_rule_no_scopes",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Scheduled query rule has no scopes configured; add scopes to define which resources to monitor"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check if rule has tags for organization
		if len(resource.Tags) == 0 {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_scheduled_query_rule_missing_tags",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"reason": "Scheduled query rule has no tags; add tags for better organization, cost tracking, and resource management"},
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

func (s *scheduledQueryRulesService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
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

	client, err := armmonitor.NewScheduledQueryRulesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create scheduled query rules client: %w", err)
	}

	// Extract resource group and rule name from resource ID
	var resourceGroup, ruleName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "scheduledQueryRules" && i+1 < len(parts) {
			ruleName = parts[i+1]
		}
	}

	if resourceGroup == "" || ruleName == "" {
		return fmt.Errorf("failed to extract resource group or rule name from resource ID: %s", recommendation.ResourceId)
	}

	// Get the current rule configuration
	ruleResp, err := client.Get(ctx.GetContext(), resourceGroup, ruleName, nil)
	if err != nil {
		return fmt.Errorf("failed to get scheduled query rule: %w", err)
	}

	rule := ruleResp.ScheduledQueryRuleResource
	if rule.Properties == nil {
		return fmt.Errorf("rule properties are nil")
	}

	// Apply the recommendation based on the rule name
	modified := false
	switch recommendation.RuleName {
	case "azure_scheduled_query_rule_disabled":
		// Enable the rule
		enabled := true
		rule.Properties.Enabled = &enabled
		modified = true
		logger.Info("applying recommendation: enabling scheduled query rule", "ruleName", ruleName)

	case "azure_scheduled_query_rule_auto_mitigation_disabled":
		// Enable auto-mitigation
		autoMitigate := true
		rule.Properties.AutoMitigate = &autoMitigate
		modified = true
		logger.Info("applying recommendation: enabling auto-mitigation", "ruleName", ruleName)

	case "azure_scheduled_query_rule_no_action_group":
		// Cannot auto-fix this - requires user to specify which action group to add
		return fmt.Errorf("cannot auto-apply recommendation '%s': action group must be specified manually", recommendation.RuleName)

	case "azure_scheduled_query_rule_empty_query":
		// Cannot auto-fix this - requires user to specify query
		return fmt.Errorf("cannot auto-apply recommendation '%s': query must be specified manually", recommendation.RuleName)

	case "azure_scheduled_query_rule_no_scopes":
		// Cannot auto-fix this - requires user to specify scopes
		return fmt.Errorf("cannot auto-apply recommendation '%s': scopes must be specified manually", recommendation.RuleName)

	case "azure_scheduled_query_rule_missing_tags":
		// Cannot auto-fix this - requires user to specify tags
		return fmt.Errorf("cannot auto-apply recommendation '%s': tags must be specified manually", recommendation.RuleName)

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}

	if !modified {
		return fmt.Errorf("no changes were made for recommendation: %s", recommendation.RuleName)
	}

	// Update the rule
	_, err = client.CreateOrUpdate(ctx.GetContext(), resourceGroup, ruleName, rule, nil)
	if err != nil {
		return fmt.Errorf("failed to update scheduled query rule: %w", err)
	}

	logger.Info("successfully applied recommendation", "ruleName", recommendation.RuleName, "queryRuleName", ruleName)
	return nil
}

func (s *scheduledQueryRulesService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	client, err := armmonitor.NewScheduledQueryRulesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create scheduled query rules client: %v", err),
		}, err
	}

	// Extract resource group and rule name from resource ID
	var resourceGroup, ruleName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "scheduledQueryRules" && i+1 < len(parts) {
			ruleName = parts[i+1]
		}
	}

	if resourceGroup == "" || ruleName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or rule name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	// Get the current rule configuration
	ruleResp, err := client.Get(ctx.GetContext(), resourceGroup, ruleName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get scheduled query rule: %v", err),
		}, err
	}

	rule := ruleResp.ScheduledQueryRuleResource
	if rule.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "rule properties are nil",
		}, fmt.Errorf("rule properties are nil")
	}

	// Execute the command
	switch command.Command {
	case "enable":
		enabled := true
		rule.Properties.Enabled = &enabled
		logger.Info("executing command: enabling scheduled query rule", "ruleName", ruleName)

	case "disable":
		enabled := false
		rule.Properties.Enabled = &enabled
		logger.Info("executing command: disabling scheduled query rule", "ruleName", ruleName)

	case "enable_auto_mitigation":
		autoMitigate := true
		rule.Properties.AutoMitigate = &autoMitigate
		logger.Info("executing command: enabling auto-mitigation", "ruleName", ruleName)

	case "disable_auto_mitigation":
		autoMitigate := false
		rule.Properties.AutoMitigate = &autoMitigate
		logger.Info("executing command: disabling auto-mitigation", "ruleName", ruleName)

	case "update_evaluation_frequency":
		if freq, ok := command.Args["evaluation_frequency"].(string); ok {
			rule.Properties.EvaluationFrequency = &freq
			logger.Info("executing command: updating evaluation frequency", "ruleName", ruleName, "frequency", freq)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "evaluation_frequency argument is required and must be a string (e.g., 'PT1M', 'PT5M')",
			}, fmt.Errorf("missing or invalid evaluation_frequency argument")
		}

	case "update_window_size":
		if windowSize, ok := command.Args["window_size"].(string); ok {
			rule.Properties.WindowSize = &windowSize
			logger.Info("executing command: updating window size", "ruleName", ruleName, "windowSize", windowSize)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "window_size argument is required and must be a string (e.g., 'PT5M', 'PT15M')",
			}, fmt.Errorf("missing or invalid window_size argument")
		}

	case "update_severity":
		if severity, ok := command.Args["severity"].(float64); ok {
			severityValue := armmonitor.AlertSeverity(int64(severity))
			rule.Properties.Severity = &severityValue
			logger.Info("executing command: updating severity", "ruleName", ruleName, "severity", severityValue)
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

	// Update the rule
	_, err = client.CreateOrUpdate(ctx.GetContext(), resourceGroup, ruleName, rule, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update scheduled query rule: %v", err),
		}, err
	}

	logger.Info("successfully executed command", "command", command.Command, "ruleName", ruleName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on scheduled query rule '%s'", command.Command, ruleName),
	}, nil
}

func (s *scheduledQueryRulesService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Scheduled query rules write to Azure Monitor Activity Log
	// The resourceId itself can be used to query rule firing history via Azure Monitor
	return resourceId, nil
}

func (s *scheduledQueryRulesService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "scheduledqueryrule",
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
		if actions, ok := props["actions"].(map[string]interface{}); ok {
			if actionGroups, ok := actions["actionGroups"].([]interface{}); ok {
				for _, actionGroup := range actionGroups {
					if actionGroupStr, ok := actionGroup.(string); ok {
						app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
							Id: providers.ServiceApplicationId{
								Name:      actionGroupStr,
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
