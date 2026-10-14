package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/collector/cloud/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthHandlerMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevToken := config.Config.CloudCollectorServerToken
	prevHeader := config.Config.CloudCollectorServerTokenHeader
	t.Cleanup(func() {
		config.Config.CloudCollectorServerToken = prevToken
		config.Config.CloudCollectorServerTokenHeader = prevHeader
	})
	config.Config.CloudCollectorServerTokenHeader = "X-ACTION-TOKEN"

	build := func() *gin.Engine {
		r := gin.New()
		r.Use(authHandlerMiddleware())
		r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/livez", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/debug/pprof", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/api/internal", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		return r
	}

	do := func(r *gin.Engine, path, headerVal string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if headerVal != "" {
			req.Header.Set("X-ACTION-TOKEN", headerVal)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("token configured: matching header passes", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "/api/internal", "secret"))
	})

	t.Run("token configured: missing header returns unauthorized", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "/api/internal", ""))
	})

	t.Run("token configured: wrong header returns unauthorized", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "/api/internal", "wrong"))
	})

	t.Run("token unset: protected routes fail closed", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = ""
		r := build()
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "/api/internal", ""))
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "/api/internal", "anything"))
	})

	t.Run("public routes still bypass auth", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = ""
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "/health", ""))
		assert.Equal(t, http.StatusOK, do(r, "/livez", ""))
		assert.Equal(t, http.StatusOK, do(r, "/debug/pprof", ""))
	})
}
