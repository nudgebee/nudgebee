package services_server

import (
	"encoding/json"
	"nudgebee/llm/security"
	"os"
	"testing"
)

// TestGetSourceCodeRepo_ArgoCD_SampleApp tests the actual deployed sample-app
// This test requires:
// 1. sample-app deployed in demo namespace via ArgoCD
// 2. ArgoCD integration configured for the account
// 3. Database access with workload data
func TestGetSourceCodeRepo_ArgoCD_SampleApp(t *testing.T) {
	// Skip in CI/CD - this is an integration test requiring actual infrastructure
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create request context
	// TODO: Replace with actual account ID from your system
	accountId := os.Getenv("TEST_ACCOUNT")
	ctx := &security.RequestContext{
		// Initialize with necessary context
	}

	// Test querying the frontend workload from sample-app
	options := SourceCodeAnnotationOptions{
		WorkloadName: "frontend",
		Namespace:    "demo",
	}

	t.Run("fetch source code from ArgoCD sample-app", func(t *testing.T) {
		result := GetSourceCodeRepo(ctx, accountId, options)

		// Convert to JSON for easy viewing
		resultJSON, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal result: %v", err)
		}

		t.Logf("GetSourceCodeRepo result:\n%s", string(resultJSON))

		// Verify ArgoCD fields are populated
		if result.ArgoCDApp == "" {
			t.Error("Expected ArgoCDApp to be populated")
		} else {
			t.Logf("✓ ArgoCD App: %s", result.ArgoCDApp)
		}

		if result.ArgoCDApp != "sample-app" {
			t.Errorf("Expected ArgoCDApp to be 'sample-app', got '%s'", result.ArgoCDApp)
		}

		if result.SyncStatus == "" {
			t.Error("Expected SyncStatus to be populated")
		} else {
			t.Logf("✓ Sync Status: %s", result.SyncStatus)
		}

		// Verify Helm chart information
		if result.HelmChartRepo == "" {
			t.Error("Expected HelmChartRepo to be populated")
		} else {
			t.Logf("✓ Helm Chart Repo: %s", result.HelmChartRepo)
		}

		expectedChartRepo := "https://open-telemetry.github.io/opentelemetry-helm-charts"
		if result.HelmChartRepo != expectedChartRepo {
			t.Errorf("Expected HelmChartRepo to be '%s', got '%s'", expectedChartRepo, result.HelmChartRepo)
		}

		if result.HelmChartName == "" {
			t.Error("Expected HelmChartName to be populated")
		} else {
			t.Logf("✓ Helm Chart Name: %s", result.HelmChartName)
		}

		if result.HelmChartName != "opentelemetry-demo" {
			t.Errorf("Expected HelmChartName to be 'opentelemetry-demo', got '%s'", result.HelmChartName)
		}

		// Verify values file information
		if len(result.ValuesFiles) == 0 {
			t.Error("Expected ValuesFiles to be populated")
		} else {
			t.Logf("✓ Values Files: %v", result.ValuesFiles)
		}

		if result.ValuesRepoURL == "" {
			t.Error("Expected ValuesRepoURL to be populated")
		} else {
			t.Logf("✓ Values Repo URL: %s", result.ValuesRepoURL)
		}

		expectedValuesRepo := "https://github.com/nudgebee/nudgebee.git"
		if result.ValuesRepoURL != expectedValuesRepo {
			t.Errorf("Expected ValuesRepoURL to be '%s', got '%s'", expectedValuesRepo, result.ValuesRepoURL)
		}

		if result.ValuesPath == "" {
			t.Error("Expected ValuesPath to be populated")
		} else {
			t.Logf("✓ Values Path: %s", result.ValuesPath)
		}

		expectedValuesPath := "deploy/kubernetes/sample-app"
		if result.ValuesPath != expectedValuesPath {
			t.Errorf("Expected ValuesPath to be '%s', got '%s'", expectedValuesPath, result.ValuesPath)
		}

		// Verify source indicator
		if result.Source != "argocd" && result.Source != "both" {
			t.Errorf("Expected Source to be 'argocd' or 'both', got '%s'", result.Source)
		} else {
			t.Logf("✓ Source: %s", result.Source)
		}

		// Verify target revision
		if result.TargetRevision == "" {
			t.Error("Expected TargetRevision to be populated")
		} else {
			t.Logf("✓ Target Revision: %s", result.TargetRevision)
		}

		// Log full values files array
		if len(result.ValuesFiles) > 0 {
			t.Logf("✓ Values Files Details:")
			for i, vf := range result.ValuesFiles {
				t.Logf("  [%d] %s", i, vf)
			}
		}
	})
}

// TestExtractArgoCDAppName tests the ArgoCD app name extraction logic
func TestExtractArgoCDAppName(t *testing.T) {
	tests := []struct {
		name        string
		trackingID  string
		expectedApp string
	}{
		{
			name:        "valid tracking ID",
			trackingID:  "sample-app:apps/Deployment:demo/frontend",
			expectedApp: "sample-app",
		},
		{
			name:        "empty tracking ID",
			trackingID:  "",
			expectedApp: "",
		},
		{
			name:        "tracking ID without colon",
			trackingID:  "sample-app",
			expectedApp: "sample-app",
		},
		{
			name:        "complex app name",
			trackingID:  "my-complex-app-name:argoproj.io/Application:argocd/test",
			expectedApp: "my-complex-app-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractArgoCDAppName(tt.trackingID)
			if result != tt.expectedApp {
				t.Errorf("extractArgoCDAppName(%s) = %s, want %s", tt.trackingID, result, tt.expectedApp)
			}
		})
	}
}

// TestGetSourceCodeFromArgoCD_Integration tests the ArgoCD API integration
// This requires actual ArgoCD integration to be configured
func TestGetSourceCodeFromArgoCD_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// TODO: Replace with actual account ID
	accountId := "your-account-id-here"
	appName := "sample-app"

	ctx := &security.RequestContext{
		// Initialize with necessary context
	}

	t.Run("fetch ArgoCD application details", func(t *testing.T) {
		result, err := GetSourceCodeFromArgoCD(ctx, accountId, appName)
		if err != nil {
			t.Fatalf("GetSourceCodeFromArgoCD failed: %v", err)
		}

		// Convert to JSON for debugging
		resultJSON, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("ArgoCD Response:\n%s", string(resultJSON))

		// Verify basic fields
		if result.ArgoCDApp != appName {
			t.Errorf("Expected ArgoCDApp=%s, got %s", appName, result.ArgoCDApp)
		}

		// Verify multi-source configuration
		if result.HelmChartRepo == "" {
			t.Error("Expected HelmChartRepo to be populated for multi-source app")
		}

		if result.ValuesRepoURL == "" {
			t.Error("Expected ValuesRepoURL to be populated for multi-source app")
		}

		if len(result.ValuesFiles) == 0 {
			t.Error("Expected ValuesFiles to contain at least one file")
		}

		// Log summary
		t.Log("=== ArgoCD Multi-Source Configuration ===")
		t.Logf("App Name: %s", result.ArgoCDApp)
		t.Logf("Helm Chart: %s/%s", result.HelmChartRepo, result.HelmChartName)
		t.Logf("Values Repo: %s", result.ValuesRepoURL)
		t.Logf("Values Path: %s", result.ValuesPath)
		t.Logf("Values Files: %v", result.ValuesFiles)
		t.Logf("Target Revision: %s", result.TargetRevision)
		t.Logf("Sync Status: %s", result.SyncStatus)
	})
}
