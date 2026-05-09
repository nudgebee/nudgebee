package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapAdvisorCategory(t *testing.T) {
	tests := []struct {
		name     string
		category *armadvisor.Category
		expected providers.RecommendationCategory
	}{
		{"nil", nil, providers.RecommendationCategoryConfiguration},
		{"Cost", ptr(armadvisor.CategoryCost), providers.RecommendationCategoryRightSizing},
		{"Security", ptr(armadvisor.CategorySecurity), providers.RecommendationCategorySecurity},
		{"HighAvailability", ptr(armadvisor.CategoryHighAvailability), providers.RecommendationCategoryConfiguration},
		{"Performance", ptr(armadvisor.CategoryPerformance), providers.RecommendationCategoryConfiguration},
		{"OperationalExcellence", ptr(armadvisor.CategoryOperationalExcellence), providers.RecommendationCategoryConfiguration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapAdvisorCategory(tt.category)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapAdvisorImpact(t *testing.T) {
	tests := []struct {
		name     string
		impact   *armadvisor.Impact
		expected providers.RecommendationSeverity
	}{
		{"nil", nil, providers.RecommendationSeverityMedium},
		{"High", ptrImpact(armadvisor.ImpactHigh), providers.RecommendationSeverityHigh},
		{"Medium", ptrImpact(armadvisor.ImpactMedium), providers.RecommendationSeverityMedium},
		{"Low", ptrImpact(armadvisor.ImpactLow), providers.RecommendationSeverityLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapAdvisorImpact(tt.impact)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapAdvisorSavingsToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		savings  float64
		expected providers.RecommendationSeverity
	}{
		{"zero", 0, providers.RecommendationSeverityLow},
		{"small ($5)", 5, providers.RecommendationSeverityLow},
		{"medium ($20)", 20, providers.RecommendationSeverityMedium},
		{"medium ($50)", 50, providers.RecommendationSeverityMedium},
		{"high ($100)", 100, providers.RecommendationSeverityHigh},
		{"high ($500)", 500, providers.RecommendationSeverityHigh},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapAdvisorSavingsToSeverity(tt.savings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAdvisorSavings(t *testing.T) {
	tests := []struct {
		name     string
		ext      map[string]*string
		expected float64
	}{
		{"nil map", nil, 0},
		{"empty map", map[string]*string{}, 0},
		{
			"annualSavingsAmount",
			map[string]*string{"annualSavingsAmount": ptrStr("1200.00")},
			100.0, // 1200/12
		},
		{
			"savingsAmount fallback",
			map[string]*string{"savingsAmount": ptrStr("42.50")},
			42.50,
		},
		{
			"annualSavingsAmount takes priority",
			map[string]*string{
				"annualSavingsAmount": ptrStr("600.00"),
				"savingsAmount":       ptrStr("99.00"),
			},
			50.0, // 600/12
		},
		{
			"zero annual falls through to savingsAmount",
			map[string]*string{
				"annualSavingsAmount": ptrStr("0"),
				"savingsAmount":       ptrStr("25.00"),
			},
			25.0,
		},
		{
			"invalid value",
			map[string]*string{"annualSavingsAmount": ptrStr("not-a-number")},
			0,
		},
		{
			"nil value pointer",
			map[string]*string{"annualSavingsAmount": nil},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAdvisorSavings(tt.ext)
			assert.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

func TestSanitizeAdvisorCategory(t *testing.T) {
	tests := []struct {
		name     string
		category *armadvisor.Category
		expected string
	}{
		{"nil", nil, "general"},
		{"Cost", ptr(armadvisor.CategoryCost), "cost"},
		{"Security", ptr(armadvisor.CategorySecurity), "security"},
		{"HighAvailability", ptr(armadvisor.CategoryHighAvailability), "highavailability"},
		{"OperationalExcellence", ptr(armadvisor.CategoryOperationalExcellence), "operationalexcellence"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAdvisorCategory(tt.category)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapAzureAdvisorRecommendation(t *testing.T) {
	t.Run("full cost recommendation with savings", func(t *testing.T) {
		category := armadvisor.CategoryCost
		impact := armadvisor.ImpactHigh
		item := &armadvisor.ResourceRecommendationBase{
			ID:   ptrStr("/subscriptions/sub-1/providers/Microsoft.Advisor/recommendations/rec-123"),
			Name: ptrStr("rec-123"),
			Properties: &armadvisor.RecommendationProperties{
				Category: &category,
				Impact:   &impact,
				ShortDescription: &armadvisor.ShortDescription{
					Problem:  ptrStr("Underutilized VM"),
					Solution: ptrStr("Resize or shut down"),
				},
				ImpactedField:        ptrStr("Microsoft.Compute/virtualMachines"),
				ImpactedValue:        ptrStr("my-vm"),
				PotentialBenefits:    ptrStr("Save $50/month"),
				LearnMoreLink:        ptrStr("https://aka.ms/advisor"),
				RecommendationTypeID: ptrStr("type-abc"),
				ExtendedProperties: map[string]*string{
					"annualSavingsAmount": ptrStr("600.00"),
					"savingsCurrency":     ptrStr("USD"),
					"currentSku":          ptrStr("Standard_D4s_v3"),
					"targetSku":           ptrStr("Standard_D2s_v3"),
					"region":              ptrStr("eastus"),
				},
			},
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-1")

		assert.Equal(t, "azure_native_advisor_cost", rec.RuleName)
		assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity) // $50/mo = Medium
		assert.InDelta(t, 50.0, rec.Savings, 0.01)
		assert.Equal(t, providers.RecommendationActionModify, rec.Action)
		assert.Equal(t, ServiceNameAdvisor, rec.ResourceServiceName)
		assert.Equal(t, "rec-123", rec.ResourceId)
		assert.Equal(t, "Microsoft.Compute/virtualMachines", rec.ResourceType)
		assert.Equal(t, "eastus", rec.ResourceRegion)

		assert.Equal(t, "azure", rec.Data["source"])
		assert.Equal(t, "Underutilized VM", rec.Data["description"])
		assert.Equal(t, "Resize or shut down", rec.Data["solution"])
		assert.Equal(t, "sub-1", rec.Data["subscription_id"])
		assert.Equal(t, "Cost", rec.Data["advisor_category"])
		assert.Equal(t, "High", rec.Data["impact"])
		assert.Equal(t, "Save $50/month", rec.Data["potential_benefits"])
		assert.Equal(t, 50.0, rec.Data["estimated_monthly_savings"])
		assert.Equal(t, "Standard_D4s_v3", rec.Data["ext_currentsku"])
	})

	t.Run("security recommendation without savings", func(t *testing.T) {
		category := armadvisor.CategorySecurity
		impact := armadvisor.ImpactMedium
		item := &armadvisor.ResourceRecommendationBase{
			Name: ptrStr("rec-456"),
			Properties: &armadvisor.RecommendationProperties{
				Category: &category,
				Impact:   &impact,
				ShortDescription: &armadvisor.ShortDescription{
					Problem: ptrStr("Enable MFA"),
				},
				ImpactedField: ptrStr("Microsoft.Authorization/roleAssignments"),
				ImpactedValue: ptrStr("user@example.com"),
			},
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-2")

		assert.Equal(t, "azure_native_advisor_security", rec.RuleName)
		assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
		assert.Equal(t, 0.0, rec.Savings)
		assert.Equal(t, "global", rec.ResourceRegion)
		assert.Equal(t, "rec-456", rec.ResourceId)
	})

	t.Run("nil properties", func(t *testing.T) {
		item := &armadvisor.ResourceRecommendationBase{
			Name:       ptrStr("rec-789"),
			Properties: nil,
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-3")

		assert.Equal(t, "azure_native_advisor_general", rec.RuleName)
		assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
		assert.Equal(t, 0.0, rec.Savings)
		assert.Equal(t, "global", rec.ResourceRegion)
		assert.Equal(t, "rec-789", rec.ResourceId)
	})

	t.Run("recommendation with ResourceMetadata sets ExternalResourceId", func(t *testing.T) {
		category := armadvisor.CategoryCost
		impact := armadvisor.ImpactHigh
		resourcePath := "/subscriptions/sub-1/resourceGroups/rg-test/providers/Microsoft.Compute/virtualMachines/my-vm"
		item := &armadvisor.ResourceRecommendationBase{
			Name: ptrStr("rec-meta-1"),
			Properties: &armadvisor.RecommendationProperties{
				Category:      &category,
				Impact:        &impact,
				ImpactedField: ptrStr("Microsoft.Compute/virtualMachines"),
				ImpactedValue: ptrStr("my-vm"),
				ResourceMetadata: &armadvisor.ResourceMetadata{
					ResourceID: &resourcePath,
				},
				ExtendedProperties: map[string]*string{
					"annualSavingsAmount": ptrStr("1200.00"),
					"region":              ptrStr("eastus"),
				},
			},
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-1")

		assert.Equal(t, strings.ToLower(resourcePath), rec.ExternalResourceId)
		assert.Equal(t, strings.ToLower(resourcePath), rec.Data["resource_path"])
		// ResourceId should still be the recommendation UUID for dedup
		assert.Equal(t, "rec-meta-1", rec.ResourceId)
	})

	t.Run("recommendation without ResourceMetadata has empty ExternalResourceId", func(t *testing.T) {
		category := armadvisor.CategorySecurity
		impact := armadvisor.ImpactMedium
		item := &armadvisor.ResourceRecommendationBase{
			Name: ptrStr("rec-no-meta"),
			Properties: &armadvisor.RecommendationProperties{
				Category: &category,
				Impact:   &impact,
			},
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-2")

		assert.Empty(t, rec.ExternalResourceId)
		assert.Nil(t, rec.Data["resource_path"])
	})

	t.Run("region from location extended property", func(t *testing.T) {
		category := armadvisor.CategoryHighAvailability
		item := &armadvisor.ResourceRecommendationBase{
			Name: ptrStr("rec-loc"),
			Properties: &armadvisor.RecommendationProperties{
				Category: &category,
				ExtendedProperties: map[string]*string{
					"location": ptrStr("westeurope"),
				},
			},
		}

		rec := mapAzureAdvisorRecommendation(item, "sub-4")
		assert.Equal(t, "westeurope", rec.ResourceRegion)
	})
}

func TestAdvisorServiceName(t *testing.T) {
	svc := &advisorService{}
	assert.Equal(t, "Advisor", svc.Name())
}

func TestAdvisorServiceScope(t *testing.T) {
	svc := &advisorService{}
	assert.Equal(t, ServiceScopeSubscription, svc.Scope())
}

// Integration test — requires Azure credentials
func TestAzureAdvisorIntegration(t *testing.T) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	tenantID := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	clientSecret := os.Getenv("AZURE_CLIENT_SECRET")

	if subscriptionID == "" || tenantID == "" || clientID == "" || clientSecret == "" {
		t.Skip("skipping integration test: set AZURE_SUBSCRIPTION_ID, AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET")
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	require.NoError(t, err)

	client, err := armadvisor.NewRecommendationsClient(subscriptionID, cred, nil)
	require.NoError(t, err)

	var allRecs []providers.Recommendation
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			t.Logf("warning: failed to list recommendations: %v", err)
			break
		}
		for _, item := range page.Value {
			rec := mapAzureAdvisorRecommendation(item, subscriptionID)
			allRecs = append(allRecs, rec)
		}
	}

	t.Logf("fetched %d Azure Advisor recommendations", len(allRecs))

	for i, rec := range allRecs {
		assert.Equal(t, "azure", rec.Data["source"])
		assert.NotEmpty(t, rec.RuleName, "recommendation %d should have a rule name", i)
		assert.Equal(t, ServiceNameAdvisor, rec.ResourceServiceName)

		t.Logf("  [%d] rule=%s severity=%s savings=%.2f region=%s type=%s desc=%s",
			i, rec.RuleName, rec.Severity, rec.Savings, rec.ResourceRegion, rec.ResourceType, rec.Data["description"])
	}
}

// Helper functions
func ptr(c armadvisor.Category) *armadvisor.Category {
	return &c
}

func ptrImpact(i armadvisor.Impact) *armadvisor.Impact {
	return &i
}

func ptrStr(s string) *string {
	return &s
}
