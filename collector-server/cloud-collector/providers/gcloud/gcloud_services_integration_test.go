package gcloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// TestGCPComputeIntegration tests the Compute Engine service
func TestGCPComputeIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &computeEngineService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== COMPUTE ENGINE RESOURCES ===\n")
		fmt.Printf("Total instances found: %d\n\n", len(resources))

		for i, r := range resources {
			fmt.Printf("Instance #%d:\n", i+1)
			fmt.Printf("  Name: %s\n", r.Name)
			fmt.Printf("  ID: %s\n", r.Id)
			fmt.Printf("  Type: %s\n", r.Type)
			fmt.Printf("  Status: %s\n", r.Status)
			fmt.Printf("  Region: %s\n", r.Region)
			fmt.Printf("  Service: %s\n", r.ServiceName)
			fmt.Printf("  Created: %s\n", r.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("  Tags: %v\n", r.Tags)
			fmt.Printf("  Meta keys: %v\n", getMapKeys(r.Meta))
			fmt.Printf("\n")
		}

		t.Logf("Found %d compute instances", len(resources))
		for _, r := range resources {
			t.Logf("  - Instance: %s (Status: %s, Region: %s)", r.Name, r.Status, r.Region)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Compute Engine",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}

		fmt.Printf("\n=== COMPUTE ENGINE RECOMMENDATIONS ===\n")
		fmt.Printf("Total recommendations: %d\n\n", len(recommendations))

		for i, r := range recommendations {
			fmt.Printf("Recommendation #%d:\n", i+1)
			fmt.Printf("  Rule: %s\n", r.RuleName)
			fmt.Printf("  Category: %s\n", r.CategoryName)
			fmt.Printf("  Severity: %s\n", r.Severity)
			fmt.Printf("  Action: %s\n", r.Action)
			fmt.Printf("  Resource ID: %s\n", r.ResourceId)
			fmt.Printf("  Resource Type: %s\n", r.ResourceType)
			fmt.Printf("  Region: %s\n", r.ResourceRegion)
			fmt.Printf("  Savings: $%.2f\n", r.Savings)
			fmt.Printf("  Data: %v\n", r.Data)
			fmt.Printf("\n")
		}

		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})

	// Test GetMetrics
	t.Run("GetMetrics", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		if len(resources) == 0 {
			t.Skip("No resources found to test metrics")
		}

		endTime := time.Now()
		startTime := endTime.Add(-1 * time.Hour)

		metrics, err := service.GetMetrices(ctx, account, providers.QueryMetricsRequest{
			ServiceName: "Compute Engine",
			StartDate:   &startTime,
			EndDate:     &endTime,
			MetricNames: []string{"cpu/utilization"},
			Statistics:  []string{"Average"},
		})
		if err != nil {
			t.Fatalf("GetMetrics failed: %v", err)
		}

		fmt.Printf("\n=== COMPUTE ENGINE METRICS ===\n")
		fmt.Printf("Total metric items: %d\n", len(metrics.Items))
		fmt.Printf("Time range: %s to %s\n\n", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

		for i, m := range metrics.Items {
			fmt.Printf("Metric #%d:\n", i+1)
			fmt.Printf("  Name: %s\n", m.Name)
			fmt.Printf("  Statistics: %s\n", m.Statistics)
			fmt.Printf("  Resource ID: %s\n", m.ResourceId)
			fmt.Printf("  Region: %s\n", m.Region)
			fmt.Printf("  Service: %s\n", m.ServiceName)
			fmt.Printf("  Data points: %d\n", len(m.Values))
			if len(m.Values) > 0 {
				fmt.Printf("  Sample values: [")
				for j := 0; j < len(m.Values) && j < 5; j++ {
					if j > 0 {
						fmt.Printf(", ")
					}
					fmt.Printf("%.2f", m.Values[j])
				}
				if len(m.Values) > 5 {
					fmt.Printf(", ...")
				}
				fmt.Printf("]\n")
			}
			fmt.Printf("\n")
		}

		t.Logf("Found %d metric items", len(metrics.Items))
	})
}

// TestGCPSQLIntegration tests the Cloud SQL service
func TestGCPSQLIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudSQLService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}
		t.Logf("Found %d SQL instances", len(resources))
		for _, r := range resources {
			t.Logf("  - Instance: %s (Status: %s, Type: %s)", r.Name, r.Status, r.Type)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud SQL",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}
		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})
}

// TestGCPStorageIntegration tests the Cloud Storage service
func TestGCPStorageIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudStorageService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}
		t.Logf("Found %d storage buckets", len(resources))
		for _, r := range resources {
			t.Logf("  - Bucket: %s (Location: %s)", r.Name, r.Region)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud Storage",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}
		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})
}

// TestGCPGKEIntegration tests the GKE service
func TestGCPGKEIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &gkeService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}
		t.Logf("Found %d GKE clusters", len(resources))
		for _, r := range resources {
			t.Logf("  - Cluster: %s (Status: %s, Type: %s)", r.Name, r.Status, r.Type)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Kubernetes Engine",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}
		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})
}

