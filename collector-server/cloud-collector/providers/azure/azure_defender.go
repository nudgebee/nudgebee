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

		// Security assessments are intentionally NOT persisted as cloud_resourses.
		// Defender-for-Containers emits one assessment per (image SHA, vulnerable
		// package), which for image-heavy subscriptions explodes to tens of thousands
		// of rows carrying hundreds of MB of meta (observed: one subscription at 23k
		// rows / 752MB). The metrics ETL later loads every row's meta into memory,
		// OOM-killing the collector. Assessments are findings, not infrastructure —
		// Phase 2 will surface the unhealthy ones as aggregated security
		// recommendations (mirroring aws_securityhub.go, which consumes GetFindings in
		// GetRecommendations rather than storing per-finding resources). Only Defender
		// pricing tiers are stored here.
	}

	return allResources, nil
}

func (s *defenderService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *defenderService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	// Free-tier recommendations are derived from the stored pricing resources.
	for _, resource := range existingResources {
		properties := resource.Meta

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
	}

	// Unhealthy security assessments are fetched live (Phase 2 of #34103) rather
	// than read from resources — they are no longer persisted as cloud_resourses.
	// This mirrors aws_securityhub.go, which consumes findings in GetRecommendations.
	assessmentRecs, err := s.getAssessmentRecommendations(ctx, account)
	if err != nil {
		// Don't fail the whole sync on assessment errors; free-tier recs still apply.
		ctx.GetLogger().Warn("failed to fetch defender assessment recommendations", "error", err)
	} else {
		recommendations = append(recommendations, assessmentRecs...)
	}

	return recommendations, nil
}

// getAssessmentRecommendations lists Defender for Cloud security assessments
// across all subscriptions and returns one recommendation per Unhealthy
// assessment. Recommendations are grouped by assessment definition via RuleName
// (azure_defender_assessment_<definitionId>) and linked to the assessed resource
// via ExternalResourceId, mirroring aws_securityhub.go.
func (s *defenderService) getAssessmentRecommendations(ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	assessmentsClient, err := armsecurity.NewAssessmentsClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create assessments client: %w", err)
	}

	var recommendations []providers.Recommendation

	for _, subID := range strings.Split(session.SubscriptionID, ",") {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		scope := fmt.Sprintf("subscriptions/%s", subID)
		pager := assessmentsClient.NewListPager(scope, nil)

		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				// A transient failure on one subscription must not discard the
				// assessments already collected from other subscriptions in this
				// run (the caller drops everything on a returned error, which would
				// archive all assessment recs). Log and move to the next subscription.
				ctx.GetLogger().Warn("failed to list defender assessments for subscription",
					"subscription", subID, "error", err)
				break
			}

			for _, assessment := range page.Value {
				if assessment.ID == nil || assessment.Name == nil ||
					assessment.Properties == nil || assessment.Properties.Status == nil ||
					assessment.Properties.Status.Code == nil {
					continue
				}

				// Only unhealthy assessments are actionable.
				if *assessment.Properties.Status.Code != armsecurity.AssessmentStatusCodeUnhealthy {
					continue
				}

				props := assessment.Properties

				displayName := *assessment.Name
				if props.DisplayName != nil && *props.DisplayName != "" {
					displayName = *props.DisplayName
				}

				severity := providers.RecommendationSeverityMedium
				if props.Metadata != nil && props.Metadata.Severity != nil {
					switch *props.Metadata.Severity {
					case armsecurity.SeverityHigh:
						severity = providers.RecommendationSeverityHigh
					case armsecurity.SeverityLow:
						severity = providers.RecommendationSeverityLow
					default:
						severity = providers.RecommendationSeverityMedium
					}
				}

				// Prefer the SDK-provided assessed resource ID; fall back to
				// stripping the assessment suffix off the assessment ID.
				assessedID := assessedResourceID(props.ResourceDetails)
				if assessedID == "" {
					assessedID = strings.ToLower(stripAssessmentSuffix(*assessment.ID))
				}

				data := map[string]any{
					"reason":         "Review and remediate the security assessment in Microsoft Defender for Cloud",
					"displayName":    displayName,
					"assessmentId":   strings.ToLower(*assessment.Name),
					"resourceId":     *assessment.ID,
					"statusCode":     string(*props.Status.Code),
					"subscriptionId": subID,
					"resourcePath":   assessedID,
				}
				if props.Status.Cause != nil {
					data["statusCause"] = *props.Status.Cause
				}
				if props.Status.Description != nil {
					data["statusDescription"] = *props.Status.Description
				}
				if props.Metadata != nil && props.Metadata.RemediationDescription != nil {
					data["remediation"] = *props.Metadata.RemediationDescription
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_defender_assessment_" + strings.ToLower(*assessment.Name),
					Severity:            severity,
					Savings:             0,
					Action:              providers.RecommendationActionModify,
					Data:                data,
					ResourceServiceName: s.Name(),
					ResourceId:          assessedID,
					ResourceType:        azureResourceTypeFromID(assessedID),
					ResourceRegion:      "global",
					ExternalResourceId:  assessedID,
				})
			}
		}
	}

	return recommendations, nil
}

// assessedResourceID extracts the assessed resource's Azure ID from the
// assessment's ResourceDetails (Azure-hosted resources only), lowercased to
// match the external_resource_id storage convention.
func assessedResourceID(details armsecurity.ResourceDetailsClassification) string {
	if azureDetails, ok := details.(*armsecurity.AzureResourceDetails); ok {
		if azureDetails.ID != nil {
			return strings.ToLower(*azureDetails.ID)
		}
	}
	return ""
}

// stripAssessmentSuffix removes the trailing
// "/providers/microsoft.security/assessments/<name>" segment from an assessment
// ID, yielding the impacted resource (or subscription) path.
func stripAssessmentSuffix(assessmentID string) string {
	idx := strings.Index(strings.ToLower(assessmentID), "/providers/microsoft.security/assessments/")
	if idx == -1 {
		return assessmentID
	}
	return assessmentID[:idx]
}

// azureResourceTypeFromID returns the "<provider>/<resourceType>" of an Azure
// resource ID (e.g. "microsoft.storage/storageaccounts"), or "subscription" for
// a subscription-scoped ID with no resource provider segment.
func azureResourceTypeFromID(resourceID string) string {
	lower := strings.ToLower(resourceID)
	idx := strings.LastIndex(lower, "/providers/")
	if idx == -1 {
		return "subscription"
	}
	parts := strings.Split(strings.Trim(lower[idx+len("/providers/"):], "/"), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return "subscription"
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

	// Unhealthy-assessment recommendations use a per-definition rule name
	// (azure_defender_assessment_<definitionId>) and require manual remediation
	// in Microsoft Defender for Cloud.
	if strings.HasPrefix(command.Command, "azure_defender_assessment_") {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("cannot auto-apply command: %s requires manual remediation in Microsoft Defender for Cloud", command.Command),
		}, errors.ErrUnsupported
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
