package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/prompts"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

func init() {
	prompts.InitializeGlobalLoader()
}

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tracer := otel.Tracer("test")
	meter := otel.Meter("test")
	handlePromptsApis(r, tracer, meter)
	return r
}

func TestAdminAuthMiddleware_ValidToken(t *testing.T) {
	r := setupTestRouter()

	// Set a test token
	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/config?name=test&category=agents", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should not be 401
	assert.NotEqual(t, 401, w.Code)
}

func TestAdminAuthMiddleware_InvalidToken(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/config?name=test&category=agents", nil)
	req.Header.Set("Authorization", "wrong-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestAdminAuthMiddleware_MissingToken(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/config?name=test&category=agents", nil)
	// No Authorization header

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
}

func TestGetPromptConfig_Success(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	// Note: This test requires the prompt loader to be initialized
	// In real tests, you'd mock the loader or ensure it's initialized

	req, _ := http.NewRequest("GET", "/api/admin/prompts/config?name=k8s_debug&category=agents&provider=default", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// The actual response depends on whether the loader is initialized
	// This test mainly verifies the endpoint exists and auth works
	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestGetPromptConfig_MissingParameters(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	tests := []struct {
		name string
		url  string
	}{
		{"missing name", "/api/admin/prompts/config?category=agents"},
		{"missing category", "/api/admin/prompts/config?name=test"},
		{"missing both", "/api/admin/prompts/config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.url, nil)
			req.Header.Set("Authorization", "test-admin-token")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, 400, w.Code)
		})
	}
}

func TestCreateExperiment_InvalidPayload(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	// Invalid JSON
	req, _ := http.NewRequest("POST", "/api/admin/prompts/experiments", bytes.NewBufferString("{invalid json"))
	req.Header.Set("Authorization", "test-admin-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestCreateExperiment_MissingFields(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	payload := map[string]interface{}{
		"name": "test_exp",
		// Missing required fields
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/api/admin/prompts/experiments", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "test-admin-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestUpdateExperimentAccounts_InvalidAction(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	payload := map[string]interface{}{
		"action":   "invalid_action",
		"accounts": []string{"account-1"},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("PATCH", "/api/admin/prompts/experiments/test_exp/accounts", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "test-admin-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestClearCache_AllCache(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("POST", "/api/admin/prompts/cache/clear", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should succeed (may be 200 or 503 if DB not available)
	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestClearCache_ByPrompt(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("POST", "/api/admin/prompts/cache/clear?prompt_name=k8s_debug&category=agents", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)

	if w.Code == 200 {
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.True(t, resp["success"].(bool))
		assert.Equal(t, "k8s_debug", resp["prompt_name"])
	}
}

func TestClearCache_ByAccount(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("POST", "/api/admin/prompts/cache/clear?account_id=test-account", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)

	if w.Code == 200 {
		var resp map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.True(t, resp["success"].(bool))
		assert.Equal(t, "test-account", resp["account_id"])
	}
}

func TestListActiveExperiments(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/experiments/active", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should succeed or return 503 if DB not available
	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestListActiveExperiments_WithFilters(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/experiments/active?prompt_name=k8s_debug&category=agents", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestDisableExperiment_MissingName(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("POST", "/api/admin/prompts/experiments//disable", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should be 400 or 404 due to empty name
	assert.True(t, w.Code == 400 || w.Code == 404)
}

func TestGetAuditLogs(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/audit", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should succeed or return 503 if DB not available
	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestGetAuditLogs_WithFilters(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	req, _ := http.NewRequest("GET", "/api/admin/prompts/audit?prompt_name=k8s_debug&limit=50", nil)
	req.Header.Set("Authorization", "test-admin-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, 401, w.Code)
	assert.NotEqual(t, 404, w.Code)
}

func TestUpdateActiveVersion_InvalidPayload(t *testing.T) {
	r := setupTestRouter()

	config.Config.LlmServerToken = "test-admin-token"
	config.Config.LlmServerTokenHeader = "Authorization"

	// Missing required fields
	payload := map[string]interface{}{
		"prompt_name": "k8s_debug",
		// Missing category and new_version
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/api/admin/prompts/config/version", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "test-admin-token")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestParseInt_Valid(t *testing.T) {
	result, err := parseInt("123")
	assert.NoError(t, err)
	assert.Equal(t, 123, result)
}

func TestParseInt_Invalid(t *testing.T) {
	_, err := parseInt("abc")
	assert.Error(t, err)
}

func TestGetUserFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Test with x-user-id header
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("x-user-id", "test-user-1")
	user := getUserFromContext(c)
	assert.Equal(t, "test-user-1", user)

	// Test with x-hasura-user-id header
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	c.Request.Header.Set("x-hasura-user-id", "test-user-2")
	user = getUserFromContext(c)
	assert.Equal(t, "test-user-2", user)

	// Test with no headers (should default to "admin")
	c.Request, _ = http.NewRequest("GET", "/test", nil)
	user = getUserFromContext(c)
	assert.Equal(t, "admin", user)
}
