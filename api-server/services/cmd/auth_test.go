package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"nudgebee/services/config"
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
		handler := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
		r.GET("/private", handler)
		r.GET("/health", handler)
		r.POST("/api/webhooks/slack", handler)
		r.GET("/swagger/doc.json", handler)
		r.GET("/openapi.json", handler)
		return r
	}

	status := func(r *gin.Engine, method, path, token string) int {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("X-ACTION-TOKEN", token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("unset token rejects absent and arbitrary headers with 503", func(t *testing.T) {
		config.Config.ServiceApiServerToken = ""
		r := build()
		for _, token := range []string{"", "anything"} {
			if got := status(r, http.MethodGet, "/private", token); got != http.StatusServiceUnavailable {
				t.Errorf("token %q: got %d, want %d", token, got, http.StatusServiceUnavailable)
			}
		}
	})

	t.Run("configured token rejects missing and wrong headers", func(t *testing.T) {
		config.Config.ServiceApiServerToken = "secret"
		r := build()
		for _, token := range []string{"", "wrong"} {
			if got := status(r, http.MethodGet, "/private", token); got != http.StatusUnauthorized {
				t.Errorf("token %q: got %d, want %d", token, got, http.StatusUnauthorized)
			}
		}
		if got := status(r, http.MethodGet, "/private", "secret"); got != http.StatusOK {
			t.Errorf("matching token: got %d, want %d", got, http.StatusOK)
		}
	})

	t.Run("public routes remain available without a token", func(t *testing.T) {
		config.Config.ServiceApiServerToken = "secret"
		r := build()
		for _, route := range [][2]string{{http.MethodGet, "/health"}, {http.MethodPost, "/api/webhooks/slack"}, {http.MethodGet, "/swagger/doc.json"}, {http.MethodGet, "/openapi.json"}} {
			if got := status(r, route[0], route[1], ""); got != http.StatusOK {
				t.Errorf("%s %s: got %d, want %d", route[0], route[1], got, http.StatusOK)
			}
		}
	})
}
