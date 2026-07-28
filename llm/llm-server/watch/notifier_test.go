package watch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nudgebee/llm/config"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The notifier had zero tests prior to this file — the only safety net was
// the integration suite, which only fires under WATCH_INTEGRATION_DB_URL.
// What we pin here:
//
//   - The body shape sent to /llm/response is what notifications-server
//     expects (conversation_id, session_id, tenant_id, type=final, response).
//   - renderNotifyMessage picks the right canonical line per status when no
//     notify_template is set, and substitutes {summary} when it is.
//   - HTTPNotifier returns an error on >=300 status (callers escalate to
//     recordNotifierFailure).
//   - HTTPNotifier no-ops cleanly when NotificationServerUrl is empty —
//     local dev / unit tests must NEVER fan out to a real URL by accident.

// ---------------------------------------------------------------------------
// renderNotifyMessage
// ---------------------------------------------------------------------------

func TestRenderNotifyMessage_TemplateSubstitution(t *testing.T) {
	tmpl := "Done: {summary}"
	w := Watch{NotifyTemplate: &tmpl}
	got := renderNotifyMessage(w, StatusCompleted, "rolled out v1.2.3")
	assert.Equal(t, "Done: rolled out v1.2.3", got)
}

func TestRenderNotifyMessage_TemplateWithMultipleSubstitutions(t *testing.T) {
	// strings.ReplaceAll → all {summary} occurrences get substituted; this
	// is documented behavior, not a bug. Lock it in so we don't drift to
	// "replace first only" by accident.
	tmpl := "first: {summary} / second: {summary}"
	w := Watch{NotifyTemplate: &tmpl}
	got := renderNotifyMessage(w, StatusCompleted, "x")
	assert.Equal(t, "first: x / second: x", got)
}

func TestRenderNotifyMessage_TemplateEmptyFallsBackToCanonical(t *testing.T) {
	// An empty template (nil OR empty string) should NOT produce an empty
	// message — fall back to the canonical per-status line so the user
	// always sees something useful.
	emptyT := ""
	w := Watch{NotifyTemplate: &emptyT}
	got := renderNotifyMessage(w, StatusCompleted, "x")
	assert.Contains(t, got, "Watch completed",
		"empty template must fall back to canonical line, not render an empty message")
}

func TestRenderNotifyMessage_CanonicalPerStatus(t *testing.T) {
	cases := []struct {
		status   Status
		summary  string
		contains string
	}{
		{StatusCompleted, "ok", "Watch completed"},
		{StatusCompleted, "", "Watch completed."},
		{StatusExpired, "last obs", "expired"},
		{StatusExpired, "", "expired before"},
		{StatusFailed, "boom", "Watch failed"},
		{StatusFailed, "", "Watch failed."},
		{StatusCancelled, "ignored", "Watch cancelled"},
	}
	for _, tc := range cases {
		t.Run(string(tc.status)+"_"+tc.summary, func(t *testing.T) {
			got := renderNotifyMessage(Watch{}, tc.status, tc.summary)
			assert.Contains(t, got, tc.contains)
		})
	}
}

// ---------------------------------------------------------------------------
// buildNotifyBody
// ---------------------------------------------------------------------------

func TestBuildNotifyBody_HasExpectedKeys(t *testing.T) {
	w := Watch{
		ID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ConversationID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		TenantID:       uuid.MustParse("33333333-3333-3333-3333-333333333333"),
	}
	body := buildNotifyBody(w, StatusCompleted, "done")

	// Pin every key notifications-server reads — a missing key on the wire
	// = the message routes nowhere.
	assert.Equal(t, w.ConversationID.String(), body["conversation_id"])
	assert.Equal(t, w.ConversationID.String(), body["session_id"],
		"session_id is intentionally the same as conversation_id; notifications-server uses session_id to find the chat")
	assert.Equal(t, w.TenantID.String(), body["tenant_id"])
	assert.Equal(t, "final", body["type"],
		"type=final tells the notifier this is the terminal-state message, not a streaming chunk")
	assert.Contains(t, body["response"], "Watch completed")
}

// ---------------------------------------------------------------------------
// HTTPNotifier — HTTP behavior via httptest
// ---------------------------------------------------------------------------

// notifierRequest captures what the notifications-server endpoint sees on
// each call so the test can assert post-state.
type notifierRequest struct {
	method string
	path   string
	body   map[string]any
}

func TestHTTPNotifier_NoURL_SkipsCleanly(t *testing.T) {
	// Empty NotificationServerUrl must be a no-op success — not a panic, not
	// an HTTP call to "". Otherwise local dev / unit tests would fan out
	// requests to whatever the empty URL resolves to.
	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ""
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	err := HTTPNotifier{}.Notify(superAdminCtx(), Watch{}, StatusCompleted, "ok")
	require.NoError(t, err)
}

func TestHTTPNotifier_PostsExpectedBody_To_LLMResponse(t *testing.T) {
	// Spin a tiny test server and verify Notify hits /llm/response with the
	// expected POST body. Pins the contract notifications-server depends on.
	var got notifierRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	w := Watch{
		ID:             uuid.New(),
		ConversationID: uuid.New(),
		TenantID:       uuid.New(),
	}
	require.NoError(t, HTTPNotifier{}.Notify(superAdminCtx(), w, StatusCompleted, "done"))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/llm/response", got.path)
	assert.Equal(t, w.ConversationID.String(), got.body["conversation_id"])
	assert.Equal(t, "final", got.body["type"])
	assert.Contains(t, got.body["response"], "Watch completed")
}

func TestHTTPNotifier_TrailingSlashOnURL_IsHandled(t *testing.T) {
	// Operators routinely set URLs with or without trailing slashes. The
	// notifier MUST normalize to a single /llm/response (not //llm/response).
	var seenPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL + "///"
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	require.NoError(t, HTTPNotifier{}.Notify(superAdminCtx(), Watch{}, StatusCompleted, "x"))
	assert.Equal(t, "/llm/response", seenPath,
		"notifier must trim trailing slashes; otherwise notifications-server returns 404")
}

func TestHTTPNotifier_5xx_ReturnsError(t *testing.T) {
	// notifier-server 5xx must surface as a non-nil error so the executor's
	// recordNotifierFailure metric fires. A silent 5xx → the user's chip
	// never updates and on-call has no signal.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"err":"upstream down"}`))
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	err := HTTPNotifier{}.Notify(superAdminCtx(), Watch{}, StatusFailed, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Contains(t, err.Error(), "upstream down",
		"error message must include the response body so logs are actionable")
}

func TestHTTPNotifier_4xx_ReturnsError(t *testing.T) {
	// 4xx is just as bad as 5xx for the user — the message didn't get
	// delivered. Lock that in: notifier returns an error for any code >=300.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	err := HTTPNotifier{}.Notify(superAdminCtx(), Watch{}, StatusFailed, "")
	require.Error(t, err)
}

func TestHTTPNotifier_2xx_NoError(t *testing.T) {
	// 200 and 201 must succeed. 299 too. 300 is an error.
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated) // 201
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	require.NoError(t, HTTPNotifier{}.Notify(superAdminCtx(), Watch{}, StatusCompleted, "x"))
	assert.Equal(t, int32(1), calls.Load())
}
