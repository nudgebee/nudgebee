package gcloud

import (
	"context"
	"fmt"
	"os"
	"testing"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"

	recommender "cloud.google.com/go/recommender/apiv1"
	recommenderpb "cloud.google.com/go/recommender/apiv1/recommenderpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
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

func TestNormalizeGCPResourcePath(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		expected string
	}{
		{"https selfLink", "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instances/Demo-VM", "projects/-/zones/us-central1-a/instances/demo-vm"},
		{"operation resource", "//compute.googleapis.com/projects/p/zones/us-central1-a/instances/demo-vm", "projects/-/zones/us-central1-a/instances/demo-vm"},
		{"bare path", "projects/p/zones/us-central1-a/disks/disk-1", "projects/-/zones/us-central1-a/disks/disk-1"},
		// Same resource referenced by project number (Recommender) and project id
		// (selfLink) must land on the same key.
		{"project number", "//container.googleapis.com/projects/137143602736/locations/us-central1/clusters/dev", "projects/-/locations/us-central1/clusters/dev"},
		{"path ends at project", "projects/p", "projects/p"},
		{"no projects segment", "1020227973377891447", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGCPResourcePath(tt.ref))
		})
	}
}

// TestResolveRecommenderTarget proves resolution is resolve-first, not
// first-operation: the idle-disk shape leads with a create-snapshot operation
// whose snapshot is not a collected resource, and the disk in the second
// operation must still win.
func TestResolveRecommenderTarget(t *testing.T) {
	disk := providers.Resource{
		Id:          "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/disks/pvc-disk",
		Name:        "pvc-disk",
		Type:        "storage",
		Arn:         "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/disks/pvc-disk",
		ServiceName: ServiceNameDisk,
		Region:      "us-central1",
	}
	index := buildRecommenderResourceIndex([]providers.Resource{disk})

	rec := &recommenderpb.Recommendation{
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{{
				Operations: []*recommenderpb.Operation{
					{
						Action:       "add",
						ResourceType: "compute.googleapis.com/Snapshot",
						Resource:     "//compute.googleapis.com/projects/p/global/snapshots/snapshot-of-pvc-disk",
					},
					{
						Action:       "remove",
						ResourceType: "compute.googleapis.com/Disk",
						Resource:     "//compute.googleapis.com/projects/p/zones/us-central1-a/disks/pvc-disk",
					},
				},
			}},
		},
	}

	target, ok := resolveRecommenderTarget(rec, index)
	require.True(t, ok)
	assert.Equal(t, "pvc-disk", target.Name)

	t.Run("no operation resolves", func(t *testing.T) {
		unresolved := &recommenderpb.Recommendation{
			Content: &recommenderpb.RecommendationContent{
				OperationGroups: []*recommenderpb.OperationGroup{{
					Operations: []*recommenderpb.Operation{{
						Resource: "//compute.googleapis.com/projects/p/zones/us-central1-a/instances/never-collected",
					}},
				}},
			},
		}
		_, ok := resolveRecommenderTarget(unresolved, index)
		assert.False(t, ok)
	})

	t.Run("no content", func(t *testing.T) {
		_, ok := resolveRecommenderTarget(&recommenderpb.Recommendation{}, index)
		assert.False(t, ok)
	})

	// GKE DiagnosisRecommender shape: no operation groups at all; the cluster is
	// referenced via content.overview targetClusters, by project NUMBER while the
	// collected cluster's selfLink carries the project ID.
	t.Run("diagnosis overview target cluster", func(t *testing.T) {
		// The selfLink carries the project ID, the overview's clusterUri the
		// project NUMBER — the canonicalized project segment must bridge them.
		cluster := providers.Resource{
			Id:          "78f3eb8cafcf41c1",
			Name:        "demo-cluster",
			Type:        "container.googleapis.com/Cluster",
			Arn:         "https://container.googleapis.com/v1/projects/my-project/locations/us-central1/clusters/demo-cluster",
			ServiceName: "Kubernetes Engine",
			Region:      "us-central1",
		}
		clusterIndex := buildRecommenderResourceIndex([]providers.Resource{cluster})

		overview, err := structpb.NewStruct(map[string]interface{}{
			"targetClusters": []interface{}{
				map[string]interface{}{
					"clusterId":  "78f3eb8cafcf41c1",
					"clusterUri": "//container.googleapis.com/projects/123456789012/locations/us-central1/clusters/demo-cluster",
				},
			},
		})
		require.NoError(t, err)

		rec := &recommenderpb.Recommendation{
			Content: &recommenderpb.RecommendationContent{Overview: overview},
		}

		target, ok := resolveRecommenderTarget(rec, clusterIndex)
		require.True(t, ok)
		assert.Equal(t, "demo-cluster", target.Name)
	})
}

