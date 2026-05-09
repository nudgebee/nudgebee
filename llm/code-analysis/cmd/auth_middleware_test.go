package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(authHandlerMiddleware())
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"info": "ok"})
	})
	router.POST("/execute", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"result": "ok"})
	})
	return router
}

func TestAuthMiddleware_HealthBypass(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "/health should bypass auth")
}

func TestAuthMiddleware_InfoBypass(t *testing.T) {
	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "/info should bypass auth")
}

func TestAuthMiddleware_RejectsWhenTokenNotConfigured(t *testing.T) {
	// Ensure NB_WORKSPACE_TOKEN is unset
	t.Setenv("NB_WORKSPACE_TOKEN", "")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodPost, "/execute", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"should reject requests when NB_WORKSPACE_TOKEN is not configured")
	assert.Contains(t, w.Body.String(), "workspace token not configured")
}

func TestAuthMiddleware_RejectsInvalidToken(t *testing.T) {
	t.Setenv("NB_WORKSPACE_TOKEN", "correct-token")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodPost, "/execute", nil)
	req.Header.Set("X-Workspace-Token", "wrong-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"should reject requests with invalid token")
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestAuthMiddleware_RejectsMissingToken(t *testing.T) {
	t.Setenv("NB_WORKSPACE_TOKEN", "correct-token")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodPost, "/execute", nil)
	// No X-Workspace-Token header
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"should reject requests with missing token header")
}

func TestAuthMiddleware_AllowsValidToken(t *testing.T) {
	t.Setenv("NB_WORKSPACE_TOKEN", "correct-token")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodPost, "/execute", nil)
	req.Header.Set("X-Workspace-Token", "correct-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"should allow requests with valid token")
	assert.Contains(t, w.Body.String(), "ok")
}

func TestAuthMiddleware_OptionsBypass(t *testing.T) {
	t.Setenv("NB_WORKSPACE_TOKEN", "correct-token")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodOptions, "/execute", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// OPTIONS should bypass auth entirely for CORS preflight
	assert.NotEqual(t, http.StatusUnauthorized, w.Code,
		"OPTIONS requests should bypass auth for CORS preflight")
	assert.NotEqual(t, http.StatusServiceUnavailable, w.Code,
		"OPTIONS requests should not be rejected")
}

func TestAuthMiddleware_OptionsBypassWithoutToken(t *testing.T) {
	t.Setenv("NB_WORKSPACE_TOKEN", "")

	router := setupTestRouter()

	req, _ := http.NewRequest(http.MethodOptions, "/execute", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// OPTIONS should bypass even when token is not configured
	assert.NotEqual(t, http.StatusServiceUnavailable, w.Code,
		"OPTIONS requests should bypass auth even without token configured")
}
