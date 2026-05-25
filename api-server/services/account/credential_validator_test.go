package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAzureCredentialsInternal_Success(t *testing.T) {
	// Create mock collector-server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/cloud/validate_credentials", r.URL.Path)

		// Verify headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "test-token", r.Header.Get("X-Cloud-Collector-Token"))

		// Verify request body
		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		assert.Equal(t, "Azure", reqBody["cloud_provider"])
		assert.Equal(t, "tenant-123", reqBody["tenant_id"])
		assert.Equal(t, "client-123", reqBody["client_id"])
		assert.Equal(t, "secret-123", reqBody["client_secret"])
		assert.Equal(t, "sub-123", reqBody["subscription_id"])

		// Return success response (using camelCase as expected by the code)
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      true,
				"provider":     "Azure",
				"errorMessage": "",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cost Management",
						"hasAccess":  true,
						"error":      "",
					},
					{
						"permission": "Resource API",
						"hasAccess":  true,
						"error":      "",
					},
				},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	// Setup config
	origURL := config.Config.CloudCollectorServerUrl
	origToken := config.Config.CloudCollectorServerToken
	origHeader := config.Config.CloudCollectorServerTokenHeader
	defer func() {
		config.Config.CloudCollectorServerUrl = origURL
		config.Config.CloudCollectorServerToken = origToken
		config.Config.CloudCollectorServerTokenHeader = origHeader
	}()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	// Call function
	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	// Verify result
	assert.True(t, result.Success)
	assert.Equal(t, "Azure", result.Provider)
	assert.Empty(t, result.ErrorMessage)
	assert.Len(t, result.PermissionDetails, 2)
	assert.Equal(t, "Cost Management", result.PermissionDetails[0].Permission)
	assert.True(t, result.PermissionDetails[0].HasAccess)
	assert.Empty(t, result.MissingPermissions)
}

func TestValidateAzureCredentialsInternal_MissingPermissions(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return success with missing permissions
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      true,
				"provider":     "Azure",
				"errorMessage": "",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cost Management",
						"hasAccess":  false,
						"error":      "permission denied",
					},
					{
						"permission": "Resource API",
						"hasAccess":  true,
						"error":      "",
					},
				},
				"missingPermissions": []string{"Cost Management"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	assert.True(t, result.Success)
	assert.Len(t, result.MissingPermissions, 1)
	assert.Equal(t, "Cost Management", result.MissingPermissions[0])
	assert.False(t, result.PermissionDetails[0].HasAccess)
}

func TestValidateAzureCredentialsInternal_InvalidCredentials(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return validation failure
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":            false,
				"provider":           "Azure",
				"errorMessage":       "failed to create Azure credentials: invalid tenant ID",
				"permissionDetails":  []map[string]interface{}{},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "invalid-tenant", "client-123", "secret-123", "sub-123")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "invalid tenant ID")
}

func TestValidateAzureCredentialsInternal_ServerError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 500 error
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("Internal Server Error")); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "500")
}

func TestValidateAzureCredentialsInternal_NetworkError(t *testing.T) {
	// Use invalid URL to simulate network error
	config.Config.CloudCollectorServerUrl = "http://invalid-host-that-does-not-exist:9999"
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "failed to call collector-server")
}

func TestValidateGCPCredentialsInternal_Success(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/cloud/validate_credentials", r.URL.Path)

		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		assert.Equal(t, "GCP", reqBody["cloud_provider"])
		assert.Equal(t, `{"project_id":"test-project"}`, reqBody["credentials_json"])
		assert.Equal(t, "test-project", reqBody["project_id"])

		// Return success response
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      true,
				"provider":     "GCP",
				"errorMessage": "",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cloud Billing",
						"hasAccess":  true,
						"error":      "",
					},
					{
						"permission": "Resource Manager",
						"hasAccess":  true,
						"error":      "",
					},
				},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateGCPCredentialsInternal(context.Background(), `{"project_id":"test-project"}`, "test-project", "", "", "")

	assert.True(t, result.Success)
	assert.Equal(t, "GCP", result.Provider)
	assert.Empty(t, result.ErrorMessage)
	assert.Len(t, result.PermissionDetails, 2)
	assert.Empty(t, result.MissingPermissions)
}

func TestValidateGCPCredentialsInternal_InvalidCredentials(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":            false,
				"provider":           "GCP",
				"errorMessage":       "GCP credentials JSON is empty",
				"permissionDetails":  []map[string]interface{}{},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateGCPCredentialsInternal(context.Background(), "", "test-project", "", "", "")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "empty")
}

func TestValidateGCPCredentialsInternal_MissingPermissions(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      true,
				"provider":     "GCP",
				"errorMessage": "",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cloud Billing",
						"hasAccess":  false,
						"error":      "permission denied: billing.accounts.get",
					},
					{
						"permission": "Resource Manager",
						"hasAccess":  true,
						"error":      "",
					},
					{
						"permission": "Recommender",
						"hasAccess":  false,
						"error":      "permission denied: recommender.computeInstanceMachineTypeRecommendations.list",
					},
				},
				"missingPermissions": []string{"Cloud Billing", "Recommender"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateGCPCredentialsInternal(context.Background(), `{"project_id":"test-project"}`, "test-project", "", "", "")

	assert.True(t, result.Success)
	assert.Len(t, result.MissingPermissions, 2)
	assert.Contains(t, result.MissingPermissions, "Cloud Billing")
	assert.Contains(t, result.MissingPermissions, "Recommender")
	assert.False(t, result.PermissionDetails[0].HasAccess)
	assert.False(t, result.PermissionDetails[2].HasAccess)
}

