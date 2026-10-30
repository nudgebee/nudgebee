package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthHandlerMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevToken := config.Config.ServiceApiServerToken
	prevHeader := config.Config.ServiceApiServerTokenHeader
	t.Cleanup(func() {
		config.Config.ServiceApiServerToken = prevToken
		config.Config.ServiceApiServerTokenHeader = prevHeader
	})
	config.Config.ServiceApiServerTokenHeader = "X-ACTION-TOKEN"

	build := func() *gin.Engine {
		r := gin.New()
		r.Use(authHandlerMiddleware())
		r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.POST("/api/webhooks/example", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/swagger/index.html", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		r.GET("/api/internal", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
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
		config.Config.ServiceApiServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "GET", "/api/internal", "secret"))
	})

	t.Run("token configured: missing header returns unauthorized", func(t *testing.T) {
		config.Config.ServiceApiServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/api/internal", ""))
	})

	t.Run("token configured: wrong header returns unauthorized", func(t *testing.T) {
		config.Config.ServiceApiServerToken = "secret"
		r := build()
		assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/api/internal", "wrong"))
	})

	t.Run("token unset: protected routes fail closed", func(t *testing.T) {
		config.Config.ServiceApiServerToken = ""
		r := build()
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/internal", ""))
		assert.Equal(t, http.StatusServiceUnavailable, do(r, "GET", "/api/internal", "anything"))
	})

	t.Run("public routes still bypass auth", func(t *testing.T) {
		config.Config.ServiceApiServerToken = ""
		r := build()
		assert.Equal(t, http.StatusOK, do(r, "GET", "/health", ""))
		assert.Equal(t, http.StatusOK, do(r, "POST", "/api/webhooks/example", ""))
		assert.Equal(t, http.StatusOK, do(r, "GET", "/swagger/index.html", ""))
	})
}
