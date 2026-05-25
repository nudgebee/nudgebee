package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
)

type policyService struct {
}

func (s *policyService) Name() string {
	return "microsoft.authorization/policyassignments"
}

// Scope returns the service scope - Policy is a global service
func (s *policyService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *policyService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get Policy Assignments
		assignmentsClient, err := armpolicy.NewAssignmentsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create policy assignments client: %w", err)
		}

		assignmentsPager := assignmentsClient.NewListPager(nil)
		for assignmentsPager.More() {
			page, err := assignmentsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of policy assignments: %w", err)
			}

			for _, assignment := range page.Value {
				if assignment == nil || assignment.ID == nil || assignment.Name == nil || assignment.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				if assignment.Properties != nil && assignment.Properties.EnforcementMode != nil {
					if *assignment.Properties.EnforcementMode == armpolicy.EnforcementModeDoNotEnforce {
						status = providers.ResourceStatusInactive
					}
				}

				meta := structToMap(assignment.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				// Add location info if available
				location := "global"
				if assignment.Location != nil {
					location = normalizeAzureRegion(*assignment.Location)
				}

				createdAt := time.Now()
				if assignment.SystemData != nil && assignment.SystemData.CreatedAt != nil {
					createdAt = *assignment.SystemData.CreatedAt
				}

				allResources = append(allResources, providers.Resource{
					Id:          *assignment.ID,
					Name:        *assignment.Name,
					Type:        *assignment.Type,
					Region:      location,
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *assignment.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Policy Definitions (built-in and custom)
		definitionsClient, err := armpolicy.NewDefinitionsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create policy definitions client: %w", err)
		}

		definitionsPager := definitionsClient.NewListPager(nil)
		for definitionsPager.More() {
			page, err := definitionsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of policy definitions: %w", err)
			}

			for _, definition := range page.Value {
				if definition == nil || definition.ID == nil || definition.Name == nil || definition.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				meta := structToMap(definition.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *definition.ID,
					Name:        *definition.Name,
					Type:        *definition.Type,
					Region:      "global",
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *definition.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get Policy Set Definitions (Initiatives)
		setDefinitionsClient, err := armpolicy.NewSetDefinitionsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create policy set definitions client: %w", err)
		}

		setDefinitionsPager := setDefinitionsClient.NewListPager(nil)
		for setDefinitionsPager.More() {
			page, err := setDefinitionsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of policy set definitions: %w", err)
			}

			for _, setDefinition := range page.Value {
				if setDefinition == nil || setDefinition.ID == nil || setDefinition.Name == nil || setDefinition.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				meta := structToMap(setDefinition.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				allResources = append(allResources, providers.Resource{
					Id:          *setDefinition.ID,
					Name:        *setDefinition.Name,
					Type:        *setDefinition.Type,
					Region:      "global",
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *setDefinition.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *policyService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *policyService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	assignmentCount := 0
	definitionCount := 0
	disabledAssignmentCount := 0

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for policy assignments with enforcement disabled
		if strings.Contains(strings.ToLower(resource.Type), "policyassignment") {
			assignmentCount++

			if resource.Status == providers.ResourceStatusInactive {
				disabledAssignmentCount++
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_policy_assignment_not_enforced",
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

			// Check for policy assignments without description
			if description, ok := properties["description"].(string); !ok || description == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "azure_policy_assignment_no_description",
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

		// Check for custom policy definitions without metadata
		if strings.Contains(strings.ToLower(resource.Type), "policydefinition") {
			definitionCount++

			// Check if it's a custom definition (not built-in)
			if policyType, ok := properties["policyType"].(string); ok && policyType == "Custom" {
				// Check for missing metadata
				if metadata, ok := properties["metadata"].(map[string]interface{}); !ok || len(metadata) == 0 {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_policy_custom_definition_no_metadata",
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

				// Check for missing category in metadata
				if metadata, ok := properties["metadata"].(map[string]interface{}); ok {
					if category, ok := metadata["category"].(string); !ok || category == "" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "azure_policy_definition_no_category",
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

	// Check if there are no policy assignments at all
	if assignmentCount == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategorySecurity,
			RuleName:            "azure_policy_no_assignments",
			Severity:            providers.RecommendationSeverityHigh,
			Savings:             0,
			Data:                map[string]any{},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: s.Name(),
			ResourceId:          "subscription-level",
			ResourceType:        "Microsoft.Authorization/policyAssignments",
			ResourceRegion:      "global",
		})
	}

	return recommendations, nil
}

func (s *policyService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *policyService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Policy changes require careful review and should not be auto-applied
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Policy: %s requires manual review for compliance and security reasons", command.Command),
	}, errors.ErrUnsupported
}

func (s *policyService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *policyService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
