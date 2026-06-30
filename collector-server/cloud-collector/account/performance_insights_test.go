package account

import (
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
)

func TestPerformanceInsightAws(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	resp, err := QueryDatabasePerformance(ctx, os.Getenv("TEST_ACCOUNT"), providers.DatabasePerformanceRequest{
		Region:             "us-east-1",
		DatabaseIdentifier: "main",
	})

	if err != nil {
		t.Fatalf("Error querying database performance: %v", err)
	}
	// print as json
	jsonResp, _ := json.MarshalIndent(resp, "", "  ")
	t.Logf("Performance Insight Response: %s", string(jsonResp))
}

func TestPerformanceInsightGCP(t *testing.T) {
	dbID := os.Getenv("TEST_GCP_DATABASE_ID")
	if dbID == "" {
		t.Skip("Skipping test - TEST_GCP_DATABASE_ID must be set (Cloud SQL instance identifier)")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	resp, err := QueryDatabasePerformance(ctx, os.Getenv("TEST_GCP_ACCOUNT"), providers.DatabasePerformanceRequest{
		Region:             "us-central1",
		DatabaseIdentifier: dbID,
		IncludeTopQueries:  true,
	})

	if err != nil {
		t.Fatalf("Error querying GCP Performance Insights: %v", err)
	}

	// Verify response structure
	if resp.Provider != "gcp" {
		t.Errorf("Expected provider 'gcp', got '%s'", resp.Provider)
	}
	if resp.DatabaseIdentifier != dbID {
		t.Errorf("Expected database '%s', got '%s'", dbID, resp.DatabaseIdentifier)
	}

	// Log the response as JSON for inspection
	jsonResp, _ := json.MarshalIndent(resp, "", "  ")
	t.Logf("GCP Performance Insight Response: %s", string(jsonResp))
}