// TestGCPBigQueryIntegration tests the BigQuery service
func TestGCPBigQueryIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &bigQueryService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== BIGQUERY RESOURCES ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		datasetsCount := 0
		tablesCount := 0
		viewsCount := 0

		datasets := make(map[string][]providers.Resource)

		for _, r := range resources {
			if r.Type == "bigquery.googleapis.com/Dataset" {
				datasetsCount++
				datasets[r.Name] = []providers.Resource{}
			} else {
				tablesCount++
				if r.Type == "bigquery.googleapis.com/View" || r.Type == "bigquery.googleapis.com/MaterializedView" {
					viewsCount++
				}
			}
		}

		// Group tables by dataset
		for _, r := range resources {
			if r.Type != "bigquery.googleapis.com/Dataset" {
				// Extract dataset name from resource ID (format: projects/X/datasets/Y/tables/Z)
				parts := strings.Split(r.Id, "/")
				if len(parts) >= 4 {
					datasetName := parts[3]
					if _, ok := datasets[datasetName]; ok {
						datasets[datasetName] = append(datasets[datasetName], r)
					}
				}
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Datasets: %d\n", datasetsCount)
		fmt.Printf("  Tables: %d (including %d views)\n\n", tablesCount, viewsCount)

		// Print datasets and their tables
		for datasetName, tables := range datasets {
			// Find the dataset resource
			var datasetResource *providers.Resource
			for _, r := range resources {
				if r.Type == "bigquery.googleapis.com/Dataset" && r.Name == datasetName {
					datasetResource = &r
					break
				}
			}

			if datasetResource != nil {
				fmt.Printf("Dataset: %s\n", datasetName)
				fmt.Printf("  Location: %s\n", datasetResource.Region)
				fmt.Printf("  Created: %s\n", datasetResource.CreatedAt.Format("2006-01-02 15:04:05"))
				fmt.Printf("  Tags: %v\n", datasetResource.Tags)
				fmt.Printf("  Tables: %d\n", len(tables))

				for _, table := range tables {
					fmt.Printf("    - %s (%s)\n", table.Name, table.Type)
				}
				fmt.Printf("\n")
			}
		}

		t.Logf("Found %d BigQuery datasets/tables", len(resources))
		t.Logf("Summary: %d datasets, %d tables", datasetsCount, tablesCount)
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "BigQuery",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}
		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})
}

// TestGCPProviderIntegration tests the main GCP provider
func TestGCPProviderIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	provider := &gcloudProvider{}

	// Test ListResources for multiple services
	t.Run("ListResources-Compute", func(t *testing.T) {
		resp, err := provider.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: "Compute Engine",
			Regions:     []string{"us-central1"},
		})
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		t.Logf("Found %d compute resources", len(resp.Items))
	})

	t.Run("ListResources-GKE", func(t *testing.T) {
		resp, err := provider.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: "Kubernetes Engine",
			Regions:     []string{"us-central1"},
		})
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		t.Logf("Found %d GKE resources", len(resp.Items))
	})

	// Test QueryMetrics
	t.Run("QueryMetrics-Compute", func(t *testing.T) {
		endTime := time.Now()
		startTime := endTime.Add(-1 * time.Hour)

		resp, err := provider.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ServiceName: "Compute Engine",
			StartDate:   &startTime,
			EndDate:     &endTime,
			MetricNames: []string{"cpu/utilization"},
			Statistics:  []string{"Average"},
		})
		if err != nil {
			t.Fatalf("QueryMetrics failed: %v", err)
		}
		t.Logf("Found %d metric items", len(resp.Items))
	})
}

// TestServiceRegistry tests the service registry
func TestServiceRegistry(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		expectFound bool
	}{
		{"Compute", "Compute Engine", true},
		{"SQL", "Cloud SQL", true},
		{"Storage", "Cloud Storage", true},
		{"GKE", "Kubernetes Engine", true},
		{"BigQuery", "BigQuery", true},
		{"Functions", "Cloud Functions", true},
		{"Run", "Cloud Run", true},
		{"PubSub", "Cloud Pub/Sub", true},
		{"Monitoring", "Cloud Monitoring", true},
		{"Networking", "Networking", true},
		{"VM Manager", "VM Manager", true},
		{"Vertex AI", "Vertex AI", true},
		{"Gemini API", "Gemini API", true},
		{"Invalid", "invalid-service", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, found := GetGcloudService(tt.serviceName)
			if found != tt.expectFound {
				t.Errorf("GetGcloudService(%s) found=%v, want %v", tt.serviceName, found, tt.expectFound)
			}
			if tt.expectFound && service == nil {
				t.Errorf("GetGcloudService(%s) returned nil service", tt.serviceName)
			}
		})
	}
}

// TestMetricHelpers tests the metric helper functions
func TestMetricHelpers(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		expectedType string
	}{
		{"Compute", "compute", "compute.googleapis.com/instance"},
		{"Compute-Engine", "compute-engine", "compute.googleapis.com/instance"},
		{"SQL", "sql", "cloudsql.googleapis.com/database"},
		{"Cloud-SQL", "cloud-sql", "cloudsql.googleapis.com/database"},
		{"Storage", "storage", "storage.googleapis.com"},
		{"Cloud-Storage", "cloud-storage", "storage.googleapis.com"},
		{"GKE", "gke", "container.googleapis.com"},
		{"BigQuery", "bigquery", "bigquery.googleapis.com"},
		{"Monitoring", "monitoring", "monitoring.googleapis.com"},
		{"Networking", "networking", "networkmanagement.googleapis.com"},
		{"VM Manager", "vm manager", "vmmigration.googleapis.com"},
		{"Vertex AI", "vertex ai", "aiplatform.googleapis.com"},
		{"Gemini API", "gemini api", "gemini.googleapis.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metricType := getMetricTypePrefix(tt.serviceName, "")
			if metricType != tt.expectedType {
				t.Errorf("getMetricTypePrefix(%s) = %s, want %s", tt.serviceName, metricType, tt.expectedType)
			}
		})
	}
}

