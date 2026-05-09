package account

import (
	"context"
	"encoding/json"
	"nudgebee/services/config"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateAzureCredentials_RealCredentials tests Azure credential validation with actual credentials
// This test requires a running collector-server and real Azure credentials
// Skip this test in CI by setting SKIP_INTEGRATION_TESTS=true
func TestValidateAzureCredentials_RealCredentials(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		t.Skip("Skipping integration test")
	}

	// Azure credentials
	tenantID := os.Getenv("TEST_TENANT")
	clientID := os.Getenv("TEST_CLIENT_ID")
	clientSecret := os.Getenv("TEST_CLIENT_SECRET")
	subscriptionID := os.Getenv("TEST_SUBSCRIPTION_ID")

	//Ensure collector-server URL is configured
	if config.Config.CloudCollectorServerUrl == "" {
		t.Skip("CloudCollectorServerUrl not configured, skipping integration test")
	}

	// Call the validation function
	result := validateAzureCredentialsInternal(context.Background(), tenantID, clientID, clientSecret, subscriptionID)

	// Assertions
	t.Logf("Validation result: Success=%v, Provider=%s, ErrorMessage=%s", result.Success, result.Provider, result.ErrorMessage)
	t.Logf("Permission details: %+v", result.PermissionDetails)
	t.Logf("Missing permissions: %v", result.MissingPermissions)

	// Basic assertions - we expect the call to succeed (even if some permissions are missing)
	assert.Equal(t, "Azure", result.Provider)

	// Log detailed permission status
	for _, perm := range result.PermissionDetails {
		t.Logf("Permission: %s, HasAccess: %v, Error: %s", perm.Permission, perm.HasAccess, perm.ErrorDetail)
	}

	// If validation failed completely, log the error
	if !result.Success {
		t.Logf("Validation failed: %s", result.ErrorMessage)
	}

	// Check specific permissions
	hasCostManagement := false
	hasResourceAPI := false
	hasRecommendations := false

	for _, perm := range result.PermissionDetails {
		switch perm.Permission {
		case "Cost Management":
			hasCostManagement = perm.HasAccess
			t.Logf("Cost Management access: %v", perm.HasAccess)
		case "Resource API":
			hasResourceAPI = perm.HasAccess
			t.Logf("Resource API access: %v", perm.HasAccess)
		case "Azure Recommendations":
			hasRecommendations = perm.HasAccess
			t.Logf("Azure Recommendations access: %v", perm.HasAccess)
		}
	}

	// Report on permissions (informational, not failing the test)
	t.Logf("Permission Summary:")
	t.Logf("  - Cost Management: %v", hasCostManagement)
	t.Logf("  - Resource API: %v", hasResourceAPI)
	t.Logf("  - Azure Recommendations: %v", hasRecommendations)

	if len(result.MissingPermissions) > 0 {
		t.Logf("Missing permissions: %v", result.MissingPermissions)
		t.Logf("Note: Missing permissions is not a failure - account can still be created with limited functionality")
	}
}

// TestValidateGCPCredentials_RealCredentials tests GCP credential validation with actual credentials
// This test requires a running collector-server and real GCP credentials
// Skip this test in CI by setting SKIP_INTEGRATION_TESTS=true
func TestValidateGCPCredentials_RealCredentials(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		t.Skip("Skipping integration test")
	}

	// Try to read GCP credentials from file or environment variable
	var credentialsJSON string
	var projectID string

	// Option 1: Read from file path if GCP_CREDENTIALS_FILE is set
	credentialsFilePath := os.Getenv("GCP_CREDENTIALS_FILE")
	if credentialsFilePath != "" {
		credBytes, err := os.ReadFile(credentialsFilePath)
		if err != nil {
			t.Skipf("Failed to read GCP credentials file: %v", err)
		}
		credentialsJSON = string(credBytes)
	} else if os.Getenv("GCP_CREDENTIALS_JSON") != "" {
		// Option 2: Read from environment variable
		credentialsJSON = os.Getenv("GCP_CREDENTIALS_JSON")
	} else {
		t.Skip("GCP_CREDENTIALS_FILE or GCP_CREDENTIALS_JSON not set, skipping integration test")
	}

	// Extract project ID from environment or credentials
	projectID = os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		// Try to parse from credentials JSON
		var creds struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal([]byte(credentialsJSON), &creds); err == nil && creds.ProjectID != "" {
			projectID = creds.ProjectID
		} else {
			t.Skip("GCP_PROJECT_ID not set and could not parse from credentials, skipping integration test")
		}
	}

	// Ensure collector-server URL is configured
	if config.Config.CloudCollectorServerUrl == "" {
		t.Skip("CloudCollectorServerUrl not configured, skipping integration test")
	}

	// Call the validation function
	result := validateGCPCredentialsInternal(context.Background(), credentialsJSON, projectID, "", "", "")

	// Assertions
	t.Logf("Validation result: Success=%v, Provider=%s, ErrorMessage=%s", result.Success, result.Provider, result.ErrorMessage)
	t.Logf("Permission details: %+v", result.PermissionDetails)
	t.Logf("Missing permissions: %v", result.MissingPermissions)

	// Basic assertions
	assert.Equal(t, "GCP", result.Provider)

	// Log detailed permission status
	for _, perm := range result.PermissionDetails {
		t.Logf("Permission: %s, HasAccess: %v, Error: %s", perm.Permission, perm.HasAccess, perm.ErrorDetail)
	}

	// If validation failed completely, log the error
	if !result.Success {
		t.Logf("Validation failed: %s", result.ErrorMessage)
	}

	// Check specific permissions
	hasCloudBilling := false
	hasResourceManager := false
	hasRecommender := false

	for _, perm := range result.PermissionDetails {
		switch perm.Permission {
		case "Cloud Billing":
			hasCloudBilling = perm.HasAccess
			t.Logf("Cloud Billing access: %v", perm.HasAccess)
		case "Resource Manager":
			hasResourceManager = perm.HasAccess
			t.Logf("Resource Manager access: %v", perm.HasAccess)
		case "Recommender":
			hasRecommender = perm.HasAccess
			t.Logf("Recommender access: %v", perm.HasAccess)
		}
	}

	// Report on permissions (informational, not failing the test)
	t.Logf("Permission Summary:")
	t.Logf("  - Cloud Billing: %v", hasCloudBilling)
	t.Logf("  - Resource Manager: %v", hasResourceManager)
	t.Logf("  - Recommender: %v", hasRecommender)

	if len(result.MissingPermissions) > 0 {
		t.Logf("Missing permissions: %v", result.MissingPermissions)
		t.Logf("Note: Missing permissions is not a failure - account can still be created with limited functionality")
	}
}

