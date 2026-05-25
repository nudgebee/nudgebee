package account

import (
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildSpendMap_GCPNullResourceName verifies that GCP services with NULL resource.name
// (which all fall back to ResourceId=projectId) produce separate spend entries per service,
// not a single collapsed entry.
func TestBuildSpendMap_GCPNullResourceName(t *testing.T) {
	account := providers.Account{
		CloudProvider: "GCP",
		AccountNumber: "nudgebee-dev",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Simulate GCP items where resource.name is NULL — ResourceId falls back to project ID.
	// Before the fix, these items would collide on the same spendKey.
	items := []providers.UsageReportItem{
		{
			ProductCode:        "Artifact Registry",
			ResourceType:       "artifact-registry",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               100.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "BigQuery",
			ResourceType:       "bigquery",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               0.78,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "VM Manager",
			ResourceType:       "vm-manager",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               168.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}

	// Mock: each service has a unique cloud_resource_id UUID
	extIdMap := map[string]string{
		"arn:gcp:artifact-registry:us-central1:nudgebee-dev:artifact-registry:nudgebee-dev": "uuid-artifact-registry",
		"arn:gcp:bigquery:us-central1:nudgebee-dev:bigquery:nudgebee-dev":                   "uuid-bigquery",
		"arn:gcp:vm-manager:us-central1:nudgebee-dev:vm-manager:nudgebee-dev":               "uuid-vm-manager",
	}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	// Must produce 3 separate entries, not 1 collapsed entry
	assert.Equal(t, 3, len(spendMap), "expected 3 separate spend entries for 3 different services, got %d", len(spendMap))

	// Verify each entry has the correct amount and service tag
	type spendCheck struct {
		service string
		amount  float64
	}
	found := map[string]spendCheck{}
	for _, entry := range spendMap {
		var tags map[string][]string
		err := json.Unmarshal([]byte(entry["tags"].(string)), &tags)
		require.NoError(t, err)
		svc := tags["nb_service_name"][0]
		found[svc] = spendCheck{service: svc, amount: entry["amount"].(float64)}
	}

	assert.Equal(t, 100.0, found["Artifact Registry"].amount, "Artifact Registry cost should be 100, not accumulated")
	assert.Equal(t, 0.78, found["BigQuery"].amount, "BigQuery cost should be 0.78, not accumulated")
	assert.Equal(t, 168.0, found["VM Manager"].amount, "VM Manager cost should be 168, not accumulated")
}

// TestBuildSpendMap_CreditsSeparatePerService verifies that credit items from different
// services produce separate spend entries instead of collapsing into one row per day.
func TestBuildSpendMap_CreditsSeparatePerService(t *testing.T) {
	account := providers.Account{
		CloudProvider: "GCP",
		AccountNumber: "nudgebee-dev",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	items := []providers.UsageReportItem{
		{
			ProductCode:        "Gemini API",
			ResourceType:       "gemini-api",
			ResourceId:         "", // cleared for credits
			ResourceRegionCode: "global",
			CostCategory:       providers.UsageReportCostCategory("Credit"),
			CostSubCategory:    "Credits",
			Cost:               -500.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{"nb_credit_source_resource": {"nudgebee-dev"}},
		},
		{
			ProductCode:        "Compute Engine",
			ResourceType:       "compute-engine",
			ResourceId:         "", // cleared for credits
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportCostCategory("Credit"),
			CostSubCategory:    "Credits",
			Cost:               -200.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{"nb_credit_source_resource": {"nudgebee-dev/some-vm"}},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}
	extIdMap := map[string]string{}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	// Must produce 2 separate credit entries (different service+region), not 1 collapsed
	assert.Equal(t, 2, len(spendMap), "expected 2 separate credit entries, got %d", len(spendMap))

	// Verify each credit has correct amount
	for _, entry := range spendMap {
		assert.True(t, entry["exclude_aggregate"].(bool), "credit entries should have exclude_aggregate=true")
		amount := entry["amount"].(float64)
		assert.True(t, amount == -500.0 || amount == -200.0, "credit amount should be -500 or -200, got %f", amount)
	}
}

// TestBuildSpendMap_SameServiceSameDayAggregates verifies that multiple items from
// the same service on the same day correctly aggregate into one spend entry.
func TestBuildSpendMap_SameServiceSameDayAggregates(t *testing.T) {
	account := providers.Account{
		CloudProvider: "GCP",
		AccountNumber: "nudgebee-dev",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Two items for the same service, same resource, same day — should aggregate
	items := []providers.UsageReportItem{
		{
			ProductCode:        "Cloud SQL",
			ResourceType:       "cloud-sql",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               50.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "Cloud SQL",
			ResourceType:       "cloud-sql",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeTax,
			Cost:               5.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}
	extIdMap := map[string]string{
		"arn:gcp:cloud-sql:us-central1:nudgebee-dev:cloud-sql:nudgebee-dev": "uuid-cloud-sql",
	}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	assert.Equal(t, 1, len(spendMap), "same service+resource+day items should aggregate into 1 entry")

	for _, entry := range spendMap {
		assert.Equal(t, 55.0, entry["amount"].(float64), "aggregated amount should be 50 + 5 = 55")
	}
}

// TestBuildSpendMap_AWSUniqueResourceIds verifies AWS items with unique ARN-based
// ResourceIds produce separate spend entries (no regression from the fix).
func TestBuildSpendMap_AWSUniqueResourceIds(t *testing.T) {
	account := providers.Account{
		CloudProvider: "AWS",
		AccountNumber: "123456789012",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	items := []providers.UsageReportItem{
		{
			ProductCode:        "AmazonEC2",
			ResourceType:       "instance",
			ResourceId:         "i-0abc123",
			ResourceRegionCode: "us-east-1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               100.0,
			CostCurrency:       "USD",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "AmazonEC2",
			ResourceType:       "instance",
			ResourceId:         "i-0def456",
			ResourceRegionCode: "us-east-1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               200.0,
			CostCurrency:       "USD",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}
	extIdMap := map[string]string{
		"arn:aws:ec2:us-east-1:123456789012:instance:i-0abc123": "uuid-ec2-1",
		"arn:aws:ec2:us-east-1:123456789012:instance:i-0def456": "uuid-ec2-2",
	}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	assert.Equal(t, 2, len(spendMap), "two different EC2 instances should produce 2 entries")

	for _, entry := range spendMap {
		amount := entry["amount"].(float64)
		assert.True(t, amount == 100.0 || amount == 200.0, "each instance should retain its own cost")
	}
}

// TestBuildSpendMap_DifferentRegionsSameService verifies that the same service in
// different regions produces separate spend entries.
func TestBuildSpendMap_DifferentRegionsSameService(t *testing.T) {
	account := providers.Account{
		CloudProvider: "GCP",
		AccountNumber: "nudgebee-dev",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	items := []providers.UsageReportItem{
		{
			ProductCode:        "Artifact Registry",
			ResourceType:       "artifact-registry",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "asia-south1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               50.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "Artifact Registry",
			ResourceType:       "artifact-registry",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               30.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}
	extIdMap := map[string]string{
		"arn:gcp:artifact-registry:asia-south1:nudgebee-dev:artifact-registry:nudgebee-dev": "uuid-ar-asia",
		"arn:gcp:artifact-registry:us-central1:nudgebee-dev:artifact-registry:nudgebee-dev": "uuid-ar-us",
	}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	assert.Equal(t, 2, len(spendMap), "same service in different regions should produce 2 entries")
}

// TestBuildSpendMap_EmptyExternalResourceId verifies that non-credit items with empty
// externalResourceId (not found in extIdMap) still produce separate entries per service
// instead of colliding on an empty key.
func TestBuildSpendMap_EmptyExternalResourceId(t *testing.T) {
	account := providers.Account{
		CloudProvider: "GCP",
		AccountNumber: "nudgebee-dev",
	}
	day := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	items := []providers.UsageReportItem{
		{
			ProductCode:        "Cloud Functions",
			ResourceType:       "cloud-functions",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               10.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
		{
			ProductCode:        "Cloud Run",
			ResourceType:       "cloud-run",
			ResourceId:         "nudgebee-dev",
			ResourceRegionCode: "us-central1",
			CostCategory:       providers.UsageReportItemTypeUsage,
			Cost:               20.0,
			CostCurrency:       "INR",
			StartDate:          day,
			EndDate:            day.Add(24*time.Hour - time.Nanosecond),
			ResourceTags:       map[string][]string{},
		},
	}

	usageReport := providers.GetUsageReportResponse{Items: items}
	// Empty extIdMap — no resources found in DB, so externalResourceId lookups return ""
	extIdMap := map[string]string{}

	spendMap, err := buildSpendMap(account, usageReport, extIdMap, "tenant-1", "account-1")
	require.NoError(t, err)

	assert.Equal(t, 2, len(spendMap), "items with empty externalResourceId but different services should produce 2 entries, got %d", len(spendMap))

	for _, entry := range spendMap {
		amount := entry["amount"].(float64)
		assert.True(t, amount == 10.0 || amount == 20.0, "each service should retain its own cost, got %f", amount)
	}
}