// TestResourceLabelKey tests the resource label key helper
func TestResourceLabelKey(t *testing.T) {
	tests := []struct {
		name          string
		serviceName   string
		expectedLabel string
	}{
		{"Compute", "compute", "instance_id"},
		{"SQL", "sql", "database_id"},
		{"Storage", "storage", "bucket_name"},
		{"GKE", "gke", "cluster_name"},
		{"BigQuery", "bigquery", "dataset_id"},
		{"Monitoring", "monitoring", "metric_id"},
		{"Networking", "networking", "network_id"},
		{"VM Manager", "vm manager", "vm_id"},
		{"Vertex AI", "vertex ai", "model_id"},
		{"Gemini API", "gemini api", "api_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labelKey := getResourceLabelKey(tt.serviceName, "")
			if labelKey != tt.expectedLabel {
				t.Errorf("getResourceLabelKey(%s) = %s, want %s", tt.serviceName, labelKey, tt.expectedLabel)
			}
		})
	}
}

// TestExtractMetricName tests metric name extraction
func TestExtractMetricName(t *testing.T) {
	tests := []struct {
		name         string
		metricType   string
		expectedName string
	}{
		{
			"Compute CPU",
			"compute.googleapis.com/instance/cpu/utilization",
			"cpu/utilization",
		},
		{
			"SQL Memory",
			"cloudsql.googleapis.com/database/memory/utilization",
			"memory/utilization",
		},
		{
			"Short Name",
			"metric",
			"metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := extractMetricName(tt.metricType)
			if extracted != tt.expectedName {
				t.Errorf("extractMetricName(%s) = %s, want %s", tt.metricType, extracted, tt.expectedName)
			}
		})
	}
}

// Helper function to create test account from environment
func getTestAccount(t *testing.T) providers.Account {
	// Load environment variables right before they are needed to ensure the encryption key is available.
	cloud.LoadEnvFromFile(t)

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Fatal("GCP_PROJECT_ID environment variable is required")
	}

	// Try to load from database first
	t.Logf("Attempting to load GCP account from database for project: %s", projectID)
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err == nil {
		query := `
			SELECT assume_role, access_key, access_secret, region, data::varchar,
			       cloud_provider, account_number, account_name
			FROM cloud_accounts
			WHERE account_number = $1 AND cloud_provider = 'GCP'
			ORDER BY created_at DESC
			LIMIT 1
		`
		r, err := dbManager.QueryRow(query, projectID)
		if err == nil {
			var account providers.Account
			var assumeRole, accessKey, accessSecret, region, cloudProvider, accountNumber, accountName *string
			var data string

			err = r.Scan(&assumeRole, &accessKey, &accessSecret, &region, &data, &cloudProvider, &accountNumber, &accountName)
			if err == nil && accessSecret != nil && *accessSecret != "" {
				t.Logf("✓ Successfully loaded account from database")
				account.AssumeRole = assumeRole
				account.AccessKey = accessKey
				account.AccessSecret = accessSecret
				account.Region = region
				account.Data = &data
				account.CloudProvider = *cloudProvider
				account.AccountNumber = *accountNumber
				account.AccountName = *accountName
				return account
			} else {
				t.Logf("Account found in DB but access_secret is empty, falling back to file")
			}
		} else {
			t.Logf("Account not found in database, falling back to file: %v", err)
		}
	} else {
		t.Logf("Could not connect to database, falling back to file: %v", err)
	}

	// Fallback: Read from GOOGLE_APPLICATION_CREDENTIALS file
	credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credPath == "" {
		t.Fatal("GOOGLE_APPLICATION_CREDENTIALS environment variable is required (database fallback failed)")
	}

	t.Logf("Loading credentials from file: %s", credPath)
	credBytes, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("Failed to read credentials file: %v", err)
	}

	encryptedCreds, err := common.Encrypt(string(credBytes))
	if err != nil {
		t.Fatalf("Failed to encrypt credentials for test: %v", err)
	}

	region := "us-central1-c"
	t.Logf("✓ Successfully loaded credentials from file")
	return providers.Account{
		AccountNumber: projectID,
		AccountName:   "Test GCP Project",
		AccessSecret:  &encryptedCreds,
		Region:        &region,
	}
}

// TestGCPCloudFunctionsIntegration tests the Cloud Functions service
func TestGCPCloudFunctionsIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudFunctionsService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD FUNCTIONS RESOURCES ===\n")
		fmt.Printf("Total functions found: %d\n\n", len(resources))

		for i, r := range resources {
			fmt.Printf("Function #%d:\n", i+1)
			fmt.Printf("  Name: %s\n", r.Name)
			fmt.Printf("  ID: %s\n", r.Id)
			fmt.Printf("  Type: %s\n", r.Type)
			fmt.Printf("  Status: %s\n", r.Status)
			fmt.Printf("  Region: %s\n", r.Region)
			fmt.Printf("  Service: %s\n", r.ServiceName)
			fmt.Printf("  Created: %s\n", r.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("  Tags: %v\n", r.Tags)
			if runtime, ok := r.Meta["runtime"].(string); ok {
				fmt.Printf("  Runtime: %s\n", runtime)
			}
			if triggerType, ok := r.Meta["trigger_type"].(string); ok {
				fmt.Printf("  Trigger: %s\n", triggerType)
			}
			if memory, ok := r.Meta["memory"].(string); ok {
				fmt.Printf("  Memory: %s\n", memory)
			}
			fmt.Printf("\n")
		}

		t.Logf("Found %d cloud functions", len(resources))
		for _, r := range resources {
			t.Logf("  - Function: %s (Status: %s, Region: %s)", r.Name, r.Status, r.Region)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud Functions",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD FUNCTIONS RECOMMENDATIONS ===\n")
		fmt.Printf("Total recommendations: %d\n\n", len(recommendations))

		for i, r := range recommendations {
			fmt.Printf("Recommendation #%d:\n", i+1)
			fmt.Printf("  Rule: %s\n", r.RuleName)
			fmt.Printf("  Category: %s\n", r.CategoryName)
			fmt.Printf("  Severity: %s\n", r.Severity)
			fmt.Printf("  Action: %s\n", r.Action)
			fmt.Printf("  Resource ID: %s\n", r.ResourceId)
			fmt.Printf("  Resource Type: %s\n", r.ResourceType)
			fmt.Printf("  Region: %s\n", r.ResourceRegion)
			fmt.Printf("  Savings: $%.2f\n", r.Savings)
			fmt.Printf("  Data: %v\n", r.Data)
			fmt.Printf("\n")
		}

		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s)", r.RuleName, r.ResourceId, r.Severity)
		}
	})

	// Test GetMetrics
	t.Run("GetMetrics", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		if len(resources) == 0 {
			t.Skip("No resources found to test metrics")
		}

		endTime := time.Now()
		startTime := endTime.Add(-1 * time.Hour)

		metrics, err := service.GetMetrices(ctx, account, providers.QueryMetricsRequest{
			ServiceName: "Cloud Functions",
			StartDate:   &startTime,
			EndDate:     &endTime,
			MetricNames: []string{"function/execution_count"},
			Statistics:  []string{"Average"},
		})
		if err != nil {
			t.Fatalf("GetMetrics failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD FUNCTIONS METRICS ===\n")
		fmt.Printf("Total metric items: %d\n", len(metrics.Items))
		fmt.Printf("Time range: %s to %s\n\n", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

		t.Logf("Found %d metric items", len(metrics.Items))
	})
}

