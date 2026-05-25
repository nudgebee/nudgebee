package gcloud

import (
	"context"
	"fmt"
	"os"
	"testing"

	"nudgebee/collector/cloud/providers"

	recommender "cloud.google.com/go/recommender/apiv1"
	recommenderpb "cloud.google.com/go/recommender/apiv1/recommenderpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestSanitizeRecommenderID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"idle resource", "google.compute.instance.IdleResourceRecommender", "compute_instance_idle_resource"},
		{"machine type", "google.compute.instance.MachineTypeRecommender", "compute_instance_machine_type"},
		{"iam policy", "google.iam.policy.Recommender", "iam_policy"},
		{"cloudsql security", "google.cloudsql.instance.SecurityRecommender", "cloudsql_instance_security"},
		{"container diagnosis", "google.container.DiagnosisRecommender", "container_diagnosis"},
		{"disk idle", "google.compute.disk.IdleResourceRecommender", "compute_disk_idle_resource"},
		{"no prefix or suffix", "SomeCustom", "some_custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeRecommenderID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractRecommendationID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full path", "projects/my-project/locations/us-central1/recommenders/google.compute.instance.IdleResourceRecommender/recommendations/abc-123", "abc-123"},
		{"simple id", "abc-123", "abc-123"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRecommendationID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapGCPPriorityToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		priority recommenderpb.Recommendation_Priority
		expected providers.RecommendationSeverity
	}{
		{"P1", recommenderpb.Recommendation_P1, providers.RecommendationSeverityHigh},
		{"P2", recommenderpb.Recommendation_P2, providers.RecommendationSeverityHigh},
		{"P3", recommenderpb.Recommendation_P3, providers.RecommendationSeverityMedium},
		{"P4", recommenderpb.Recommendation_P4, providers.RecommendationSeverityLow},
		{"unspecified", recommenderpb.Recommendation_PRIORITY_UNSPECIFIED, providers.RecommendationSeverityMedium},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapGCPPriorityToSeverity(tt.priority)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapGCPSavingsToSeverity(t *testing.T) {
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
			result := mapGCPSavingsToSeverity(tt.savings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapGCPImpactCategory(t *testing.T) {
	tests := []struct {
		name     string
		impact   recommenderpb.Impact_Category
		fallback providers.RecommendationCategory
		expected providers.RecommendationCategory
	}{
		{"COST", recommenderpb.Impact_COST, providers.RecommendationCategoryConfiguration, providers.RecommendationCategoryRightSizing},
		{"SECURITY", recommenderpb.Impact_SECURITY, providers.RecommendationCategoryConfiguration, providers.RecommendationCategorySecurity},
		{"PERFORMANCE", recommenderpb.Impact_PERFORMANCE, providers.RecommendationCategorySecurity, providers.RecommendationCategoryConfiguration},
		{"RELIABILITY", recommenderpb.Impact_RELIABILITY, providers.RecommendationCategorySecurity, providers.RecommendationCategoryConfiguration},
		{"UNSPECIFIED fallback", recommenderpb.Impact_CATEGORY_UNSPECIFIED, providers.RecommendationCategorySecurity, providers.RecommendationCategorySecurity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapGCPImpactCategory(tt.impact, tt.fallback)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapGCPRecommenderAction(t *testing.T) {
	tests := []struct {
		name          string
		recommenderID string
		expected      providers.RecommendationAction
	}{
		{"idle resource", "google.compute.instance.IdleResourceRecommender", providers.RecommendationActionDelete},
		{"shutdown", "google.compute.instance.ShutdownRecommender", providers.RecommendationActionDelete},
		{"machine type", "google.compute.instance.MachineTypeRecommender", providers.RecommendationActionModify},
		{"iam policy", "google.iam.policy.Recommender", providers.RecommendationActionModify},
		{"security", "google.cloudsql.instance.SecurityRecommender", providers.RecommendationActionModify},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapGCPRecommenderAction(tt.recommenderID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractGCPSavings(t *testing.T) {
	tests := []struct {
		name     string
		rec      *recommenderpb.Recommendation
		expected float64
	}{
		{
			"nil impact",
			&recommenderpb.Recommendation{},
			0,
		},
		{
			"no cost projection",
			&recommenderpb.Recommendation{
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_SECURITY,
				},
			},
			0,
		},
		{
			"negative cost = savings (30 day)",
			&recommenderpb.Recommendation{
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{
								CurrencyCode: "USD",
								Units:        -50,
								Nanos:        0,
							},
							Duration: &durationpb.Duration{
								Seconds: 2592000, // 30 days
							},
						},
					},
				},
			},
			50.0,
		},
		{
			"negative cost with nanos",
			&recommenderpb.Recommendation{
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{
								CurrencyCode: "USD",
								Units:        -10,
								Nanos:        -500000000, // -0.50
							},
							Duration: &durationpb.Duration{
								Seconds: 2592000,
							},
						},
					},
				},
			},
			10.5,
		},
		{
			"positive cost = no savings",
			&recommenderpb.Recommendation{
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{
								CurrencyCode: "USD",
								Units:        25,
								Nanos:        0,
							},
							Duration: &durationpb.Duration{
								Seconds: 2592000,
							},
						},
					},
				},
			},
			0,
		},
		{
			"nil duration returns absolute value",
			&recommenderpb.Recommendation{
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{
								CurrencyCode: "USD",
								Units:        -75,
								Nanos:        0,
							},
						},
					},
				},
			},
			75.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractGCPSavings(tt.rec)
			assert.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

