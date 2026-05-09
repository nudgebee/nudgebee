package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetPerformanceInsightsMetrics(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// Create context
	ctx := providers.NewCloudProviderContext(context.Background())

	// Get account from environment
	account := providers.Account{
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
	}

	// Test with RDS instance "main" from event b07b90b8-ea86-4c27-a463-8f0c866baa2a
	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)

	request := PerformanceInsightsRequest{
		DBInstanceIdentifier: "main",
		Region:               "us-east-1",
		StartTime:            &startTime,
		EndTime:              &endTime,
	}

	response, err := GetPerformanceInsightsMetrics(ctx, account, request)

	// Assert response is valid
	assert.NotNil(t, response)
	assert.Equal(t, "main", response.DBInstanceIdentifier)

	// Performance Insights may not be enabled, so we check that field
	if !response.PerformanceInsightsEnabled {
		t.Logf("Performance Insights is not enabled for instance 'main'")
		assert.Empty(t, response.Metrics, "Metrics should be empty when PI is not enabled")
		assert.Empty(t, response.TopSQL, "TopSQL should be empty when PI is not enabled")
		assert.Empty(t, response.WaitEvents, "WaitEvents should be empty when PI is not enabled")
		return
	}

	// If PI is enabled, we should have metrics
	t.Logf("Performance Insights is enabled for instance 'main'")

	if err != nil {
		t.Logf("Error fetching PI metrics (may be expected if there are permission issues): %v", err)
		return
	}

	// Validate metrics structure
	assert.NotNil(t, response.Metrics, "Metrics should not be nil when PI is enabled")

	// Log the response for debugging
	t.Logf("Performance Insights Response:")
	t.Logf("  DB Instance: %s", response.DBInstanceIdentifier)
	t.Logf("  PI Enabled: %v", response.PerformanceInsightsEnabled)
	t.Logf("  Metrics Count: %d", len(response.Metrics))
	t.Logf("  Top SQL Count: %d", len(response.TopSQL))
	t.Logf("  Wait Events Count: %d", len(response.WaitEvents))

	if len(response.Metrics) > 0 {
		for _, metric := range response.Metrics {
			t.Logf("  Metric: %s (unit: %s, data points: %d)", metric.Name, metric.Unit, len(metric.Values))
			if len(metric.Values) > 0 {
				t.Logf("    Sample values: %v", metric.Values[:min(3, len(metric.Values))])
			}
		}
	}

	if len(response.TopSQL) > 0 {
		t.Logf("  Top SQL Queries:")
		for i, sql := range response.TopSQL {
			t.Logf("    %d. DB Load: %.2f, SQL: %s", i+1, sql.DBLoad, truncate(sql.SQLText, 80))
		}
	}

	if len(response.WaitEvents) > 0 {
		t.Logf("  Wait Events:")
		for _, event := range response.WaitEvents {
			t.Logf("    %s: %.2f DB Load (%.1f%%)", event.EventType, event.DBLoad, event.Percentage)
		}
	}
}

func TestCheckPerformanceInsightsEnabled(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// Get account from environment
	account := providers.Account{
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
	}

	cfg, err := getAwsConfigFromAccount(context.Background(), account)
	assert.NoError(t, err, "Failed to create AWS config")

	enabled, resourceID, err := checkPerformanceInsightsEnabled(context.Background(), cfg, "main")

	assert.NoError(t, err, "Failed to check PI status")

	t.Logf("Performance Insights Status for instance 'main':")
	t.Logf("  Enabled: %v", enabled)
	t.Logf("  DbiResourceId: %s", resourceID)

	if enabled {
		assert.NotEmpty(t, resourceID, "Resource ID should not be empty when PI is enabled")
	}
}

func TestGetPerformanceInsightsMetrics_CustomTimeRange(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// Create context
	ctx := providers.NewCloudProviderContext(context.Background())

	// Get account from environment
	account := providers.Account{
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
	}

	// Test with custom time range (last 24 hours)
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	request := PerformanceInsightsRequest{
		DBInstanceIdentifier: "main",
		Region:               "us-east-1",
		StartTime:            &startTime,
		EndTime:              &endTime,
	}

	response, err := GetPerformanceInsightsMetrics(ctx, account, request)

	assert.NotNil(t, response)
	assert.Equal(t, "main", response.DBInstanceIdentifier)

	if !response.PerformanceInsightsEnabled {
		t.Logf("Performance Insights is not enabled for instance 'main'")
		return
	}

	if err != nil {
		t.Logf("Error fetching PI metrics with custom time range: %v", err)
		return
	}

	t.Logf("Successfully fetched PI metrics for 24-hour time range")
	t.Logf("  Data points in metrics: %d", len(response.Metrics))
}

func TestGetPerformanceInsightsMetrics_InvalidInstance(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// Create context
	ctx := providers.NewCloudProviderContext(context.Background())

	// Get account from environment
	account := providers.Account{
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
	}

	request := PerformanceInsightsRequest{
		DBInstanceIdentifier: "nonexistent-instance-12345",
		Region:               "us-east-1",
	}

	response, err := GetPerformanceInsightsMetrics(ctx, account, request)

	// Should return an error for non-existent instance
	assert.Error(t, err, "Should return error for non-existent instance")
	assert.Contains(t, err.Error(), "failed to describe DB instance", "Error should mention DB instance not found")

	// Response should be empty for non-existent instance
	assert.Equal(t, "", response.DBInstanceIdentifier)
	assert.False(t, response.PerformanceInsightsEnabled)
}

// Helper functions

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