// TestGCPCloudRunIntegration tests the Cloud Run service
func TestGCPCloudRunIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudRunService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD RUN RESOURCES ===\n")
		fmt.Printf("Total services found: %d\n\n", len(resources))

		for i, r := range resources {
			fmt.Printf("Service #%d:\n", i+1)
			fmt.Printf("  Name: %s\n", r.Name)
			fmt.Printf("  ID: %s\n", r.Id)
			fmt.Printf("  Type: %s\n", r.Type)
			fmt.Printf("  Status: %s\n", r.Status)
			fmt.Printf("  Region: %s\n", r.Region)
			fmt.Printf("  Service: %s\n", r.ServiceName)
			fmt.Printf("  Created: %s\n", r.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("  Tags: %v\n", r.Tags)
			if url, ok := r.Meta["url"].(string); ok {
				fmt.Printf("  URL: %s\n", url)
			}
			if image, ok := r.Meta["container_image"].(string); ok {
				fmt.Printf("  Image: %s\n", image)
			}
			if minInst, ok := r.Meta["min_instances"].(int32); ok {
				fmt.Printf("  Min Instances: %d\n", minInst)
			}
			if maxInst, ok := r.Meta["max_instances"].(int32); ok {
				fmt.Printf("  Max Instances: %d\n", maxInst)
			}
			if memory, ok := r.Meta["memory_limit"].(string); ok && memory != "" {
				fmt.Printf("  Memory: %s\n", memory)
			}
			fmt.Printf("\n")
		}

		t.Logf("Found %d cloud run services", len(resources))
		for _, r := range resources {
			t.Logf("  - Service: %s (Status: %s, Region: %s)", r.Name, r.Status, r.Region)
		}
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud Run",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD RUN RECOMMENDATIONS ===\n")
		fmt.Printf("Total recommendations: %d\n\n", len(recommendations))

		for i, r := range recommendations {
			fmt.Printf("Recommendation #%d:\n", i+1)
			fmt.Printf("  Rule: %s\n", r.RuleName)
			fmt.Printf("  Category: %s\n", r.CategoryName)
			fmt.Printf("  Severity: %s\n", r.Severity)
			fmt.Printf("  Action: %s\n", r.Action)
			fmt.Printf("  Resource ID: %s\n", r.ResourceId)
			fmt.Printf("  Resource Type: %s\n", r.ResourceType)
			fmt.Printf("  Region: %s\n", r.ResourceRegion)
			fmt.Printf("  Savings: $%.2f\n", r.Savings)
			fmt.Printf("  Data: %v\n", r.Data)
			fmt.Printf("\n")
		}

		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s, Savings: $%.2f)", r.RuleName, r.ResourceId, r.Severity, r.Savings)
		}
	})

	// Test GetMetrics
	t.Run("GetMetrics", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "us-central1")
		if len(resources) == 0 {
			t.Skip("No resources found to test metrics")
		}

		endTime := time.Now()
		startTime := endTime.Add(-1 * time.Hour)

		metrics, err := service.GetMetrices(ctx, account, providers.QueryMetricsRequest{
			ServiceName: "Cloud Run",
			StartDate:   &startTime,
			EndDate:     &endTime,
			MetricNames: []string{"request_count"},
			Statistics:  []string{"Average"},
		})
		if err != nil {
			t.Fatalf("GetMetrics failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD RUN METRICS ===\n")
		fmt.Printf("Total metric items: %d\n", len(metrics.Items))
		fmt.Printf("Time range: %s to %s\n\n", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

		t.Logf("Found %d metric items", len(metrics.Items))
	})
}