func TestMapGCPRecommendation(t *testing.T) {
	t.Run("full recommendation with cost savings", func(t *testing.T) {
		rec := &recommenderpb.Recommendation{
			Name:               "projects/my-project/locations/us-central1/recommenders/google.compute.instance.IdleResourceRecommender/recommendations/rec-123",
			Description:        "This VM instance is idle. Consider stopping or deleting it.",
			RecommenderSubtype: "STOP_VM",
			Priority:           recommenderpb.Recommendation_P2,
			PrimaryImpact: &recommenderpb.Impact{
				Category: recommenderpb.Impact_COST,
				Projection: &recommenderpb.Impact_CostProjection{
					CostProjection: &recommenderpb.CostProjection{
						Cost: &money.Money{
							CurrencyCode: "USD",
							Units:        -150,
							Nanos:        0,
						},
						Duration: &durationpb.Duration{
							Seconds: 2592000,
						},
					},
				},
			},
			StateInfo: &recommenderpb.RecommendationStateInfo{
				State: recommenderpb.RecommendationStateInfo_ACTIVE,
			},
		}

		mapped := mapGCPRecommendation(rec, "google.compute.instance.IdleResourceRecommender", providers.RecommendationCategoryRightSizing, "us-central1")

		assert.Equal(t, "gcp_native_compute_instance_idle_resource", mapped.RuleName)
		assert.Equal(t, providers.RecommendationCategoryRightSizing, mapped.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityHigh, mapped.Severity)
		assert.InDelta(t, 150.0, mapped.Savings, 0.01)
		assert.Equal(t, providers.RecommendationActionDelete, mapped.Action)
		assert.Equal(t, ServiceNameRecommender, mapped.ResourceServiceName)
		assert.Equal(t, "rec-123", mapped.ResourceId)
		assert.Equal(t, "us-central1", mapped.ResourceRegion)
		assert.Equal(t, "gcp", mapped.Data["source"])
		assert.Equal(t, "This VM instance is idle. Consider stopping or deleting it.", mapped.Data["description"])
	})

	t.Run("security recommendation without cost", func(t *testing.T) {
		rec := &recommenderpb.Recommendation{
			Name:               "projects/my-project/locations/global/recommenders/google.iam.policy.Recommender/recommendations/rec-456",
			Description:        "Role binding has excessive permissions.",
			RecommenderSubtype: "REMOVE_ROLE",
			Priority:           recommenderpb.Recommendation_P3,
			PrimaryImpact: &recommenderpb.Impact{
				Category: recommenderpb.Impact_SECURITY,
			},
			StateInfo: &recommenderpb.RecommendationStateInfo{
				State: recommenderpb.RecommendationStateInfo_ACTIVE,
			},
		}

		mapped := mapGCPRecommendation(rec, "google.iam.policy.Recommender", providers.RecommendationCategorySecurity, "global")

		assert.Equal(t, "gcp_native_iam_policy", mapped.RuleName)
		assert.Equal(t, providers.RecommendationCategorySecurity, mapped.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityMedium, mapped.Severity) // P3 = Medium
		assert.Equal(t, 0.0, mapped.Savings)
		assert.Equal(t, providers.RecommendationActionModify, mapped.Action)
		assert.Equal(t, "rec-456", mapped.ResourceId)
		assert.Equal(t, "global", mapped.ResourceRegion)
	})

	t.Run("recommendation with no impact", func(t *testing.T) {
		rec := &recommenderpb.Recommendation{
			Name:     "projects/p/locations/l/recommenders/r/recommendations/rec-789",
			Priority: recommenderpb.Recommendation_PRIORITY_UNSPECIFIED,
		}

		mapped := mapGCPRecommendation(rec, "google.container.DiagnosisRecommender", providers.RecommendationCategoryConfiguration, "us-east1")

		assert.Equal(t, "gcp_native_container_diagnosis", mapped.RuleName)
		assert.Equal(t, providers.RecommendationCategoryConfiguration, mapped.CategoryName)
		assert.Equal(t, providers.RecommendationSeverityMedium, mapped.Severity)
		assert.Equal(t, 0.0, mapped.Savings)
	})
}

func TestIsGCPPermissionOrNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		expected bool
	}{
		{"permission denied", "rpc error: code = PermissionDenied", true},
		{"PERMISSION_DENIED", "rpc error: code = PERMISSION_DENIED desc = ...", true},
		{"not found", "rpc error: code = NotFound", true},
		{"403", "googleapi: Error 403: Forbidden", true},
		{"404", "googleapi: Error 404: Not Found", true},
		{"api not enabled", "googleapi: Error 400: The project my-project has not enabled BigQuery., invalid", true},
		{"api not used", "rpc error: code = PermissionDenied desc = Cloud Functions API has not been used in project my-project before or it is disabled.", true},
		{"api disabled", "googleapi: Error 400: Cloud Run Admin API is disabled. Enable it by visiting ... then retry.", true},
		{"other error", "connection refused", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGCPPermissionOrNotFoundError(fmt.Errorf("%s", tt.err))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGCPRecommenderIntegration(t *testing.T) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("skipping integration test: set GCP_PROJECT_ID (and GOOGLE_APPLICATION_CREDENTIALS or gcloud auth) to run")
	}

	// Use the recommender client directly with ADC (application default credentials)
	client, err := recommender.NewClient(context.Background(), option.WithQuotaProject(projectID))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := providers.NewCloudProviderContext(context.Background())

	var recommendations []providers.Recommendation
	for _, rt := range recommenderTypes {
		for _, location := range recommenderLocations {
			parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/%s", projectID, location, rt.id)

			recs, fetchErr := fetchRecommendations(ctx, client, parent, rt.id, rt.category, location)
			if fetchErr != nil {
				if isGCPPermissionOrNotFoundError(fetchErr) {
					continue
				}
				t.Logf("warning: failed to list %s in %s: %v", rt.id, location, fetchErr)
				continue
			}
			recommendations = append(recommendations, recs...)
		}
	}

	t.Logf("fetched %d GCP Recommender recommendations", len(recommendations))

	for i, rec := range recommendations {
		assert.Equal(t, "gcp", rec.Data["source"])
		assert.NotEmpty(t, rec.RuleName, "recommendation %d should have a rule name", i)
		assert.Equal(t, ServiceNameRecommender, rec.ResourceServiceName)

		t.Logf("  [%d] rule=%s severity=%s savings=%.2f region=%s type=%s desc=%s",
			i, rec.RuleName, rec.Severity, rec.Savings, rec.ResourceRegion, rec.ResourceType, rec.Data["description"])
	}
}
