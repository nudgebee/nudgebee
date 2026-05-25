package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
)

const ServiceNameAdvisor = "Advisor"

type advisorService struct{}

func (s *advisorService) Name() string {
	return ServiceNameAdvisor
}

func (s *advisorService) Scope() ServiceScope {
	return ServiceScopeSubscription
}

func (s *advisorService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *advisorService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (s *advisorService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("failed to get azure credentials for advisor", "error", err)
		return nil, err
	}

	var allRecommendations []providers.Recommendation

	// Azure supports comma-separated subscription IDs
	subscriptions := strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptions {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}

		client, err := armadvisor.NewRecommendationsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Error("failed to create advisor client", "subscription", subID, "error", err)
			continue
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("failed to list advisor recommendations", "subscription", subID, "error", err)
				break
			}

			for _, item := range page.Value {
				rec := mapAzureAdvisorRecommendation(item, subID)
				allRecommendations = append(allRecommendations, rec)
			}
		}
	}

	ctx.GetLogger().Info("fetched azure advisor recommendations", "count", len(allRecommendations))
	return allRecommendations, nil
}

func mapAzureAdvisorRecommendation(item *armadvisor.ResourceRecommendationBase, subscriptionID string) providers.Recommendation {
	props := item.Properties
	if props == nil {
		props = &armadvisor.RecommendationProperties{}
	}

	category := mapAdvisorCategory(props.Category)
	severity := mapAdvisorImpact(props.Impact)
	savings := extractAdvisorSavings(props.ExtendedProperties)

	// Override severity if there are cost savings
	if savings > 0 {
		severity = mapAdvisorSavingsToSeverity(savings)
	}

	description := ""
	if props.ShortDescription != nil && props.ShortDescription.Problem != nil {
		description = *props.ShortDescription.Problem
	}

	impactedField := ""
	if props.ImpactedField != nil {
		impactedField = *props.ImpactedField
	}

	impactedValue := ""
	if props.ImpactedValue != nil {
		impactedValue = *props.ImpactedValue
	}

	recommendationID := ""
	if item.Name != nil {
		recommendationID = *item.Name
	}

	resourceRegion := "global"
	if ext := props.ExtendedProperties; ext != nil {
		if regionVal, ok := ext["region"]; ok && regionVal != nil && *regionVal != "" {
			resourceRegion = *regionVal
		} else if locVal, ok := ext["location"]; ok && locVal != nil && *locVal != "" {
			resourceRegion = *locVal
		}
	}

	ruleName := fmt.Sprintf("azure_native_advisor_%s", sanitizeAdvisorCategory(props.Category))

	data := map[string]any{
		"source":            "azure",
		"description":       description,
		"impacted_field":    impactedField,
		"impacted_value":    impactedValue,
		"recommendation_id": recommendationID,
		"subscription_id":   subscriptionID,
	}

	if props.Category != nil {
		data["advisor_category"] = string(*props.Category)
	}
	if props.Impact != nil {
		data["impact"] = string(*props.Impact)
	}
	if props.PotentialBenefits != nil {
		data["potential_benefits"] = *props.PotentialBenefits
	}
	if props.LearnMoreLink != nil {
		data["learn_more_link"] = *props.LearnMoreLink
	}
	if props.RecommendationTypeID != nil {
		data["recommendation_type_id"] = *props.RecommendationTypeID
	}
	if props.ShortDescription != nil && props.ShortDescription.Solution != nil {
		data["solution"] = *props.ShortDescription.Solution
	}

	// Include extended properties (savings, VM size, etc.)
	if props.ExtendedProperties != nil {
		for k, v := range props.ExtendedProperties {
			if v != nil {
				data["ext_"+strings.ToLower(k)] = *v
			}
		}
	}

	if savings > 0 {
		data["estimated_monthly_savings"] = savings
	}

	// Extract the actual impacted resource's Azure resource ID from ResourceMetadata
	externalResourceId := ""
	if props.ResourceMetadata != nil && props.ResourceMetadata.ResourceID != nil && *props.ResourceMetadata.ResourceID != "" {
		externalResourceId = strings.ToLower(*props.ResourceMetadata.ResourceID)
		data["resource_path"] = externalResourceId
	}

	action := providers.RecommendationActionModify

	return providers.Recommendation{
		CategoryName:        category,
		RuleName:            ruleName,
		Severity:            severity,
		Savings:             savings,
		Action:              action,
		Data:                data,
		ResourceServiceName: ServiceNameAdvisor,
		ResourceId:          recommendationID,
		ResourceType:        impactedField,
		ResourceRegion:      resourceRegion,
		ExternalResourceId:  externalResourceId,
	}
}

func mapAdvisorCategory(category *armadvisor.Category) providers.RecommendationCategory {
	if category == nil {
		return providers.RecommendationCategoryConfiguration
	}
	switch *category {
	case armadvisor.CategoryCost:
		return providers.RecommendationCategoryRightSizing
	case armadvisor.CategorySecurity:
		return providers.RecommendationCategorySecurity
	case armadvisor.CategoryHighAvailability:
		return providers.RecommendationCategoryConfiguration
	case armadvisor.CategoryPerformance:
		return providers.RecommendationCategoryConfiguration
	case armadvisor.CategoryOperationalExcellence:
		return providers.RecommendationCategoryConfiguration
	default:
		return providers.RecommendationCategoryConfiguration
	}
}

func mapAdvisorImpact(impact *armadvisor.Impact) providers.RecommendationSeverity {
	if impact == nil {
		return providers.RecommendationSeverityMedium
	}
	switch *impact {
	case armadvisor.ImpactHigh:
		return providers.RecommendationSeverityHigh
	case armadvisor.ImpactMedium:
		return providers.RecommendationSeverityMedium
	case armadvisor.ImpactLow:
		return providers.RecommendationSeverityLow
	default:
		return providers.RecommendationSeverityMedium
	}
}

func mapAdvisorSavingsToSeverity(monthlySavings float64) providers.RecommendationSeverity {
	switch {
	case monthlySavings >= 100:
		return providers.RecommendationSeverityHigh
	case monthlySavings >= 20:
		return providers.RecommendationSeverityMedium
	default:
		return providers.RecommendationSeverityLow
	}
}

func extractAdvisorSavings(extendedProperties map[string]*string) float64 {
	if extendedProperties == nil {
		return 0
	}
	// Azure Advisor provides annualSavingsAmount in extended properties
	if v, ok := extendedProperties["annualSavingsAmount"]; ok && v != nil {
		if annual, err := strconv.ParseFloat(*v, 64); err == nil && annual > 0 {
			return annual / 12.0 // Convert to monthly
		}
	}
	if v, ok := extendedProperties["savingsAmount"]; ok && v != nil {
		if s, err := strconv.ParseFloat(*v, 64); err == nil && s > 0 {
			return s
		}
	}
	return 0
}

func sanitizeAdvisorCategory(category *armadvisor.Category) string {
	if category == nil {
		return "general"
	}
	s := string(*category)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func (s *advisorService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (s *advisorService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (s *advisorService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (s *advisorService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
