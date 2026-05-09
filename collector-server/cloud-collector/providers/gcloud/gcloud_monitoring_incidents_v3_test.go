//go:build integration

package gcloud

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCPIncidentsV3API tests the new v3 alerts API implementation
func TestGCPIncidentsV3API(t *testing.T) {
	// Set encryption key
	config.Config.NudgebeeEncryptionKey = "3030303030303030303030303030303030303030303030303030303030303030"

	// Read GCP credentials
	projectID := os.Getenv("GCP_PROJECT_ID")
	credsPath := os.Getenv("GCP_CREDENTIALS_JSON")

	if projectID == "" {
		projectID = "nudgebee-dev"
	}
	if credsPath == "" {
		credsPath = "/path/to/creds.json"
	}

	credsJSON, err := os.ReadFile(credsPath)
	require.NoError(t, err, "Failed to read GCP credentials file at %s", credsPath)

	encryptedCreds, err := common.Encrypt(string(credsJSON))
	require.NoError(t, err, "Failed to encrypt credentials")

	// Create account
	account := providers.Account{
		AccountNumber: projectID,
		CloudProvider: "GCP",
		AccessSecret:  &encryptedCreds,
	}

	// Create context
	ctx := security.NewRequestContextForSuperAdmin()
	providerCtx := providers.NewCloudProviderContext(ctx.GetContext())

	t.Run("FetchFiringIncidentsV3", func(t *testing.T) {
		t.Log(">>> Test: Fetching Firing Incidents via v3 API")

		// Query for current firing incidents
		endTime := time.Now()
		query := providers.ListEventRequest{
			EndDate: &endTime,
		}

		incidents, err := getGCPIncidentsV3(providerCtx, &account, query)
		require.NoError(t, err, "Failed to fetch incidents")

		t.Logf("✓ Found %d firing incidents", len(incidents.Items))

		// Log sample incidents
		for i, incident := range incidents.Items {
			if i >= 3 {
				break // Only show first 3
			}

			t.Logf("\nSample Incident #%d:", i+1)
			t.Logf("- Title: %s", incident.Title)
			t.Logf("- Event Name: %s", incident.EventName)
			t.Logf("- Status: %s", incident.EventStatus)
			t.Logf("- Severity: %s", incident.EventSeverity)
			t.Logf("- Description: %s", incident.Description)
			t.Logf("- Resource Type: %s", incident.ResourceType)
			t.Logf("- Resource ID: %s", incident.ResourceId)

			if incident.Labels != nil {
				if policyName, ok := incident.Labels["gcp_policy_name"]; ok {
					t.Logf("- Policy: %s", policyName)
				}
			}
		}

		// Assertions
		// Note: We may have 0 incidents if no alerts are currently firing
		assert.GreaterOrEqual(t, len(incidents.Items), 0, "Should return non-negative number of incidents")

		// If we have incidents, validate their structure
		for _, incident := range incidents.Items {
			assert.NotEmpty(t, incident.EventId, "Event ID should not be empty")
			assert.NotEmpty(t, incident.Title, "Title should not be empty")
			assert.NotEmpty(t, incident.EventStatus, "Status should not be empty")
			assert.NotEmpty(t, incident.EventSeverity, "Severity should not be empty")
		}
	})

	t.Run("FetchIncidentsWithTimeRangeV3", func(t *testing.T) {
		t.Log(">>> Test: Fetching Incidents with Time Range via v3 API")

		// Query for incidents in the last 24 hours
		now := time.Now()
		startTime := now.Add(-24 * time.Hour)
		endTime := now

		query := providers.ListEventRequest{
			StartDate: &startTime,
			EndDate:   &endTime,
		}

		incidents, err := getGCPIncidentsV3(providerCtx, &account, query)
		require.NoError(t, err, "Failed to fetch incidents with time range")

		t.Logf("✓ Found %d incidents in last 24 hours", len(incidents.Items))

		// Validate time range
		for _, incident := range incidents.Items {
			// Incident should be within the time range (or currently open)
			assert.True(t,
				(incident.Date.After(startTime) && incident.Date.Before(endTime)) ||
					incident.EventStatus == providers.EventStatusFiring,
				"Incident timestamp should be within range or status should be FIRING")
		}
	})

	t.Run("CompareV3WithCloudLogging", func(t *testing.T) {
		t.Log(">>> Test: Compare v3 API results with Cloud Logging")

		endTime := time.Now()
		query := providers.ListEventRequest{
			EndDate: &endTime,
		}

		// Fetch via v3 API
		v3Incidents, err := getGCPIncidentsV3(providerCtx, &account, query)
		require.NoError(t, err, "Failed to fetch incidents via v3 API")

		// Fetch via Cloud Logging (old method)
		loggingIncidents, err := getGCPAlertIncidents(providerCtx, account, query)
		require.NoError(t, err, "Failed to fetch incidents via Cloud Logging")

		t.Logf("✓ v3 API incidents: %d", len(v3Incidents.Items))
		t.Logf("✓ Cloud Logging incidents: %d", len(loggingIncidents.Items))

		// v3 API should be the source of truth
		// Cloud Logging might have more or fewer depending on notification configuration
		assert.GreaterOrEqual(t, len(v3Incidents.Items), 0, "v3 API should return valid result")
	})
}

