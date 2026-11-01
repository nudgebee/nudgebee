package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"nudgebee/collector/cloud/config"
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
		handler := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
		r.GET("/private", handler)
		r.GET("/health", handler)
		r.GET("/livez", handler)
		return r
	}

	status := func(r *gin.Engine, path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("X-ACTION-TOKEN", token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("unset token rejects absent and arbitrary headers with 503", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = ""
		r := build()
		for _, token := range []string{"", "anything"} {
			if got := status(r, "/private", token); got != http.StatusServiceUnavailable {
				t.Errorf("token %q: got %d, want %d", token, got, http.StatusServiceUnavailable)
			}
		}
	})

	t.Run("configured token rejects missing and wrong headers", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = "secret"
		r := build()
		for _, token := range []string{"", "wrong"} {
			if got := status(r, "/private", token); got != http.StatusUnauthorized {
				t.Errorf("token %q: got %d, want %d", token, got, http.StatusUnauthorized)
			}
		}
		if got := status(r, "/private", "secret"); got != http.StatusOK {
			t.Errorf("matching token: got %d, want %d", got, http.StatusOK)
		}
	})

	t.Run("health probes remain available without a token", func(t *testing.T) {
		config.Config.CloudCollectorServerToken = "secret"
		r := build()
		for _, path := range []string{"/health", "/livez"} {
			if got := status(r, path, ""); got != http.StatusOK {
				t.Errorf("%s: got %d, want %d", path, got, http.StatusOK)
			}
		}
	})
}
