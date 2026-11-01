package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// Every non-exempt request requires LLM_SERVER_TOKEN. Missing server config
// returns 503; invalid request credentials return 401.
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
		r.GET("/swagger/doc.json", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/openapi.json", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
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

	t.Run("token unset: requests fail closed", func(t *testing.T) {
		config.Config.LlmServerToken = ""
		r := build()
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/admin/prompts/config", ""))
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/admin/prompts/config", "anything"))
	})

	t.Run("health, docs, and workspace paths skip the gate regardless", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "GET", "/health", ""))
		assert.Equal(t, http.StatusOK, do(r, "GET", "/swagger/doc.json", ""))
		assert.Equal(t, http.StatusOK, do(r, "GET", "/openapi.json", ""))
		assert.Equal(t, http.StatusOK, do(r, "GET", "/api/v1/workspace/anything", ""))
	})
}

// pprof can leak secrets held in memory and workspace pods can reach this
// port (#33912). These use the real router so they also pin that pprof is
// registered after the auth middleware — registered first, it bypassed auth.
func TestDebugEndpointsRequireServiceToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevToken := config.Config.LlmServerToken
	prevHeader := config.Config.LlmServerTokenHeader
	t.Cleanup(func() {
		config.Config.LlmServerToken = prevToken
		config.Config.LlmServerTokenHeader = prevHeader
	})
	config.Config.LlmServerTokenHeader = "X-ACTION-TOKEN"

	get := func(headers map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		newRouter().ServeHTTP(w, req)
		return w.Code
	}

	t.Run("token configured", func(t *testing.T) {
		config.Config.LlmServerToken = "secret"
		assert.Equal(t, http.StatusUnauthorized, get(nil), "no credentials")
		assert.Equal(t, http.StatusUnauthorized, get(map[string]string{"X-ACTION-TOKEN": "wrong"}), "wrong header token")
		assert.Equal(t, http.StatusUnauthorized, get(map[string]string{"Authorization": "Bearer wrong"}), "wrong bearer token")
		assert.Equal(t, http.StatusOK, get(map[string]string{"X-ACTION-TOKEN": "secret"}), "token header")
		assert.Equal(t, http.StatusOK, get(map[string]string{"Authorization": "Bearer secret"}), "bearer token")
	})

	t.Run("no token configured: debug endpoints refused", func(t *testing.T) {
		config.Config.LlmServerToken = ""
		assert.Equal(t, http.StatusForbidden, get(nil))
		assert.Equal(t, http.StatusForbidden, get(map[string]string{"Authorization": "Bearer anything"}))
	})
}
