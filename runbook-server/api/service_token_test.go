package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveWithTokenCheck(t *testing.T, path, token string) int {
	t.Helper()
	r := gin.New()
	r.Use(serviceTokenMiddleware())
	r.Any("/*any", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if token != "" {
		req.Header.Set(serviceTokenHeader, token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestServiceTokenIsRequiredOnAPIRoutes(t *testing.T) {
	for _, path := range []string{"/rpc", "/workflows/wf-1/trigger", "/tasks/k8s.pod_delete/execute", "/workflows/dry-run", "/configs"} {
		assert.Equal(t, http.StatusUnauthorized, serveWithTokenCheck(t, path, ""), path)
		assert.Equal(t, http.StatusUnauthorized, serveWithTokenCheck(t, path, "wrong"), path)
		assert.Equal(t, http.StatusOK, serveWithTokenCheck(t, path, testServiceToken), path)
	}
}

func TestServiceTokenIsNotRequiredWhereARouteCarriesItsOwnCredential(t *testing.T) {
	for _, path := range []string{"/health", "/openapi.json", "/swagger/index.html", "/approvals/tok", "/webhook/wf-1", "/webhook/by-integration/github"} {
		assert.Equal(t, http.StatusOK, serveWithTokenCheck(t, path, ""), path)
	}
}

func TestEmptyConfiguredTokenRejectsEverything(t *testing.T) {
	// Fail closed: an unset token must never mean "no check".
	old := testServiceToken
	t.Cleanup(func() { setServiceToken(old) })
	setServiceToken("")
	assert.Equal(t, http.StatusUnauthorized, serveWithTokenCheck(t, "/rpc", ""))
}

func restContext(t *testing.T, allowSystem bool, headers map[string]string) (*security.RequestContext, error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/webhook/wf-1", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	if allowSystem {
		c.Set(ctxAllowSystemCaller, true)
	}
	return buildContextFromRequestPayload(context.Background(), c, map[string]string{}, nil, nil, slog.Default())
}

func TestRestRequestWithoutAUserIsRefused(t *testing.T) {
	// Before: no user meant a tenant-admin context.
	_, err := restContext(t, false, map[string]string{"x-tenant-id": "t"})
	assert.ErrorContains(t, err, "no user")
	_, err = restContext(t, false, map[string]string{"x-tenant-id": "t", "x-user-id": security.GetSystemUserId()})
	assert.ErrorContains(t, err, "no user")
}

func TestWebhookRoutesMayProceedAsTheSystemUser(t *testing.T) {
	// Webhook routes carry their own credential; the runs they start act as
	// the workflow's owner. (The tenant's account list needs a DB, so only the
	// absence of the "no user" refusal is asserted here.)
	_, err := restContext(t, true, map[string]string{"x-tenant-id": "t"})
	require.NoError(t, err)
}

func TestRPCRequestWithoutAUserIsRefusedEvenWithAnAdminRole(t *testing.T) {
	for _, vars := range []map[string]any{
		{"tenant_id": "t"},
		{"tenant_id": "t", "role": "admin"},
		{"role": "admin"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/rpc", nil)
		_, err := buildContextFromPayload(context.Background(), c, &ActionRequest{SessionVariables: vars, Input: map[string]any{}}, nil, nil, slog.Default())
		assert.Error(t, err, "%v", vars)
	}
}