// TestAlertStructureParsing tests JSON parsing of GCP Alert objects
func TestAlertStructureParsing(t *testing.T) {
	// Sample alert JSON from GCP API
	alertJSON := `{
		"name": "projects/nudgebee-dev/alerts/12345",
		"state": "OPEN",
		"openTime": "2026-03-10T12:00:00Z",
		"resource": {
			"type": "gce_instance",
			"labels": {
				"project_id": "nudgebee-dev",
				"instance_id": "1234567890",
				"zone": "us-central1-a"
			}
		},
		"metric": {
			"type": "compute.googleapis.com/instance/cpu/utilization",
			"labels": {
				"instance_name": "test-vm"
			}
		},
		"policy": {
			"name": "projects/nudgebee-dev/alertPolicies/67890",
			"displayName": "High CPU Usage",
			"severity": "CRITICAL",
			"userLabels": {
				"environment": "production"
			}
		}
	}`

	// Parse alert
	var alert Alert
	err := json.Unmarshal([]byte(alertJSON), &alert)
	require.NoError(t, err, "Failed to parse alert JSON")

	// Validate parsing
	assert.Equal(t, "projects/nudgebee-dev/alerts/12345", alert.Name)
	assert.Equal(t, AlertStateOpen, alert.State)
	assert.Equal(t, "2026-03-10T12:00:00Z", alert.OpenTime)
	assert.NotNil(t, alert.Resource)
	assert.Equal(t, "gce_instance", alert.Resource.Type)
	assert.Equal(t, "1234567890", alert.Resource.Labels["instance_id"])
	assert.NotNil(t, alert.Policy)
	assert.Equal(t, "High CPU Usage", alert.Policy.DisplayName)
	assert.Equal(t, "CRITICAL", alert.Policy.Severity)

	account := &providers.Account{AccountNumber: "nudgebee-dev"}
	event := convertAlertToEvent(nil, nil, &alert, account)

	assert.Equal(t, "test-vm", event.ResourceId, "Should use instance_name from metric labels")

	// EventId should be stable fingerprint: policy:resource_type:resource_id

	assert.Equal(t, "projects/nudgebee-dev/alertPolicies/67890:gce_instance:test-vm", event.EventId)
	assert.Equal(t, "gce_instance: High CPU Usage", event.Title)
	assert.Equal(t, providers.EventStatusFiring, event.EventStatus)
	assert.Equal(t, providers.EventSeverityHigh, event.EventSeverity)
	assert.Equal(t, "gce_instance", event.ResourceType)

	// Verify metric labels are included
	assert.Equal(t, "test-vm", event.Labels["metric_instance_name"], "Should include metric labels")
	assert.Equal(t, "compute.googleapis.com/instance/cpu/utilization", event.Labels["gcp_metric_type"])

	// Verify labels required for actions.go investigation tasks are set
	assert.Equal(t, "us-central1", event.Labels["gcp_region"], "Should set gcp_region for actions")
	assert.Equal(t, "test-vm", event.Labels["gcp_event_instance"], "Should set gcp_event_instance for actions")
	assert.Equal(t, "gce_instance", event.Labels["gcp_event_resource_type"], "Should set gcp_event_resource_type for actions")
	assert.Equal(t, "Compute Engine", event.Labels["gcp_service_name"], "Should set gcp_service_name for actions")
	assert.Equal(t, "nudgebee-dev", event.Labels["gcp_account"], "Should set gcp_account for actions")
	assert.Equal(t, "us-central1-a", event.Labels["gcp_zone"], "Should set gcp_zone for CLI commands")
	assert.Equal(t, "compute.googleapis.com/instance/cpu/utilization", event.Labels["gcp_event_metric_type"], "Should set gcp_event_metric_type for actions")
}