// TestGCPPubSubIntegration tests the Pub/Sub service
func TestGCPPubSubIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &pubSubService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== PUB/SUB RESOURCES ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		topicsCount := 0
		subscriptionsCount := 0

		for _, r := range resources {
			switch r.Type {
			case "pubsub.googleapis.com/Topic":
				topicsCount++
			case "pubsub.googleapis.com/Subscription":
				subscriptionsCount++
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Topics: %d\n", topicsCount)
		fmt.Printf("  Subscriptions: %d\n\n", subscriptionsCount)

		// Print topics
		fmt.Printf("Topics:\n")
		for _, r := range resources {
			if r.Type == "pubsub.googleapis.com/Topic" {
				fmt.Printf("  - %s\n", r.Name)
				if retention, ok := r.Meta["retention_duration"].(string); ok && retention != "0s" {
					fmt.Printf("    Retention: %s\n", retention)
				}
			}
		}
		fmt.Printf("\n")

		// Print subscriptions
		fmt.Printf("Subscriptions:\n")
		for _, r := range resources {
			if r.Type == "pubsub.googleapis.com/Subscription" {
				fmt.Printf("  - %s\n", r.Name)
				if topic, ok := r.Meta["topic"].(string); ok {
					fmt.Printf("    Topic: %s\n", topic)
				}
				if deliveryType, ok := r.Meta["delivery_type"].(string); ok {
					fmt.Printf("    Type: %s\n", deliveryType)
				}
				if deadLetter, ok := r.Meta["dead_letter_topic"].(string); ok && deadLetter != "" {
					fmt.Printf("    Dead Letter: %s\n", deadLetter)
				}
			}
		}
		fmt.Printf("\n")

		t.Logf("Found %d Pub/Sub resources", len(resources))
		t.Logf("Summary: %d topics, %d subscriptions", topicsCount, subscriptionsCount)
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "")
		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud Pub/Sub",
		}, resources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}

		fmt.Printf("\n=== PUB/SUB RECOMMENDATIONS ===\n")
		fmt.Printf("Total recommendations: %d\n\n", len(recommendations))

		for i, r := range recommendations {
			fmt.Printf("Recommendation #%d:\n", i+1)
			fmt.Printf("  Rule: %s\n", r.RuleName)
			fmt.Printf("  Category: %s\n", r.CategoryName)
			fmt.Printf("  Severity: %s\n", r.Severity)
			fmt.Printf("  Action: %s\n", r.Action)
			fmt.Printf("  Resource ID: %s\n", r.ResourceId)
			fmt.Printf("  Resource Type: %s\n", r.ResourceType)
			fmt.Printf("  Region: %s\n", r.ResourceRegion)
			fmt.Printf("  Savings: $%.2f\n", r.Savings)
			fmt.Printf("  Data: %v\n", r.Data)
			fmt.Printf("\n")
		}

		t.Logf("Found %d recommendations", len(recommendations))
		for _, r := range recommendations {
			t.Logf("  - %s: %s (Severity: %s, Savings: $%.2f)", r.RuleName, r.ResourceId, r.Severity, r.Savings)
		}
	})

	// Test GetMetrics
	t.Run("GetMetrics", func(t *testing.T) {
		resources, _ := service.GetResources(ctx, account, "")
		if len(resources) == 0 {
			t.Skip("No resources found to test metrics")
		}

		endTime := time.Now()
		startTime := endTime.Add(-1 * time.Hour)

		metrics, err := service.GetMetrices(ctx, account, providers.QueryMetricsRequest{
			ServiceName: "Cloud Pub/Sub",
			StartDate:   &startTime,
			EndDate:     &endTime,
			MetricNames: []string{"topic/send_message_operation_count"},
			Statistics:  []string{"Average"},
		})
		if err != nil {
			t.Fatalf("GetMetrics failed: %v", err)
		}

		fmt.Printf("\n=== PUB/SUB METRICS ===\n")
		fmt.Printf("Total metric items: %d\n", len(metrics.Items))
		fmt.Printf("Time range: %s to %s\n\n", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

		t.Logf("Found %d metric items", len(metrics.Items))
	})
}

// Helper function to get map keys for printing
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestGCPCloudMonitoringIntegration tests the Cloud Monitoring service
func TestGCPCloudMonitoringIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudMonitoringService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "")
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Expected ErrUnsupported for billing-only Cloud Monitoring service, got: %v", err)
		}
		if resources != nil {
			t.Errorf("Expected nil resources for Cloud Monitoring, got %d", len(resources))
		}
		t.Logf("Cloud Monitoring service validated (ErrUnsupported)")
	})
}

// TestGCPNetworkingIntegration tests the Networking service
func TestGCPNetworkingIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &networkingService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "global")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== NETWORKING RESOURCES ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		vpcsCount := 0
		subnetsCount := 0
		firewallsCount := 0

		for _, r := range resources {
			switch r.Type {
			case "vpc-network":
				vpcsCount++
			case "subnet":
				subnetsCount++
			case "firewall-rule":
				firewallsCount++
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  VPC Networks: %d\n", vpcsCount)
		fmt.Printf("  Subnets: %d\n", subnetsCount)
		fmt.Printf("  Firewall Rules: %d\n\n", firewallsCount)

		// Print VPCs
		fmt.Printf("VPC Networks:\n")
		for _, r := range resources {
			if r.Type == "vpc-network" {
				fmt.Printf("  - %s (Region: %s)\n", r.Name, r.Region)
			}
		}
		fmt.Printf("\n")

		// Print Firewall Rules (first 10)
		fmt.Printf("Firewall Rules (first 10):\n")
		count := 0
		for _, r := range resources {
			if r.Type == "firewall-rule" && count < 10 {
				fmt.Printf("  - %s\n", r.Name)
				count++
			}
		}
		fmt.Printf("\n")

		t.Logf("Found %d networking resources", len(resources))
		t.Logf("Summary: %d VPCs, %d subnets, %d firewall rules", vpcsCount, subnetsCount, firewallsCount)
	})

	// Test regional subnets
	t.Run("GetResources-Regional", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}
		t.Logf("Found %d regional networking resources", len(resources))
	})
}

