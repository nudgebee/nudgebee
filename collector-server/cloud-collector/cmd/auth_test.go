package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/collector/cloud/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// authHandlerMiddleware gates every route except /health behind the
// cloud-collector service token. /debug/pprof must NOT be exempt — pprof
// heap/goroutine dumps can leak in-flight request state and credentials.
func TestAuthHandlerMiddleware_PprofGated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevToken := config.Config.CloudCollectorServerToken
	prevHeader := config.Config.CloudCollectorServerTokenHeader
	t.Cleanup(func() {
		config.Config.CloudCollectorServerToken = prevToken
		config.Config.CloudCollectorServerTokenHeader = prevHeader
	})
	config.Config.CloudCollectorServerTokenHeader = "X-ACTION-TOKEN"
	config.Config.CloudCollectorServerToken = "secret"

	build := func() *gin.Engine {
		r := gin.New()
		r.Use(authHandlerMiddleware())
		r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		// pprof routes are registered after the middleware in main(); model one.
		r.GET("/debug/pprof/heap", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		return r
	}

	do := func(r *gin.Engine, path, headerVal string) int {
		req := httptest.NewRequest("GET", path, nil)
		if headerVal != "" {
			req.Header.Set("X-ACTION-TOKEN", headerVal)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	r := build()
	assert.Equal(t, http.StatusOK, do(r, "/health", ""), "/health stays public")
	assert.Equal(t, http.StatusUnauthorized, do(r, "/debug/pprof/heap", ""), "pprof requires the token")
	assert.Equal(t, http.StatusUnauthorized, do(r, "/debug/pprof/heap", "wrong"), "pprof rejects a wrong token")
	assert.Equal(t, http.StatusOK, do(r, "/debug/pprof/heap", "secret"), "pprof passes with the token")
}
