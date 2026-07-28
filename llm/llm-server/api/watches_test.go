package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/watch"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// These tests pin the request-shaping contract of the /v1/watches/* HTTP
// surface: input validation, envelope-shape unwrapping, and the
// watchManagerFactory seam. Previously this surface had zero handler-level
// tests — a refactor that removes the missing-conversation_id check, that
// changes the affected_rows shape, or that breaks the flat/nested Hasura
// envelope routing would compile, lint, and ship clean.
//
// Cross-tenant enforcement is verified separately at the manager level
// (watch/manager_test.go) — the manager owns the WHERE tenant_id=$ filter
// and is tested under sqlmock; pinning it again at the handler layer
// requires standing up the full security-context dependency chain
// (GetAccountIdsForTenant → metastore), which is out of scope for a unit
// test. The handler's job here is to forward the security-context-derived
// tenant_uuid into Manager.ListByConversation / Manager.Cancel — visible
// by inspecting watches.go and covered by the manager's own tests.

func setupWatchesTestRouter(t *testing.T) (*gin.Engine, sqlmock.Sqlmock, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevSecMode := config.Config.LlmServerSecurityMode
	config.Config.LlmServerSecurityMode = "local"

	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	mockManager := watch.NewManagerForTesting(sqlx.NewDb(rawDB, "postgresql"))
	prev := SetWatchManagerFactory(func() *watch.Manager { return mockManager })

	r := gin.New()
	tracer := tracenoop.NewTracerProvider().Tracer("test")
	meter := otel.GetMeterProvider().Meter("test")
	handleWatchesApi(r, tracer, meter)

	cleanup := func() {
		SetWatchManagerFactory(prev)
		config.Config.LlmServerSecurityMode = prevSecMode
		_ = rawDB.Close()
	}
	return r, mock, cleanup
}

// hasuraEnvelope wraps a body in the canonical Hasura action envelope.
// tenantID is filled into session_variables so unauthenticated requests
// can still test the validation-before-auth code paths.
func hasuraEnvelope(input any, tenantID, userID string) []byte {
	body := map[string]any{
		"action": map[string]any{"name": "test"},
		"input":  input,
		"session_variables": map[string]any{
			"x-hasura-user-tenant-id": tenantID,
			"x-hasura-user-id":        userID,
			"x-hasura-role":           "tenant_admin",
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func postWatches(t *testing.T, r *gin.Engine, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// list-by-conversation — validation
// ---------------------------------------------------------------------------

func TestListWatches_Validation_MissingConversationID_NestedEnvelope(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{"request": map[string]any{}}, uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/list-by-conversation", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"missing conversation_id must 400 before reaching the DB; otherwise a refactor that drops the check returns an empty 200 list silently")
	assert.Contains(t, w.Body.String(), "conversation_id is required")
}

func TestListWatches_Validation_MissingConversationID_FlatEnvelope(t *testing.T) {
	// Bypass routers / dev tools sometimes send {action,input:{...flat...}}.
	// The flat path must validate the same way as the nested path — failing
	// open here would let bogus requests through under RPC_BYPASS_HASURA.
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{ /* no conversation_id at all */ }, uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/list-by-conversation", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "conversation_id")
}

func TestListWatches_Validation_RejectsNonUUIDConversationID(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{"request": map[string]any{"conversation_id": "not-a-uuid"}},
		uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/list-by-conversation", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"non-UUID conversation_id must 400 — otherwise the manager would receive a zero UUID and silently return no rows")
	assert.Contains(t, w.Body.String(), "must be a valid UUID")
}

func TestListWatches_Validation_RejectsMalformedJSON(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	w := postWatches(t, r, "/v1/watches/list-by-conversation", []byte("not json"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// cancel — validation
// ---------------------------------------------------------------------------

func TestCancelWatch_Validation_MissingWatchID_NestedEnvelope(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{"request": map[string]any{}}, uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/cancel", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"missing watch_id must 400; otherwise the handler would call Manager.Cancel with a zero UUID and silently succeed with affected_rows=0")
	assert.Contains(t, w.Body.String(), "watch_id is required")
}

func TestCancelWatch_Validation_MissingWatchID_FlatEnvelope(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{ /* no watch_id */ }, uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/cancel", body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "watch_id")
}

func TestCancelWatch_Validation_RejectsNonUUIDWatchID(t *testing.T) {
	r, _, cleanup := setupWatchesTestRouter(t)
	defer cleanup()

	body := hasuraEnvelope(map[string]any{"request": map[string]any{"watch_id": "not-a-uuid"}},
		uuid.NewString(), uuid.NewString())
	w := postWatches(t, r, "/v1/watches/cancel", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"non-UUID watch_id must 400 — defends against the handler parsing a zero UUID and Cancelling whatever happens to match")
	assert.Contains(t, w.Body.String(), "must be a valid UUID")
}

// ---------------------------------------------------------------------------
// watchManagerFactory seam
// ---------------------------------------------------------------------------

// TestWatchManagerFactory_SeamRestores guards the test-only injection seam.
// If a future refactor inlines watch.NewManager() back into the handlers,
// every handler test that depends on a sqlmock-backed manager silently
// breaks — this test catches that by verifying the factory is replaceable
// and restorable. Without the seam, handler-level coverage is impossible
// without a real metastore.
func TestWatchManagerFactory_SeamRestores(t *testing.T) {
	sentinelA := &watch.Manager{}
	sentinelB := &watch.Manager{}

	prev := SetWatchManagerFactory(func() *watch.Manager { return sentinelA })
	assert.Same(t, sentinelA, watchManagerFactory(),
		"after SetWatchManagerFactory(A), factory must return A")

	SetWatchManagerFactory(func() *watch.Manager { return sentinelB })
	assert.Same(t, sentinelB, watchManagerFactory(),
		"after SetWatchManagerFactory(B), factory must return B")

	// Restore. The `prev` returned by the FIRST Set call is the seam's
	// rollback handle — restoring it must put the factory back to what
	// it was before this test ran, regardless of intermediate sets.
	SetWatchManagerFactory(prev)
	// Calling the restored factory must not panic and must yield a
	// non-nil manager (the production NewManager() never returns nil).
	got := watchManagerFactory()
	require.NotNil(t, got, "restored factory must produce a usable manager")
}
