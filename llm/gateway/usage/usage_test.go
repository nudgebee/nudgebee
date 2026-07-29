package usage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCacheHitPct(t *testing.T) {
	assert.Equal(t, 0.0, cacheHitPct(0, 0))
	assert.Equal(t, 0.0, cacheHitPct(0, 100))
	assert.Equal(t, 50.0, cacheHitPct(50, 50))
	assert.InDelta(t, 20.0, cacheHitPct(20, 80), 0.001)
}

func newRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, nil, token)
	return r
}

func post(r *gin.Engine, token, tenant, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/rpc/usage/aggregate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-ACTION-TOKEN", token)
	}
	if tenant != "" {
		req.Header.Set("x-tenant-id", tenant) // injected by the RPC gateway in prod
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestServiceToken_RejectsMissingOrWrong(t *testing.T) {
	r := newRouter("secret")
	assert.Equal(t, http.StatusUnauthorized, post(r, "", "t1", `{}`).Code, "no token → 401")
	assert.Equal(t, http.StatusUnauthorized, post(r, "nope", "t1", `{}`).Code, "wrong token → 401")
}

func TestServiceToken_OptionalWhenUnset(t *testing.T) {
	r := newRouter("") // token optional: unset → plane stays open (not 401)
	// Past auth, but no x-tenant-id header → 400, proving auth did NOT reject it.
	assert.Equal(t, http.StatusBadRequest, post(r, "", "", `{}`).Code)
}

func TestAggregate_ValidatesRequest(t *testing.T) {
	r := newRouter("secret")
	// The RPC gateway wraps args as { input: { request: {…} } }.
	env := func(inner string) string { return `{"input":{"request":` + inner + `}}` }

	// Malformed JSON → 400.
	assert.Equal(t, http.StatusBadRequest, post(r, "secret", "t1", `{not json`).Code)
	// Missing tenant identity (no x-tenant-id header) → 400.
	assert.Equal(t, http.StatusBadRequest,
		post(r, "secret", "", env(`{"start_date":"2026-01-01T00:00:00Z","end_date":"2026-01-02T00:00:00Z"}`)).Code)
	// Tenant present but bad date → 400 (RPC-gateway envelope).
	assert.Equal(t, http.StatusBadRequest, post(r, "secret", "t1", env(`{"start_date":"nope","end_date":"nope"}`)).Code)
	// Direct/flat body (no envelope) is also accepted and reaches parsing → 400.
	assert.Equal(t, http.StatusBadRequest, post(r, "secret", "t1", `{"start_date":"nope","end_date":"nope"}`).Code)
}