// TestGCPVMManagerIntegration tests the VM Manager service
func TestGCPVMManagerIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &vmManagerService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Expected ErrUnsupported for billing-only VM Manager service, got: %v", err)
		}
		if resources != nil {
			t.Errorf("Expected nil resources for VM Manager, got %d", len(resources))
		}
		t.Logf("VM Manager service validated (ErrUnsupported)")
	})
}

// TestGCPVertexAIIntegration tests the Vertex AI service
func TestGCPVertexAIIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &vertexAIService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== VERTEX AI RESOURCES ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		endpointsCount := 0
		modelsCount := 0

		for _, r := range resources {
			switch r.Type {
			case "vertex-ai-endpoint":
				endpointsCount++
			case "vertex-ai-model":
				modelsCount++
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Endpoints: %d\n", endpointsCount)
		fmt.Printf("  Models: %d\n\n", modelsCount)

		// Print Endpoints
		fmt.Printf("Endpoints:\n")
		for _, r := range resources {
			if r.Type == "vertex-ai-endpoint" {
				fmt.Printf("  - %s (Region: %s, Status: %s)\n", r.Name, r.Region, r.Status)
			}
		}
		fmt.Printf("\n")

		// Print Models
		fmt.Printf("Models:\n")
		for _, r := range resources {
			if r.Type == "vertex-ai-model" {
				fmt.Printf("  - %s (Region: %s)\n", r.Name, r.Region)
			}
		}
		fmt.Printf("\n")

		t.Logf("Found %d Vertex AI resources", len(resources))
		t.Logf("Summary: %d endpoints, %d models", endpointsCount, modelsCount)
	})
}

// TestGCPGeminiIntegration tests the Gemini API service
func TestGCPGeminiIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &geminiService{}

	// Test GetResources
	t.Run("GetResources", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "")
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Expected ErrUnsupported for billing-only Gemini API service, got: %v", err)
		}
		if resources != nil {
			t.Errorf("Expected nil resources for Gemini API, got %d", len(resources))
		}
		t.Logf("Gemini API service validated (ErrUnsupported)")
	})
}

// TestNewServiceRegistry tests the service registry with new services
func TestNewServiceRegistry(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		expectFound bool
	}{
		// Google Cloud official service names (case-insensitive)
		{"Compute", "Compute Engine", true},
		{"Storage", "Cloud Storage", true},
		{"BigQuery", "BigQuery", true},
		{"SQL", "Cloud SQL", true},
		{"GKE", "Kubernetes Engine", true},
		{"Functions", "Cloud Functions", true},
		{"Run", "Cloud Run", true},
		{"PubSub", "Cloud Pub/Sub", true},
		{"CloudMonitoring", "Cloud Monitoring", true},
		{"Networking", "Networking", true},
		{"VMManager", "VM Manager", true},
		{"VertexAI", "Vertex AI", true},
		{"Gemini", "Gemini API", true},
		{"CloudLoadBalancing", "Cloud Load Balancing", true},
		{"Invalid", "invalid-service", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, found := GetGcloudService(tt.serviceName)
			if found != tt.expectFound {
				t.Errorf("GetGcloudService(%s) found=%v, want %v", tt.serviceName, found, tt.expectFound)
			}
			if tt.expectFound && service == nil {
				t.Errorf("GetGcloudService(%s) returned nil service", tt.serviceName)
			}
		})
	}
}

