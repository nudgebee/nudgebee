//go:build integration
// +build integration

package gcloud

import (
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCPAlertPoliciesIntegration tests fetching alert policies from a real GCP account
// Run with: go test -tags=integration -v -run TestGCPAlertPoliciesIntegration
//
// Prerequisites:
// 1. Set environment variable: GCP_PROJECT_ID=your-project-id OR hardcode below
// 2. Set environment variable: GCP_CREDENTIALS_JSON=path/to/service-account.json OR hardcode below
// 3. Ensure the service account has roles/monitoring.viewer permission
// 4. Create at least one alert policy in GCP Cloud Monitoring Console
func TestGCPAlertPoliciesIntegration(t *testing.T) {
	// Option 1: Use environment variables
	projectID := os.Getenv("GCP_PROJECT_ID")
	credsPath := os.Getenv("GCP_CREDENTIALS_JSON")

	config.Config.NudgebeeEncryptionKey = "3030303030303030303030303030303030303030303030303030303030303030"

	// Option 2: Hardcode for quick testing (comment out if using env vars)
	if projectID == "" {
		projectID = "nudgebee-dev" // Replace with your test project ID
	}
	if credsPath == "" {
		credsPath = "/path/to/creds.json" // Replace with your creds path
	}

	if projectID == "" || credsPath == "" {
		t.Skip("Skipping integration test: GCP_PROJECT_ID and GCP_CREDENTIALS_JSON required")
	}

	// Read credentials file
	credsJSON, err := os.ReadFile(credsPath)
	require.NoError(t, err, "Failed to read GCP credentials file")

	// Create test account
	accessSecret := string(credsJSON)
	account := providers.Account{
		AccountNumber: projectID,
		CloudProvider: "GCP",
		AccessSecret:  &accessSecret,
	}

	// Create context
	ctx := security.NewRequestContextForSuperAdmin()
	providerCtx := providers.NewCloudProviderContext(ctx.GetContext())

	// Test ListEventRules (fetch alert policies)
	t.Run("ListEventRules", func(t *testing.T) {
		provider := &gcloudProvider{}
		result, err := provider.ListEventRules(providerCtx, account)

		assert.NoError(t, err, "ListEventRules should not return error")
		assert.NotNil(t, result, "Result should not be nil")

		t.Logf("Found %d alert policies", len(result.Items))

		if len(result.Items) > 0 {
			// Validate first policy structure
			policy := result.Items[0]
			assert.NotEmpty(t, policy.Name, "Policy name should not be empty")
			assert.Equal(t, "GCP_Metric_Alert", policy.Source, "Source should be GCP_Metric_Alert")
			assert.NotEmpty(t, policy.Expr, "Expr (policy JSON) should not be empty")
			assert.NotNil(t, policy.Labels, "Labels should not be nil")

			// Check required labels
			assert.Contains(t, policy.Labels, "gcp_project")
			assert.Contains(t, policy.Labels, "gcp_policy_name")
			assert.Contains(t, policy.Labels, "gcp_service_name")

			t.Logf("Sample policy: %s (Service: %s)", policy.Name, policy.Category)
		} else {
			t.Log("No alert policies found - create some in GCP Console first")
		}
	})

	// Test ListEvents (fetch alert incidents via Incident API)
	t.Run("ListEvents", func(t *testing.T) {
		provider := &gcloudProvider{}

		query := providers.ListEventRequest{}

		result, err := provider.ListEvents(providerCtx, account, query)

		assert.NoError(t, err, "ListEvents should not return error")
		assert.NotNil(t, result, "Result should not be nil")

		t.Logf("Found %d alert incidents", len(result.Items))

		// Validate incident structure if any firing alerts exist
		if len(result.Items) > 0 {
			incident := result.Items[0]

			// Verify incident source
			assert.Equal(t, "GCP_Metric_Alert", incident.EventSource,
				"Event source should be GCP_Metric_Alert")

			// Verify required fields
			assert.NotEmpty(t, incident.EventId, "Event ID should not be empty")
			assert.NotEmpty(t, incident.Title, "Title should not be empty")
			assert.NotEmpty(t, incident.Description, "Description should not be empty")

			// Verify status is either FIRING or RESOLVED
			assert.Contains(t, []providers.EventStatus{
				providers.EventStatusFiring,
				providers.EventStatusResolved,
			}, incident.EventStatus, "Status should be FIRING or RESOLVED")

			// Verify labels exist
			assert.NotNil(t, incident.Labels, "Labels should not be nil")
			assert.Contains(t, incident.Labels, "gcp_project_id", "Should have gcp_project_id label")
			assert.Contains(t, incident.Labels, "gcp_policy_name", "Should have gcp_policy_name label")
			assert.Contains(t, incident.Labels, "gcp_incident_id", "Should have gcp_incident_id label")

			t.Logf("Sample incident: %s (Status: %s, Severity: %s)",
				incident.Title,
				incident.EventStatus,
				incident.EventSeverity)

			// Log incident details for debugging
			t.Logf("Incident ID: %s", incident.EventId)
			t.Logf("Policy: %s", incident.Labels["gcp_policy_name"])
			t.Logf("Resource Type: %s", incident.ResourceType)
			t.Logf("Resource ID: %s", incident.ResourceId)
			t.Logf("Service: %s", incident.ResourceServiceName)
		} else {
			t.Log("No firing alert incidents found - this is normal if no alerts are currently triggered")
			t.Log("To test with firing alerts:")
			t.Log("1. Create an alert policy in GCP Cloud Monitoring Console")
			t.Log("2. Set a threshold that will trigger (e.g., CPU > 0%)")
			t.Log("3. Wait for alert to fire")
			t.Log("4. Run this test again")
		}
	})

	// Test ListEvents with time range filter
	t.Run("ListEventsWithTimeRange", func(t *testing.T) {
		provider := &gcloudProvider{}

		// Query for incidents in the last 7 days
		endTime := time.Now()
		startTime := endTime.Add(-7 * 24 * time.Hour)

		query := providers.ListEventRequest{
			StartDate: &startTime,
			EndDate:   &endTime,
		}

		result, err := provider.ListEvents(providerCtx, account, query)

		assert.NoError(t, err, "ListEvents with time range should not return error")
		assert.NotNil(t, result, "Result should not be nil")

		t.Logf("Found %d alert incidents in the last 7 days", len(result.Items))

		if len(result.Items) > 0 {
			// Verify all incidents are within the time range
			for _, incident := range result.Items {
				assert.True(t,
					incident.Date.After(startTime) || incident.Date.Equal(startTime),
					"Incident date should be after start time")
				assert.True(t,
					incident.Date.Before(endTime) || incident.Date.Equal(endTime),
					"Incident date should be before end time")
			}
		}
	})
}

// TestGCPAlertPolicyAttachment tests attaching alert policies to resources
// Run with: go test -tags=integration -v -run TestGCPAlertPolicyAttachment
func TestGCPAlertPolicyAttachment(t *testing.T) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	credsPath := os.Getenv("GCP_CREDENTIALS_JSON")

	if projectID == "" {
		projectID = "nudgebee-dev"
	}
	if credsPath == "" {
		credsPath = "/path/to/creds.json"
	}

	if projectID == "" || credsPath == "" {
		t.Skip("Skipping integration test: GCP_PROJECT_ID and GCP_CREDENTIALS_JSON required")
	}

	credsJSON, err := os.ReadFile(credsPath)
	require.NoError(t, err)

	accessSecret := string(credsJSON)
	account := providers.Account{
		AccountNumber: projectID,
		CloudProvider: "GCP",
		AccessSecret:  &accessSecret,
	}

	ctx := security.NewRequestContextForSuperAdmin()
	providerCtx := providers.NewCloudProviderContext(ctx.GetContext())

	t.Run("AttachPoliciesToComputeInstances", func(t *testing.T) {
		provider := &gcloudProvider{}

		// Fetch Compute Engine resources
		query := providers.ListResourceRequest{
			ServiceName: "Compute Engine",
			Regions:     []string{"us-central1"},
		}

		result, err := provider.ListResources(providerCtx, account, query)
		assert.NoError(t, err)

		t.Logf("Found %d Compute Engine instances", len(result.Items))

		// Check if any resources have AlertPolicies attached
		resourcesWithPolicies := 0
		for _, resource := range result.Items {
			if policies, ok := resource.Meta["AlertPolicies"]; ok {
				if policyArray, ok := policies.([]interface{}); ok && len(policyArray) > 0 {
					resourcesWithPolicies++
					t.Logf("Resource %s has %d alert policies attached", resource.Name, len(policyArray))
				}
			}
		}

		t.Logf("%d resources have alert policies attached", resourcesWithPolicies)

		if resourcesWithPolicies == 0 && len(result.Items) > 0 {
			t.Log("No alert policies attached - ensure you have alert policies configured for Compute Engine metrics")
		}
	})
}