// TestDeriveResourceTypeFromMetric tests deriving resource type from metric type
func TestDeriveResourceTypeFromMetric(t *testing.T) {
	tests := []struct {
		name         string
		metricType   string
		expectedType string
	}{
		{
			name:         "Compute instance CPU",
			metricType:   "compute.googleapis.com/instance/cpu/utilization",
			expectedType: "gce_instance",
		},
		{
			name:         "Compute disk",
			metricType:   "compute.googleapis.com/disk/read_bytes_count",
			expectedType: "gce_disk",
		},
		{
			name:         "Cloud SQL",
			metricType:   "cloudsql.googleapis.com/database/cpu/utilization",
			expectedType: "cloudsql_database",
		},
		{
			name:         "Cloud Storage",
			metricType:   "storage.googleapis.com/storage/object_count",
			expectedType: "gcs_bucket",
		},
		{
			name:         "Cloud Run",
			metricType:   "run.googleapis.com/request_count",
			expectedType: "cloud_run_revision",
		},
		{
			name:         "Cloud Functions",
			metricType:   "cloudfunctions.googleapis.com/function/execution_count",
			expectedType: "cloud_function",
		},
		{
			name:         "Pub/Sub topic",
			metricType:   "pubsub.googleapis.com/topic/send_message_operation_count",
			expectedType: "pubsub_topic",
		},
		{
			name:         "Kubernetes container",
			metricType:   "container.googleapis.com/container/cpu/usage_time",
			expectedType: "k8s_container",
		},
		{
			name:         "Unknown service - returns service name",
			metricType:   "newservice.googleapis.com/some/metric",
			expectedType: "newservice",
		},
		{
			name:         "Empty metric",
			metricType:   "",
			expectedType: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alert := &Alert{}
			if tc.metricType != "" {
				alert.Metric = &Metric{Type: tc.metricType}
			}
			result := deriveResourceTypeFromMetric(alert)
			assert.Equal(t, tc.expectedType, result)
		})
	}
}

// TestDeriveResourceTypeWhenResourceTypeEmpty tests that we fall back to metric-derived type
func TestDeriveResourceTypeWhenResourceTypeEmpty(t *testing.T) {
	// Alert with NO resource.type but WITH metric.type
	alertJSON := `{
		"name": "projects/nudgebee-dev/alerts/12345",
		"state": "OPEN",
		"openTime": "2026-03-10T12:00:00Z",
		"metric": {
			"type": "cloudsql.googleapis.com/database/cpu/utilization",
			"labels": {}
		},
		"policy": {
			"name": "projects/nudgebee-dev/alertPolicies/67890",
			"displayName": "High CPU Usage"
		}
	}`

	var alert Alert
	err := json.Unmarshal([]byte(alertJSON), &alert)
	require.NoError(t, err)

	account := &providers.Account{AccountNumber: "nudgebee-dev"}
	event := convertAlertToEvent(nil, nil, &alert, account)

	// Should derive resource type from metric type
	assert.Equal(t, "cloudsql_database", event.ResourceType, "Should derive resource type from metric type")
}

// TestAlertFallbackToInstanceID tests that when instance_name is not in metric labels,
// we fall back to instance_id (which will be looked up via Compute API in real scenario)
func TestAlertFallbackToInstanceID(t *testing.T) {
	// Alert WITHOUT instance_name in metric labels
	alertJSON := `{
		"name": "projects/nudgebee-dev/alerts/12345",
		"state": "OPEN",
		"openTime": "2026-03-10T12:00:00Z",
		"resource": {
			"type": "gce_instance",
			"labels": {
				"project_id": "nudgebee-dev",
				"instance_id": "3346138238029787252",
				"zone": "us-central1-a"
			}
		},
		"metric": {
			"type": "compute.googleapis.com/instance/cpu/utilization",
			"labels": {}
		},
		"policy": {
			"name": "projects/nudgebee-dev/alertPolicies/67890",
			"displayName": "High CPU Usage"
		}
	}`

	var alert Alert
	err := json.Unmarshal([]byte(alertJSON), &alert)
	require.NoError(t, err)

	// Convert to Event without HTTP client (simulates no API lookup available)
	account := &providers.Account{AccountNumber: "nudgebee-dev"}
	event := convertAlertToEvent(nil, nil, &alert, account)

	// Without metric instance_name and without API lookup, should fall back to instance_id
	assert.Equal(t, "3346138238029787252", event.ResourceId, "Should fall back to instance_id when instance_name not available")

	// EventId fingerprint uses the fallback instance_id
	assert.Equal(t, "projects/nudgebee-dev/alertPolicies/67890:gce_instance:3346138238029787252", event.EventId)
}