// TestGCPCloudLoadBalancingIntegration tests the Cloud Load Balancing service
func TestGCPCloudLoadBalancingIntegration(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAccount(t)

	service := &cloudLoadBalancingService{}

	// Test GetResources - Global
	t.Run("GetResources-Global", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "global")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD LOAD BALANCING RESOURCES (Global) ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		// Count by type
		forwardingRulesCount := 0
		backendServicesCount := 0
		healthChecksCount := 0
		urlMapsCount := 0
		httpProxiesCount := 0
		httpsProxiesCount := 0

		for _, r := range resources {
			switch r.Type {
			case "forwarding-rule":
				forwardingRulesCount++
			case "backend-service":
				backendServicesCount++
			case "health-check":
				healthChecksCount++
			case "url-map":
				urlMapsCount++
			case "target-http-proxy":
				httpProxiesCount++
			case "target-https-proxy":
				httpsProxiesCount++
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Forwarding Rules: %d\n", forwardingRulesCount)
		fmt.Printf("  Backend Services: %d\n", backendServicesCount)
		fmt.Printf("  Health Checks: %d\n", healthChecksCount)
		fmt.Printf("  URL Maps: %d\n", urlMapsCount)
		fmt.Printf("  Target HTTP Proxies: %d\n", httpProxiesCount)
		fmt.Printf("  Target HTTPS Proxies: %d\n\n", httpsProxiesCount)

		// Print Forwarding Rules
		fmt.Printf("Forwarding Rules:\n")
		for _, r := range resources {
			if r.Type == "forwarding-rule" {
				ipAddr := ""
				if ip, ok := r.Meta["ip_address"].(string); ok {
					ipAddr = ip
				}
				scheme := ""
				if s, ok := r.Meta["load_balancing_scheme"].(string); ok {
					scheme = s
				}
				fmt.Printf("  - %s (IP: %s, Scheme: %s)\n", r.Name, ipAddr, scheme)
			}
		}
		fmt.Printf("\n")

		// Print Backend Services
		fmt.Printf("Backend Services:\n")
		for _, r := range resources {
			if r.Type == "backend-service" {
				protocol := ""
				if p, ok := r.Meta["protocol"].(string); ok {
					protocol = p
				}
				fmt.Printf("  - %s (Protocol: %s)\n", r.Name, protocol)
			}
		}
		fmt.Printf("\n")

		// Print Health Checks
		fmt.Printf("Health Checks:\n")
		for _, r := range resources {
			if r.Type == "health-check" {
				hcType := ""
				if hct, ok := r.Meta["type"].(string); ok {
					hcType = hct
				}
				fmt.Printf("  - %s (Type: %s)\n", r.Name, hcType)
			}
		}
		fmt.Printf("\n")

		// Print URL Maps
		fmt.Printf("URL Maps:\n")
		for _, r := range resources {
			if r.Type == "url-map" {
				defaultService := ""
				if ds, ok := r.Meta["default_service"].(string); ok {
					// Extract just the name from the URL
					parts := strings.Split(ds, "/")
					if len(parts) > 0 {
						defaultService = parts[len(parts)-1]
					}
				}
				fmt.Printf("  - %s (Default Service: %s)\n", r.Name, defaultService)
			}
		}
		fmt.Printf("\n")

		// Print Target Proxies
		fmt.Printf("Target HTTP Proxies:\n")
		for _, r := range resources {
			if r.Type == "target-http-proxy" {
				fmt.Printf("  - %s\n", r.Name)
			}
		}
		fmt.Printf("\n")

		fmt.Printf("Target HTTPS Proxies:\n")
		for _, r := range resources {
			if r.Type == "target-https-proxy" {
				fmt.Printf("  - %s\n", r.Name)
			}
		}
		fmt.Printf("\n")

		t.Logf("Found %d global load balancing resources", len(resources))
		t.Logf("Summary: %d forwarding rules, %d backend services, %d health checks, %d URL maps, %d HTTP proxies, %d HTTPS proxies",
			forwardingRulesCount, backendServicesCount, healthChecksCount, urlMapsCount, httpProxiesCount, httpsProxiesCount)
	})

	// Test GetResources - Regional
	t.Run("GetResources-Regional", func(t *testing.T) {
		resources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD LOAD BALANCING RESOURCES (us-central1) ===\n")
		fmt.Printf("Total resources found: %d\n\n", len(resources))

		// Count by type
		forwardingRulesCount := 0
		backendServicesCount := 0
		healthChecksCount := 0
		urlMapsCount := 0
		httpProxiesCount := 0
		httpsProxiesCount := 0
		targetPoolsCount := 0

		for _, r := range resources {
			switch r.Type {
			case "forwarding-rule":
				forwardingRulesCount++
			case "backend-service":
				backendServicesCount++
			case "health-check":
				healthChecksCount++
			case "url-map":
				urlMapsCount++
			case "target-http-proxy":
				httpProxiesCount++
			case "target-https-proxy":
				httpsProxiesCount++
			case "target-pool":
				targetPoolsCount++
			}
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Forwarding Rules: %d\n", forwardingRulesCount)
		fmt.Printf("  Backend Services: %d\n", backendServicesCount)
		fmt.Printf("  Health Checks: %d\n", healthChecksCount)
		fmt.Printf("  URL Maps: %d\n", urlMapsCount)
		fmt.Printf("  Target HTTP Proxies: %d\n", httpProxiesCount)
		fmt.Printf("  Target HTTPS Proxies: %d\n", httpsProxiesCount)
		fmt.Printf("  Target Pools: %d\n\n", targetPoolsCount)

		// Print regional forwarding rules with details
		fmt.Printf("Regional Forwarding Rules:\n")
		for _, r := range resources {
			if r.Type == "forwarding-rule" {
				ipAddr := ""
				if ip, ok := r.Meta["ip_address"].(string); ok {
					ipAddr = ip
				}
				scheme := ""
				if s, ok := r.Meta["load_balancing_scheme"].(string); ok {
					scheme = s
				}
				fmt.Printf("  - %s (IP: %s, Scheme: %s, Region: %s)\n", r.Name, ipAddr, scheme, r.Region)
			}
		}
		fmt.Printf("\n")

		// Print target pools with details
		fmt.Printf("Target Pools:\n")
		for _, r := range resources {
			if r.Type == "target-pool" {
				instanceCount := 0
				if ic, ok := r.Meta["instance_count"].(int); ok {
					instanceCount = ic
				}
				fmt.Printf("  - %s (Instances: %d, Region: %s)\n", r.Name, instanceCount, r.Region)
			}
		}
		fmt.Printf("\n")

		t.Logf("Found %d regional load balancing resources", len(resources))
		t.Logf("Summary: %d forwarding rules, %d backend services, %d health checks, %d target pools",
			forwardingRulesCount, backendServicesCount, healthChecksCount, targetPoolsCount)
	})

	// Test GetRecommendations
	t.Run("GetRecommendations", func(t *testing.T) {
		// Get both global and regional resources
		globalResources, err := service.GetResources(ctx, account, "global")
		if err != nil {
			t.Fatalf("GetResources (global) failed: %v", err)
		}

		regionalResources, err := service.GetResources(ctx, account, "us-central1")
		if err != nil {
			t.Fatalf("GetResources (regional) failed: %v", err)
		}

		allResources := append(globalResources, regionalResources...)

		recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{
			ServiceName: "Cloud Load Balancing",
		}, allResources)
		if err != nil {
			t.Fatalf("GetRecommendations failed: %v", err)
		}

		fmt.Printf("\n=== CLOUD LOAD BALANCING RECOMMENDATIONS ===\n")
		fmt.Printf("Total recommendations: %d\n\n", len(recommendations))

		// Group by rule name
		ruleCount := make(map[string]int)
		for _, r := range recommendations {
			ruleCount[r.RuleName]++
		}

		fmt.Printf("Recommendations by Rule:\n")
		for rule, count := range ruleCount {
			fmt.Printf("  %s: %d\n", rule, count)
		}
		fmt.Printf("\n")

		// Print detailed recommendations
		for i, r := range recommendations {
			fmt.Printf("Recommendation #%d:\n", i+1)
			fmt.Printf("  Rule: %s\n", r.RuleName)
			fmt.Printf("  Category: %s\n", r.CategoryName)
			fmt.Printf("  Severity: %s\n", r.Severity)
			fmt.Printf("  Action: %s\n", r.Action)
			fmt.Printf("  Resource ID: %s\n", r.ResourceId)
			fmt.Printf("  Resource Type: %s\n", r.ResourceType)
			fmt.Printf("  Region: %s\n", r.ResourceRegion)
			if reason, ok := r.Data["reason"].(string); ok {
				fmt.Printf("  Reason: %s\n", reason)
			}
			fmt.Printf("\n")
		}

		t.Logf("Found %d recommendations", len(recommendations))
		for rule, count := range ruleCount {
			t.Logf("  - %s: %d", rule, count)
		}
	})

	// Test via provider
	t.Run("ListResources-via-Provider", func(t *testing.T) {
		provider := &gcloudProvider{}
		resp, err := provider.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: "Cloud Load Balancing",
			Regions:     []string{"global"},
		})
		if err != nil {
			t.Fatalf("ListResources failed: %v", err)
		}
		t.Logf("Found %d load balancing resources via provider", len(resp.Items))
	})
}