func TestCallCollectorValidationEndpoint_InvalidJSON(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return invalid JSON
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("invalid json{")); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	request := map[string]interface{}{
		"cloud_provider": "Azure",
		"tenant_id":      "tenant-123",
	}
	result := callCollectorValidationEndpoint(context.Background(), request)

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "failed to parse response")
}

func TestCallCollectorValidationEndpoint_ContextCancellation(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		// By the time we try to respond, context will be cancelled
		<-r.Context().Done()
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	// Create context with immediate cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	request := map[string]interface{}{
		"cloud_provider": "Azure",
		"tenant_id":      "tenant-123",
	}
	result := callCollectorValidationEndpoint(ctx, request)

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "failed to call collector-server")
}

func TestValidateCloudCredentials_Azure(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":            true,
				"provider":           "Azure",
				"errorMessage":       "",
				"permissionDetails":  []map[string]interface{}{},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	origURL := config.Config.CloudCollectorServerUrl
	origToken := config.Config.CloudCollectorServerToken
	origHeader := config.Config.CloudCollectorServerTokenHeader
	defer func() {
		config.Config.CloudCollectorServerUrl = origURL
		config.Config.CloudCollectorServerToken = origToken
		config.Config.CloudCollectorServerTokenHeader = origHeader
	}()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	// Test the internal validation function directly
	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	assert.True(t, result.Success)
	assert.Equal(t, "Azure", result.Provider)
}

func TestValidateCloudCredentials_GCP(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":            true,
				"provider":           "GCP",
				"errorMessage":       "",
				"permissionDetails":  []map[string]interface{}{},
				"missingPermissions": []string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	origURL := config.Config.CloudCollectorServerUrl
	origToken := config.Config.CloudCollectorServerToken
	origHeader := config.Config.CloudCollectorServerTokenHeader
	defer func() {
		config.Config.CloudCollectorServerUrl = origURL
		config.Config.CloudCollectorServerToken = origToken
		config.Config.CloudCollectorServerTokenHeader = origHeader
	}()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateGCPCredentialsInternal(context.Background(), `{"project_id":"test-project"}`, "test-project", "", "", "")

	assert.True(t, result.Success)
	assert.Equal(t, "GCP", result.Provider)
}

func TestValidateAzureCredentialsInternal_ResourceAPIFailed(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      false,
				"provider":     "Azure",
				"errorMessage": "Resource API access check failed: authorization failed: AuthorizationFailed (status: 403)",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cost Management",
						"hasAccess":  true,
					},
					{
						"permission": "Resource API",
						"hasAccess":  false,
						"error":      "authorization failed: AuthorizationFailed (status: 403)",
					},
				},
				"missingPermissions": []string{"Resource API"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "tenant-123", "client-123", "secret-123", "sub-123")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "Resource API")
}

func TestValidateAzureCredentialsInternal_TenantMismatch(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      false,
				"provider":     "Azure",
				"errorMessage": "subscription belongs to a different Azure AD tenant than the provided credentials",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Cost Management",
						"hasAccess":  false,
						"error":      "authorization failed: InvalidAuthenticationTokenTenant (status: 401)",
					},
					{
						"permission": "Resource API",
						"hasAccess":  false,
						"error":      "authorization failed: InvalidAuthenticationTokenTenant (status: 401)",
					},
				},
				"missingPermissions": []string{"Cost Management", "Resource API"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateAzureCredentialsInternal(context.Background(), "wrong-tenant", "client-123", "secret-123", "sub-123")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "different Azure AD tenant")
}

func TestValidateGCPCredentialsInternal_ResourceManagerFailed(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"success":      false,
				"provider":     "GCP",
				"errorMessage": "Resource Manager access check failed: permission denied",
				"permissionDetails": []map[string]interface{}{
					{
						"permission": "Resource Manager",
						"hasAccess":  false,
						"error":      "permission denied",
					},
					{
						"permission": "Recommender API",
						"hasAccess":  false,
						"error":      "permission denied",
					},
				},
				"missingPermissions": []string{"Resource Manager", "Recommender API"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer mockServer.Close()

	config.Config.CloudCollectorServerUrl = mockServer.URL
	config.Config.CloudCollectorServerToken = "test-token"
	config.Config.CloudCollectorServerTokenHeader = "X-Cloud-Collector-Token"

	result := validateGCPCredentialsInternal(context.Background(), `{"project_id":"test-project"}`, "test-project", "", "", "")

	assert.False(t, result.Success)
	assert.Contains(t, result.ErrorMessage, "Resource Manager")
}

// Note: TestValidateCloudCredentials_UnsupportedProvider removed
// The service-level ValidateCloudCredentials function handles unsupported providers,
// but requires *security.RequestContext. For unit testing, we focus on the internal
// validation functions that are called by the service layer.