// TestLinkRecommenderTarget proves the stamped ExternalResourceId is the
// identical construction etl_resources uses when storing the resource — built
// from the matched resource's own stored fields (numeric instance ID included,
// which the recommendation payload never carries).
func TestLinkRecommenderTarget(t *testing.T) {
	account := providers.Account{CloudProvider: "gcp", AccountNumber: "my-project"}
	instance := providers.Resource{
		Id:          "1020227973377891447",
		Name:        "demo-billing-vm",
		Type:        "compute.googleapis.com/Instance",
		Arn:         "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/demo-billing-vm",
		ServiceName: ServiceNameCompute,
		Region:      "us-central1",
	}
	index := buildRecommenderResourceIndex([]providers.Resource{instance})

	rec := &recommenderpb.Recommendation{
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{{
				Operations: []*recommenderpb.Operation{{
					Action:       "replace",
					ResourceType: "compute.googleapis.com/Instance",
					Resource:     "//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/demo-billing-vm",
				}},
			}},
		},
	}

	mapped := providers.Recommendation{Data: map[string]any{"source": "gcp"}}
	linked := linkRecommenderTarget(mapped, rec, account, index)

	expected := common.BuildExternalResourceId("gcp", "my-project", "us-central1", ServiceNameCompute, "compute.googleapis.com/Instance", "1020227973377891447", "")
	assert.Equal(t, expected, linked.ExternalResourceId)
	assert.Equal(t, "demo-billing-vm", linked.Data["target_resource_name"])

	t.Run("unmatched leaves recommendation untouched", func(t *testing.T) {
		unlinked := linkRecommenderTarget(providers.Recommendation{Data: map[string]any{}}, &recommenderpb.Recommendation{}, account, index)
		assert.Empty(t, unlinked.ExternalResourceId)
		assert.NotContains(t, unlinked.Data, "target_resource_name")
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

// TestResolveRecommenderLocations proves locations come from the account's own
// resources: regions from the Region field (deduped, lowercased), zones parsed
// back out of selfLink/ARN since collectors normalize Region to the enclosing
// region and the zone only survives inside those links.
func TestResolveRecommenderLocations(t *testing.T) {
	resources := []providers.Resource{
		{
			Region: "asia-south1",
			Arn:    "https://www.googleapis.com/compute/v1/projects/p/zones/asia-south1-a/instances/vm-1",
		},
		{
			Region: "Asia-South1", // dedupes case-insensitively with the row above
			Meta:   map[string]interface{}{"selfLink": "projects/p/zones/asia-south1-b/disks/disk-1"},
		},
		{Region: "US"},           // BigQuery multi-region dataset — lowercased
		{Region: "  "},           // blank region — skipped
		{Region: "global"},       // global resources (IAM, images) — scopeGlobal owns this class
		{Region: "europe-west2"}, // no zone info anywhere — region only
	}

	regions, zones := resolveRecommenderLocations(resources)

	assert.Equal(t, []string{"asia-south1", "europe-west2", "us"}, regions)
	assert.Equal(t, []string{"asia-south1-a", "asia-south1-b"}, zones)
}

func TestResolveRecommenderLocationsEmpty(t *testing.T) {
	regions, zones := resolveRecommenderLocations(nil)
	assert.Empty(t, regions)
	assert.Empty(t, zones)
}

// TestLocationsForScope proves the bitmask expansion: each scope class
// contributes its own location list, and combinations union them.
func TestLocationsForScope(t *testing.T) {
	regions := []string{"asia-south1", "us"}
	zones := []string{"asia-south1-a"}

	assert.Equal(t, []string{"global"}, locationsForScope(scopeGlobal, regions, zones))
	assert.Equal(t, []string{"asia-south1", "us"}, locationsForScope(scopeRegional, regions, zones))
	assert.Equal(t, []string{"asia-south1-a"}, locationsForScope(scopeZonal, regions, zones))
	assert.Equal(t, []string{"global", "asia-south1", "us"}, locationsForScope(scopeRegional|scopeGlobal, regions, zones))
	assert.Equal(t, []string{"asia-south1", "us", "asia-south1-a"}, locationsForScope(scopeRegional|scopeZonal, regions, zones))
}

// TestRecommenderTypesDeclareScope guards against a new entry silently
// defaulting to scope 0, which would make locationsForScope return no
// locations and the recommender never be queried.
func TestRecommenderTypesDeclareScope(t *testing.T) {
	for _, rt := range recommenderTypes {
		assert.NotZero(t, rt.scope, "recommender %s must declare a location scope", rt.id)
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

	// No collected resources in this standalone test — exercise the fallback
	// region list plus global, the same path production takes on a fresh account.
	regions := fallbackRecommenderRegions

	var recommendations []providers.Recommendation
	for _, rt := range recommenderTypes {
		for _, location := range locationsForScope(rt.scope, regions, nil) {
			parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/%s", projectID, location, rt.id)

			// Standalone test has no collected resources, so the target index is
			// empty and every recommendation stays unlinked — same as a fresh account.
			recs, fetchErr := fetchRecommendations(ctx, client, parent, rt.id, rt.category, location, providers.Account{CloudProvider: "gcp", AccountNumber: projectID}, nil)
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
