package usage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	RegisterRoutes(r, token)
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

func TestListRequestsFilter(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	base := ListRequest{TenantID: "t1", StartDate: start, EndDate: end}

	t.Run("no optional filters → base window only", func(t *testing.T) {
		where, args := listRequestsFilter(base)
		assert.Equal(t, "g.tenant_id = $1 AND g.created_at >= $2 AND g.created_at < $3", where)
		assert.Equal(t, []any{"t1", start, end}, args)
	})

	t.Run("user filter", func(t *testing.T) {
		req := base
		req.UserID = "u1"
		where, args := listRequestsFilter(req)
		assert.Contains(t, where, "g.user_id = $4")
		assert.Equal(t, []any{"t1", start, end, "u1"}, args)
	})

	t.Run("providers and models get numbered IN placeholders", func(t *testing.T) {
		req := base
		req.Providers = []string{"anthropic", "openai"}
		req.Models = []string{"claude-fable-5"}
		where, args := listRequestsFilter(req)
		assert.Contains(t, where, "g.provider IN ($4,$5)")
		assert.Contains(t, where, "g.model IN ($6)")
		assert.Equal(t, []any{"t1", start, end, "anthropic", "openai", "claude-fable-5"}, args)
	})

	t.Run("status classes partition all rows", func(t *testing.T) {
		req := base
		req.Status = "success"
		whereOK, argsOK := listRequestsFilter(req)
		assert.Contains(t, whereOK, "AND g.status_code BETWEEN 200 AND 299")
		assert.Len(t, argsOK, 3) // status adds no args

		req.Status = "error"
		whereErr, _ := listRequestsFilter(req)
		assert.Contains(t, whereErr, "(g.status_code IS NULL OR g.status_code NOT BETWEEN 200 AND 299)")

		req.Status = "bogus" // unknown class → ignored, not an error
		whereAll, _ := listRequestsFilter(req)
		assert.NotContains(t, whereAll, "status_code")
	})

	t.Run("all filters compose with correct placeholder numbering", func(t *testing.T) {
		req := base
		req.UserID = "u1"
		req.Providers = []string{"anthropic"}
		req.Models = []string{"m1", "m2"}
		req.Status = "error"
		req.Tool = "Bash"
		where, args := listRequestsFilter(req)
		assert.Contains(t, where, "g.user_id = $4")
		assert.Contains(t, where, "g.provider IN ($5)")
		assert.Contains(t, where, "g.model IN ($6,$7)")
		assert.Contains(t, where, "tool_names")
		assert.Contains(t, where, "$8")
		assert.Equal(t, []any{"t1", start, end, "u1", "anthropic", "m1", "m2", "Bash"}, args)
	})
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