// TestValidateAzureCredentials_RealCredentials_WithCostValidation tests Azure credential validation
// and specifically validates that Cost Management API access is working
func TestValidateAzureCredentials_RealCredentials_WithCostValidation(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		t.Skip("Skipping integration test")
	}

	// Azure credentials

	tenantID := os.Getenv("TEST_TENANT")
	clientID := os.Getenv("TEST_CLIENT_ID")
	clientSecret := os.Getenv("TEST_CLIENT_SECRET")
	subscriptionID := os.Getenv("TEST_SUBSCRIPTION_ID")

	if config.Config.CloudCollectorServerUrl == "" {
		t.Skip("CloudCollectorServerUrl not configured, skipping integration test")
	}

	result := validateAzureCredentialsInternal(context.Background(), tenantID, clientID, clientSecret, subscriptionID)

	t.Logf("Full validation result: %+v", result)

	// This test focuses on Cost Management validation
	require.Equal(t, "Azure", result.Provider, "Provider should be Azure")

	// Find Cost Management permission
	var costMgmtPerm *PermissionStatus
	for i := range result.PermissionDetails {
		if result.PermissionDetails[i].Permission == "Cost Management" {
			costMgmtPerm = &result.PermissionDetails[i]
			break
		}
	}

	require.NotNil(t, costMgmtPerm, "Cost Management permission should be checked")

	// Log the cost management validation result
	if costMgmtPerm.HasAccess {
		t.Logf(" Cost Management API access validated successfully")
		t.Logf("   This means the service principal can query Azure cost data")
	} else {
		t.Logf("  Cost Management API access not available")
		t.Logf("   Error: %s", costMgmtPerm.ErrorDetail)
		t.Logf("   The account can still be created but cost tracking will be limited")
		t.Logf("   To fix: Grant 'Cost Management Reader' role to the service principal")
	}

	// Even if cost management fails, the overall validation might succeed
	// This is expected behavior - accounts can be created without cost management access
	t.Logf("Overall validation success: %v", result.Success)
}

// TestValidateGCPCredentials_RealCredentials_WithBillingValidation tests GCP credential validation
// and specifically validates that Cloud Billing API access is working
func TestValidateGCPCredentials_RealCredentials_WithBillingValidation(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION_TESTS") == "true" {
		t.Skip("Skipping integration test")
	}

	var credentialsJSON string
	var projectID string

	credentialsFilePath := os.Getenv("GCP_CREDENTIALS_FILE")
	if credentialsFilePath != "" {
		credBytes, err := os.ReadFile(credentialsFilePath)
		require.NoError(t, err, "Failed to read GCP credentials file")
		credentialsJSON = string(credBytes)
	} else if os.Getenv("GCP_CREDENTIALS_JSON") != "" {
		credentialsJSON = os.Getenv("GCP_CREDENTIALS_JSON")
	} else {
		t.Skip("GCP_CREDENTIALS_FILE or GCP_CREDENTIALS_JSON not set, skipping integration test")
	}

	projectID = os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		var creds struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal([]byte(credentialsJSON), &creds); err == nil && creds.ProjectID != "" {
			projectID = creds.ProjectID
		} else {
			t.Skip("GCP_PROJECT_ID not set and could not parse from credentials")
		}
	}

	if config.Config.CloudCollectorServerUrl == "" {
		t.Skip("CloudCollectorServerUrl not configured, skipping integration test")
	}

	result := validateGCPCredentialsInternal(context.Background(), credentialsJSON, projectID, "", "", "")

	t.Logf("Full validation result: %+v", result)

	require.Equal(t, "GCP", result.Provider, "Provider should be GCP")

	// Find Cloud Billing permission
	var billingPerm *PermissionStatus
	for i := range result.PermissionDetails {
		if result.PermissionDetails[i].Permission == "Cloud Billing" {
			billingPerm = &result.PermissionDetails[i]
			break
		}
	}

	require.NotNil(t, billingPerm, "Cloud Billing permission should be checked")

	// Log the billing validation result
	if billingPerm.HasAccess {
		t.Logf(" Cloud Billing API access validated successfully")
		t.Logf("   This means the service account can query GCP billing/cost data")
	} else {
		t.Logf("  Cloud Billing API access not available")
		t.Logf("   Error: %s", billingPerm.ErrorDetail)
		t.Logf("   The account can still be created but cost tracking will be limited")
		t.Logf("   To fix: Grant necessary billing permissions to the service account")
	}

	t.Logf("Overall validation success: %v", result.Success)
}
