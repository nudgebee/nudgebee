package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// Single auth contract: protected llm-server routes fail closed when
// LLM_SERVER_TOKEN is unset, require a matching header when configured,
// and still allow explicit public paths to bypass the gate.
func TestAuthHandlerMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevToken := config.Config.LlmServerToken
	prevHeader := config.Config.LlmServerTokenHeader
	t.Cleanup(func() {
		config.Config.LlmServerToken = prevToken
		config.Config.LlmServerTokenHeader = prevHeader
	})
	config.Config.LlmServerTokenHeader = "X-ACTION-TOKEN"

	build := func() *gin.Engine {
		r := gin.New()
		r.Use(authHandlerMiddleware())
		r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/api/v1/workspace/anything", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/api/admin/prompts/config", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.POST("/v1/llm-config/test-connection", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		return r
	}

	do := func(r *gin.Engine, method, path, headerVal string) int {
		req := httptest.NewRequest(method, path, nil)
		if headerVal != "" {
			req.Header.Set("X-ACTION-TOKEN", headerVal)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("token configured: matching header passes", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "GET", "/api/admin/prompts/config", "secret"))
		assert.Equal(t, http.StatusOK, do(r, "POST", "/v1/llm-config/test-connection", "secret"))
	})

	t.Run("token configured: missing header → 401", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/api/admin/prompts/config", ""))
	})

	t.Run("token configured: wrong header → 401", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/api/admin/prompts/config", "wrong"))
	})

	t.Run("token unset: protected routes fail closed", func(t *testing.T) {
		config.Config.LlmServerToken = ""
		r := build()
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/admin/prompts/config", ""))
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/admin/prompts/config", "anything"))
	})

	t.Run("health and workspace paths skip the gate regardless", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "GET", "/health", ""))
		assert.Equal(t, http.StatusOK, do(r, "GET", "/api/v1/workspace/anything", ""))
	})
}
