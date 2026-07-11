package account

import (
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
)

func TestStoreUsageAws(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "6c008cf8-4d79-4999-8447-573a697d0652", time.January, 2026)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29", time.October, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageAzure2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := discoverAndStoreAccountResources(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreUsageGCloud(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreUsage(ctx, os.Getenv("TEST_ACCOUNT"), time.November, 2025)
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

// TestSummarizeDailySpend_ExcludesCreditsAndRefunds verifies that credit/refund line
// items (negative costs) are excluded from the daily and month-to-date totals, so the
// cost notification reports gross spend instead of net-of-credits. Net-of-credits is
// what produced the negative figures reported in issue #29455.
func TestSummarizeDailySpend_ExcludesCreditsAndRefunds(t *testing.T) {
	yesterday := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	earlierThisMonth := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	items := []providers.UsageReportItem{
		// Usage on the anchor day — counts toward both daily and monthly.
		{ProductCode: "AmazonEC2", CostCategory: providers.UsageReportItemTypeUsage, Cost: 5.0, CostCurrency: "USD", EndDate: yesterday},
		// Tax on the anchor day — part of the bill, counts.
		{ProductCode: "AmazonEC2", CostCategory: providers.UsageReportItemTypeTax, Cost: 0.5, CostCurrency: "USD", EndDate: yesterday},
		// Large credit on the anchor day — must be excluded (previously drove totals negative).
		{ProductCode: "AmazonEC2", CostCategory: providers.UsageReportCostCategory("Credit"), Cost: -100.0, CostCurrency: "USD", EndDate: yesterday},
		// Refund on the anchor day — must be excluded.
		{ProductCode: "AmazonS3", CostCategory: providers.UsageReportCostCategory("Refund"), Cost: -3.0, CostCurrency: "USD", EndDate: yesterday},
		// Usage earlier in the month — counts toward monthly only, not daily.
		{ProductCode: "AmazonS3", CostCategory: providers.UsageReportItemTypeUsage, Cost: 7.0, CostCurrency: "USD", EndDate: earlierThisMonth},
		// Credit earlier in the month — excluded from monthly.
		{ProductCode: "AmazonS3", CostCategory: providers.UsageReportCostCategory("Credit"), Cost: -50.0, CostCurrency: "USD", EndDate: earlierThisMonth},
	}

	summary := summarizeDailySpend(items, yesterday)

	// Daily = 5.0 (EC2 usage) + 0.5 (tax); credit and refund excluded.
	assert.InDelta(t, 5.5, summary.totalDay, 1e-9, "daily total must exclude credits/refunds")
	// Monthly = 5.0 + 0.5 + 7.0; both credits and the refund excluded.
	assert.InDelta(t, 12.5, summary.totalMonth, 1e-9, "monthly total must exclude credits/refunds")
	// The core of issue #29455: totals must stay positive, not net-of-credits.
	assert.Greater(t, summary.totalDay, 0.0)
	assert.Greater(t, summary.totalMonth, 0.0)
	// Only the two non-credit anchor-day items are retained as "today's" items.
	assert.Len(t, summary.todayItems, 2)
	// Per-service monthly breakdown also excludes credits.
	assert.InDelta(t, 5.5, summary.monthlyServices["AmazonEC2"], 1e-9)
	assert.InDelta(t, 7.0, summary.monthlyServices["AmazonS3"], 1e-9)
	assert.Equal(t, "USD", summary.currency)
	assert.False(t, summary.mixedCurrencies)
}