// TestGCPAlarmChecker tests the alarm missing detection logic
// Run with: go test -tags=integration -v -run TestGCPAlarmChecker
func TestGCPAlarmChecker(t *testing.T) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	credsPath := os.Getenv("GCP_CREDENTIALS_JSON")

	if projectID == "" {
		projectID = "nudgebee-dev"
	}
	if credsPath == "" {
		credsPath = "/path/to/creds.json"
	}

	if projectID == "" || credsPath == "" {
		t.Skip("Skipping integration test: GCP_PROJECT_ID and GCP_CREDENTIALS_JSON required")
	}

	credsJSON, err := os.ReadFile(credsPath)
	require.NoError(t, err)

	accessSecret := string(credsJSON)
	account := providers.Account{
		AccountNumber: projectID,
		CloudProvider: "GCP",
		AccessSecret:  &accessSecret,
	}

	ctx := security.NewRequestContextForSuperAdmin()
	providerCtx := providers.NewCloudProviderContext(ctx.GetContext())

	provider := &gcloudProvider{}

	// Fetch resources with alert policies attached
	query := providers.ListResourceRequest{
		ServiceName: "Compute Engine",
		Regions:     []string{"us-central1"},
	}

	result, err := provider.ListResources(providerCtx, account, query)
	require.NoError(t, err)

	if len(result.Items) == 0 {
		t.Skip("No Compute Engine instances found")
	}

	// Test alarm checker for each resource
	for _, resource := range result.Items {
		t.Run("Resource_"+resource.Name, func(t *testing.T) {
			// Create a test alarm template (CPU utilization)
			template := providers.AlarmTemplate{
				Configuration: providers.AlarmConfiguration{
					Namespace:  "compute.googleapis.com",
					MetricName: "CPUUtilization",
					Statistic:  "Average",
					Period:     300,
				},
			}

			// Build resource filter
			resourceFilter := GetResourceFilterForService(ServiceNameCompute, resource.Name)

			// Check if alarm is missing
			isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
			assert.NoError(t, err)

			if isMissing {
				t.Logf("MISSING: CPU utilization alarm not found for instance %s", resource.Name)
			} else {
				t.Logf("OK: CPU utilization alarm exists for instance %s", resource.Name)
			}

			// Log alert policies if attached
			if policies, ok := resource.Meta["AlertPolicies"]; ok {
				if policyArray, ok := policies.([]interface{}); ok {
					t.Logf("Resource has %d alert policies", len(policyArray))
				}
			}
		})
	}
}
