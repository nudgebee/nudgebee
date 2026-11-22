package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/collector/cloud/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// setToken sets the auth config for one test and restores it afterwards.
func setToken(t *testing.T, token string) {
	t.Helper()
	prevTok := config.Config.CloudCollectorServerToken
	prevHdr := config.Config.CloudCollectorServerTokenHeader
	config.Config.CloudCollectorServerToken = token
	config.Config.CloudCollectorServerTokenHeader = "X-ACTION-TOKEN"
	t.Cleanup(func() {
		config.Config.CloudCollectorServerToken = prevTok
		config.Config.CloudCollectorServerTokenHeader = prevHdr
	})
}

func runAuth(path, headerToken string) int {
	gin.SetMode(gin.TestMode)
	// The production engine, so a pprof route registered ahead of the auth
	// middleware fails TestAuthMiddleware_DebugRequiresToken.
	r := newEngine()
	r.POST("/v1/cloud/cli_credentials", func(c *gin.Context) { c.Status(200) })
	r.GET("/health", func(c *gin.Context) { c.Status(200) })
	method := http.MethodGet
	if path == "/v1/cloud/cli_credentials" {
		method = http.MethodPost
	}
	req := httptest.NewRequest(method, path, nil)
	if headerToken != "" {
		req.Header.Set("X-ACTION-TOKEN", headerToken)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAuthMiddleware_CorrectTokenAllowed(t *testing.T) {
	setToken(t, "s3cret-token")
	assert.Equal(t, 200, runAuth("/v1/cloud/cli_credentials", "s3cret-token"))
}

func TestAuthMiddleware_WrongTokenRejected(t *testing.T) {
	setToken(t, "s3cret-token")
	assert.Equal(t, 401, runAuth("/v1/cloud/cli_credentials", "nope"))
	assert.Equal(t, 401, runAuth("/v1/cloud/cli_credentials", ""))
}

// Fail closed: with no token configured, every non-public request is refused,
// with or without a header.
func TestAuthMiddleware_EmptyConfiguredTokenFailsClosed(t *testing.T) {
	setToken(t, "")
	assert.Equal(t, http.StatusServiceUnavailable, runAuth("/v1/cloud/cli_credentials", ""))
	assert.Equal(t, http.StatusServiceUnavailable, runAuth("/v1/cloud/cli_credentials", "anything"))
	assert.Equal(t, http.StatusServiceUnavailable, runAuth("/debug/pprof/", ""))
}

func TestAuthMiddleware_DebugRequiresToken(t *testing.T) {
	setToken(t, "s3cret-token")
	assert.Equal(t, 401, runAuth("/debug/pprof/", ""))
	assert.Equal(t, 200, runAuth("/debug/pprof/", "s3cret-token"))
}

func TestAuthMiddleware_PublicPathBypassed(t *testing.T) {
	setToken(t, "s3cret-token")
	assert.Equal(t, 200, runAuth("/health", ""))
}
