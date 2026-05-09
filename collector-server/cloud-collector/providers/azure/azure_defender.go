package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity"
)

type defenderService struct {
}

func (s *defenderService) Name() string {
	return "microsoft.security/pricings"
}

// Scope returns the service scope - Defender is a global service
func (s *defenderService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *defenderService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get Defender for Cloud pricing tiers
		pricingsClient, err := armsecurity.NewPricingsClient(cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create pricings client: %w", err)
		}

		// The scope for pricing is the subscription
		scope := fmt.Sprintf("subscriptions/%s", subID)
		pricingsList, err := pricingsClient.List(ctx.GetContext(), scope, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get pricings: %w", err)
		}

		if pricingsList.Value != nil {
			for _, pricing := range pricingsList.Value {
				if pricing.ID == nil || pricing.Name == nil || pricing.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				if pricing.Properties != nil && pricing.Properties.PricingTier != nil {
					if *pricing.Properties.PricingTier == armsecurity.PricingTierFree {
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *pricing.ID,
					Name:        *pricing.Name,
					Type:        *pricing.Type,
					Region:      "global", // Defender for Cloud is global
					Tags:        map[string][]string{},
					Meta:        structToMap(pricing.Properties),
					Status:      status,
					CreatedAt:   time.Now(), // Defender doesn't provide creation time
					Arn:         *pricing.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Get security assessments
		assessmentsClient, err := armsecurity.NewAssessmentsClient(cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create assessments client: %w", err)
		}

		assessmentsPager := assessmentsClient.NewListPager(scope, nil)

		for assessmentsPager.More() {
			page, err := assessmentsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of assessments: %w", err)
			}

			for _, assessment := range page.Value {
				if assessment.ID == nil || assessment.Name == nil || assessment.Type == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if assessment.Properties != nil &&
					assessment.Properties.Status != nil &&
					assessment.Properties.Status.Code != nil {

					switch *assessment.Properties.Status.Code {
					case armsecurity.AssessmentStatusCodeHealthy:
						status = providers.ResourceStatusActive

					case armsecurity.AssessmentStatusCodeUnhealthy:
						status = providers.ResourceStatusInactive

					case armsecurity.AssessmentStatusCodeNotApplicable:
						status = providers.ResourceStatusUnknown

					default:
						status = providers.ResourceStatusUnknown
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *assessment.ID,
					Name:        *assessment.Name,
					Type:        *assessment.Type,
					Region:      "global",
					Tags:        map[string][]string{},
					Meta:        structToMap(assessment.Properties),
					Status:      status,
					CreatedAt:   time.Now(),
					Arn:         *assessment.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *defenderService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *defenderService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check if Defender plan is on Free tier
		if pricingTier, ok := properties["pricingTier"].(string); ok && strings.ToLower(pricingTier) == "free" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_defender_free_tier",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"reason":      "Upgrade to Standard tier to benefit from enhanced security features",
					"meta":        properties,
					"tags":        resource.Tags,
					"status":      resource.Status,
					"name":        resource.Name,
					"id":          resource.Id,
					"type":        resource.Type,
					"region":      resource.Region,
					"serviceName": resource.ServiceName,
					"arn":         resource.Arn,
					"createdAt":   resource.CreatedAt,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for unhealthy security assessments
		if statusData, ok := properties["status"].(map[string]interface{}); ok {
			if code, ok := statusData["code"].(string); ok && strings.ToLower(code) == "unhealthy" {
				severity := providers.RecommendationSeverityMedium

				// Check severity from assessment
				if displayName, ok := properties["displayName"].(string); ok {
					if strings.Contains(strings.ToLower(displayName), "critical") {
						severity = providers.RecommendationSeverityCritical
					} else if strings.Contains(strings.ToLower(displayName), "high") {
						severity = providers.RecommendationSeverityHigh
					}
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_defender_unhealthy_assessment",
					Severity:     severity,
					Savings:      0,
					Data: map[string]any{
						"assessmentStatus": statusData,
						"meta":             properties,
						"tags":             resource.Tags,
						"status":           resource.Status,
						"name":             resource.Name,
						"id":               resource.Id,
						"type":             resource.Type,
						"region":           resource.Region,
						"serviceName":      resource.ServiceName,
						"arn":              resource.Arn,
						"createdAt":        resource.CreatedAt,
						"reason":           "Review and remediate the security assessment in Azure Security Center",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for disabled auto-provisioning
		if autoProvision, ok := properties["autoProvision"].(string); ok && strings.ToLower(autoProvision) != "on" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_defender_auto_provision_disabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"reason":        "Enable auto-provisioning to ensure all resources are protected by Defender for Cloud",
					"meta":          properties,
					"tags":          resource.Tags,
					"status":        resource.Status,
					"name":          resource.Name,
					"id":            resource.Id,
					"type":          resource.Type,
					"region":        resource.Region,
					"serviceName":   resource.ServiceName,
					"arn":           resource.Arn,
					"createdAt":     resource.CreatedAt,
					"autoProvision": autoProvision,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for missing security contacts
		if properties["securityContactConfiguration"] == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_defender_no_security_contacts",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"reason":                       "Add security contacts to receive important security notifications",
					"meta":                         properties,
					"tags":                         resource.Tags,
					"status":                       resource.Status,
					"name":                         resource.Name,
					"id":                           resource.Id,
					"type":                         resource.Type,
					"region":                       resource.Region,
					"serviceName":                  resource.ServiceName,
					"arn":                          resource.Arn,
					"createdAt":                    resource.CreatedAt,
					"securityContactConfiguration": properties["securityContactConfiguration"],
					"autoProvision":                properties["autoProvision"],
					"assessmentStatus":             properties["assessmentStatus"],
					"displayName":                  properties["displayName"],
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

func (s *defenderService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *defenderService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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

	switch command.Command {
	case "azure_defender_free_tier":
		// Upgrade to Standard tier
		pricingsClient, err := armsecurity.NewPricingsClient(cred, getAzureAuditOpts(ctx))
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create pricings client: %v", err),
			}, err
		}

		// Extract pricing name from resource ID
		var pricingName string
		for i, part := range parts {
			if part == "pricings" && i+1 < len(parts) {
				pricingName = parts[i+1]
				break
			}
		}

		if pricingName == "" {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "failed to extract pricing name from resource ID",
			}, fmt.Errorf("invalid resource ID")
		}

		scope := fmt.Sprintf("subscriptions/%s", subscriptionID)
		standardTier := armsecurity.PricingTierStandard
		pricing := armsecurity.Pricing{
			Properties: &armsecurity.PricingProperties{
				PricingTier: &standardTier,
			},
		}

		_, err = pricingsClient.Update(ctx.GetContext(), scope, pricingName, pricing, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update pricing tier: %v", err),
			}, err
		}

		logger.Info("successfully upgraded Defender for Cloud to Standard tier", "pricingName", pricingName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully upgraded Defender for Cloud plan '%s' to Standard tier", pricingName),
		}, nil

	case "azure_defender_unhealthy_assessment",
		"azure_defender_auto_provision_disabled",
		"azure_defender_no_security_contacts":
		// These require manual remediation
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("cannot auto-apply command: %s requires manual remediation in Azure Security Center", command.Command),
		}, errors.ErrUnsupported

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *defenderService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (s *defenderService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
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