// TestTargetPoolToResource tests the target pool to resource conversion
func TestTargetPoolToResource(t *testing.T) {
	service := &cloudLoadBalancingService{}

	// Helper to create string pointer
	strPtr := func(s string) *string { return &s }
	float32Ptr := func(f float32) *float32 { return &f }

	tests := []struct {
		name           string
		projectID      string
		region         string
		poolName       string
		description    string
		sessionAff     string
		failoverRatio  *float32
		backupPool     string
		healthChecks   []string
		instances      []string
		expectedType   string
		expectedRegion string
	}{
		{
			name:           "Basic target pool",
			projectID:      "test-project",
			region:         "us-central1",
			poolName:       "my-target-pool",
			description:    "Test target pool",
			sessionAff:     "NONE",
			failoverRatio:  nil,
			backupPool:     "",
			healthChecks:   []string{},
			instances:      []string{"instance-1", "instance-2"},
			expectedType:   "target-pool",
			expectedRegion: "us-central1",
		},
		{
			name:           "Target pool with failover",
			projectID:      "prod-project",
			region:         "us-east1",
			poolName:       "failover-pool",
			description:    "Pool with failover",
			sessionAff:     "CLIENT_IP",
			failoverRatio:  float32Ptr(0.5),
			backupPool:     "backup-pool-url",
			healthChecks:   []string{"health-check-1"},
			instances:      []string{},
			expectedType:   "target-pool",
			expectedRegion: "us-east1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock computepb.TargetPool
			pool := &computepb.TargetPool{
				Name:            strPtr(tt.poolName),
				Description:     strPtr(tt.description),
				SessionAffinity: strPtr(tt.sessionAff),
				BackupPool:      strPtr(tt.backupPool),
				HealthChecks:    tt.healthChecks,
				Instances:       tt.instances,
				SelfLink:        strPtr(fmt.Sprintf("https://compute.googleapis.com/compute/v1/projects/%s/regions/%s/targetPools/%s", tt.projectID, tt.region, tt.poolName)),
			}
			if tt.failoverRatio != nil {
				pool.FailoverRatio = tt.failoverRatio
			}

			resource := service.targetPoolToResource(tt.projectID, tt.region, pool)

			// Verify basic fields
			if resource.Type != tt.expectedType {
				t.Errorf("Type = %s, want %s", resource.Type, tt.expectedType)
			}
			if resource.Region != tt.expectedRegion {
				t.Errorf("Region = %s, want %s", resource.Region, tt.expectedRegion)
			}
			if resource.Name != tt.poolName {
				t.Errorf("Name = %s, want %s", resource.Name, tt.poolName)
			}
			if resource.ServiceName != ServiceNameCloudLoadBalancing {
				t.Errorf("ServiceName = %s, want %s", resource.ServiceName, ServiceNameCloudLoadBalancing)
			}

			// Verify ID format
			expectedID := fmt.Sprintf("%s/regions/%s/targetPools/%s", tt.projectID, tt.region, tt.poolName)
			if resource.Id != expectedID {
				t.Errorf("Id = %s, want %s", resource.Id, expectedID)
			}

			// Verify meta fields
			if tt.description != "" {
				if desc, ok := resource.Meta["description"].(string); !ok || desc != tt.description {
					t.Errorf("Meta[description] = %v, want %s", resource.Meta["description"], tt.description)
				}
			}
			if len(tt.instances) > 0 {
				if ic, ok := resource.Meta["instance_count"].(int); !ok || ic != len(tt.instances) {
					t.Errorf("Meta[instance_count] = %v, want %d", resource.Meta["instance_count"], len(tt.instances))
				}
			}

			t.Logf("Successfully converted target pool: %s", resource.Name)
		})
	}
}

// TestLoadBalancingServiceRegistry tests that Cloud Load Balancing is registered
func TestLoadBalancingServiceRegistry(t *testing.T) {
	service, found := GetGcloudService("Cloud Load Balancing")
	if !found {
		t.Error("Cloud Load Balancing service not found in registry")
	}
	if service == nil {
		t.Error("Cloud Load Balancing service is nil")
	}

	// Test case-insensitive lookup
	service2, found2 := GetGcloudService("cloud load balancing")
	if !found2 {
		t.Error("Cloud Load Balancing service not found with lowercase name")
	}
	if service2 == nil {
		t.Error("Cloud Load Balancing service is nil with lowercase name")
	}
}
