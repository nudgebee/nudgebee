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
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	resp, err := QueryDatabasePerformance(ctx, os.Getenv("TEST_GCP_ACCOUNT"), providers.DatabasePerformanceRequest{
		Region:             "us-central1",
		DatabaseIdentifier: "beehive-dev-pg",
		IncludeTopQueries:  true,
	})

	if err != nil {
		t.Fatalf("Error querying GCP Performance Insights: %v", err)
	}

	// Verify response structure
	if resp.Provider != "gcp" {
		t.Errorf("Expected provider 'gcp', got '%s'", resp.Provider)
	}
	if resp.DatabaseIdentifier != "beehive-dev-pg" {
		t.Errorf("Expected database 'beehive-dev-pg', got '%s'", resp.DatabaseIdentifier)
	}

	// Log the response as JSON for inspection
	jsonResp, _ := json.MarshalIndent(resp, "", "  ")
	t.Logf("GCP Performance Insight Response: %s", string(jsonResp))
}
