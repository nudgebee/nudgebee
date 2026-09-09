package gcloud

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/gcloud/models"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
)

// monthsAgo returns the start of the month that is n months before the current
// month, matching the clamping arithmetic in discoverAvailableUsageReportPeriods.
func monthsAgo(n int) time.Time {
	return monthStart(time.Now().UTC()).AddDate(0, -n, 0)
}

func TestDiscoverAvailableUsageReportPeriods(t *testing.T) {
	validAccount := providers.Account{
		AccountNumber: "test-project",
		Data: stringPtr(`{
			"billing_data": {
				"billing_project_id": "test-project",
				"dataset_name": "billing_export",
				"table_name": "gcp_billing_export_v1_ABC"
			}
		}`),
	}

	tests := []struct {
		name          string
		account       providers.Account
		maxMonths     int
		stub          func(providers.CloudProviderContext, models.BillingConfig, providers.Account) (time.Time, time.Time, bool, error)
		expectError   bool
		expectedCount int
	}{
		{
			// Export covers exactly four months, well inside the window: load those four.
			name:      "four months within window loads exactly four",
			account:   validAccount,
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return monthsAgo(3), monthsAgo(0), true, nil
			},
			expectedCount: 4,
		},
		{
			// Three years of history but a 6-month cap: trailing window wins.
			name:      "history exceeding window is truncated to maxMonths",
			account:   validAccount,
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return monthsAgo(36), monthsAgo(0), true, nil
			},
			expectedCount: 6,
		},
		{
			// Same three years with a matching cap: all of it loads.
			name:      "full history loads when cap is raised",
			account:   validAccount,
			maxMonths: 36,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return monthsAgo(35), monthsAgo(0), true, nil
			},
			expectedCount: 36,
		},
		{
			// A latest date in the future (clock skew) is clamped to the current month.
			name:      "future latest is clamped to current month",
			account:   validAccount,
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return monthsAgo(0), monthsAgo(-2), true, nil
			},
			expectedCount: 1,
		},
		{
			name:      "no data returns empty slice",
			account:   validAccount,
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return time.Time{}, time.Time{}, false, nil
			},
			expectedCount: 0,
		},
		{
			name:      "query error is propagated",
			account:   validAccount,
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				return time.Time{}, time.Time{}, false, errors.New("bigquery boom")
			},
			expectError: true,
		},
		{
			// maxMonths <= 0 disables backfill; the query must not run at all.
			name:      "non-positive maxMonths returns nil without querying",
			account:   validAccount,
			maxMonths: 0,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				t.Fatalf("queryBillingDateRange must not be called when maxMonths <= 0")
				return time.Time{}, time.Time{}, false, nil
			},
			expectedCount: 0,
		},
		{
			// Bad account data fails before any query.
			name:      "invalid billing config errors",
			account:   providers.Account{AccountNumber: "x", Data: stringPtr(`{}`)},
			maxMonths: 6,
			stub: func(_ providers.CloudProviderContext, _ models.BillingConfig, _ providers.Account) (time.Time, time.Time, bool, error) {
				t.Fatalf("queryBillingDateRange must not be called when config is invalid")
				return time.Time{}, time.Time{}, false, nil
			},
			expectError: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := security.NewRequestContext(context.Background(), nil, logger, nil, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := queryBillingDateRange
			defer func() { queryBillingDateRange = original }()
			queryBillingDateRange = tt.stub

			periods, err := discoverAvailableUsageReportPeriods(ctx, tt.account, tt.maxMonths)

			if tt.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Len(t, periods, tt.expectedCount)

			// Periods must be ascending and never in the future.
			currentMonth := monthStart(time.Now().UTC())
			for i, p := range periods {
				ps := time.Date(p.Year, p.Month, 1, 0, 0, 0, 0, time.UTC)
				assert.False(t, ps.After(currentMonth), "period %v is in the future", p)
				if i > 0 {
					prev := periods[i-1]
					prevStart := time.Date(prev.Year, prev.Month, 1, 0, 0, 0, 0, time.UTC)
					assert.True(t, ps.After(prevStart), "periods not strictly ascending at index %d", i)
				}
			}
		})
	}
}
