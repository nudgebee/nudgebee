package watch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

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
	assert.Equal(t, w.ID.String(), body["watch_id"],
		"watch_id lets notifications-server retire the in-progress status message")
}

// ---------------------------------------------------------------------------
// isRoutableChatSession
// ---------------------------------------------------------------------------

func TestIsRoutableChatSession(t *testing.T) {
	cases := []struct {
		name    string
		session string
		want    bool
	}{
		{"empty", "", false},
		{"web UUID session", "22222222-2222-2222-2222-222222222222", false},
		{"workflow session", "wf__abc123", false},
		{"slack channel-thread", "C0123ABCD-1699900000.001500", true},
		{"slack DM channel", "D0123ABCD-1699900000.001500", true},
		{"google chat space", "spaces/AAAA/threads/BBBB", true},
		{"teams a: id", "a:1a2b3c-_-msg42", true},
		{"teams 19: id", "19:meeting_abc@thread.v2-_-msg42", true},
		// Not routable: watch payloads carry no reply_ref, so an event- session
		// would land in the wrong thread and broadcast channel-wide.
		{"event-derived thread", "event-abcd-1234", false},
		// Single-dash non-Slack shapes: the old len(Split(id,"-"))==2 heuristic
		// accepted these → POST that _parse_conversation 404s.
		{"custom run id", "run-12345", false},
		{"non-slack channel prefix", "X0123ABCD-1699900000.001500", false},
		{"slack channel without thread_ts dot", "C0123ABCD-1699900000", false},
		{"thread_ts only, no channel", "-1699900000.001500", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isRoutableChatSession(tc.session))
			assert.Equal(t, tc.want, IsRoutableChatSession(tc.session))
		})
	}
}

// Cancel must deliver a terminal "cancelled" msg to the chat thread — it is a bare
// UPDATE that skips terminate(), so the "🔭 Watching…" indicator was never resolved.
func TestDeliverCancelled_NotifiesChatSession(t *testing.T) {
	var got notifierRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	prev := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = ts.URL
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	session := "C0123ABCD-1699900000.001500"
	w := Watch{ID: uuid.New(), ConversationID: uuid.New(), TenantID: uuid.New(), NotifySession: &session}

	// No metastore → responder half no-ops; the notifier half must still fire.
	NewManager().DeliverCancelled(superAdminCtx(), w)

	assert.Equal(t, session, got.body["conversation_id"], "cancel must route to the chat session, not the UUID")
	assert.Equal(t, "final", got.body["type"])
	assert.Equal(t, w.ID.String(), got.body["watch_id"], "watch_id lets the indicator be retired")
	assert.Contains(t, got.body["response"], "cancelled")
}

// A stored session must be used verbatim, never hitting the DB → kills the
// "transient lookup fails → msg dropped" mode. (No metastore ⇒ fallback gives "".)
func TestResolveNotifySession_PrefersStoredColumn(t *testing.T) {
	stored := "C0123ABCD-1699900000.001500"
	w := Watch{ID: uuid.New(), ConversationID: uuid.New(), TenantID: uuid.New(), NotifySession: &stored}
	assert.Equal(t, stored, resolveNotifySession(superAdminCtx(), w))

	// Empty/nil column falls through to the lookup path, which yields "" here.
	empty := ""
	w.NotifySession = &empty
	assert.Equal(t, "", resolveNotifySession(superAdminCtx(), w))
	w.NotifySession = nil
	assert.Equal(t, "", resolveNotifySession(superAdminCtx(), w))
}

// Headline fix: chat session id → routed to it; UUID/empty → UUID kept so web keeps
// using the DB responder. Resolve stubbed per-notifier (no metastore, no shared state).
func TestHTTPNotifier_RoutingOverride(t *testing.T) {
	cases := []struct {
		name        string
		resolved    string
		wantConvoID func(w Watch) string
	}{
		{"chat session overrides UUID", "C0123ABCD-1699900000.001500", func(Watch) string { return "C0123ABCD-1699900000.001500" }},
		{"empty resolve keeps UUID", "", func(w Watch) string { return w.ConversationID.String() }},
		{"UUID resolve keeps UUID", "33333333-3333-3333-3333-333333333333", func(w Watch) string { return w.ConversationID.String() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got notifierRequest
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &got.body)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(ts.Close)

			prevURL := config.Config.NotificationServerUrl
			config.Config.NotificationServerUrl = ts.URL
			t.Cleanup(func() { config.Config.NotificationServerUrl = prevURL })

			notifier := HTTPNotifier{
				resolveSessionOverride: func(*security.RequestContext, Watch) string { return tc.resolved },
			}
			w := Watch{ID: uuid.New(), ConversationID: uuid.New(), TenantID: uuid.New()}
			require.NoError(t, notifier.Notify(superAdminCtx(), w, StatusCompleted, "done"))

			want := tc.wantConvoID(w)
			assert.Equal(t, want, got.body["conversation_id"])
			assert.Equal(t, want, got.body["session_id"])
		})
	}
}

// ---------------------------------------------------------------------------
// Notify — action token header
// ---------------------------------------------------------------------------

func TestHTTPNotifier_AttachesActionToken_WhenConfigured(t *testing.T) {
	// /llm/response is behind verify_action_token → notify MUST send X-ACTION-TOKEN
	// when NOTIFICATION_SERVER_TOKEN is set, else 401.
	var gotToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-ACTION-TOKEN")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	prevURL := config.Config.NotificationServerUrl
	prevTok := config.Config.NotificationServerToken
	config.Config.NotificationServerUrl = ts.URL
	config.Config.NotificationServerToken = "s3cr3t"
	t.Cleanup(func() {
		config.Config.NotificationServerUrl = prevURL
		config.Config.NotificationServerToken = prevTok
	})

	require.NoError(t, HTTPNotifier{}.Notify(superAdminCtx(), Watch{ID: uuid.New()}, StatusCompleted, "x"))
	assert.Equal(t, "s3cr3t", gotToken)
}

func TestHTTPNotifier_NoActionToken_WhenUnset(t *testing.T) {
	var sawHeader bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["X-Action-Token"]
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	prevURL := config.Config.NotificationServerUrl
	prevTok := config.Config.NotificationServerToken
	config.Config.NotificationServerUrl = ts.URL
	config.Config.NotificationServerToken = ""
	t.Cleanup(func() {
		config.Config.NotificationServerUrl = prevURL
		config.Config.NotificationServerToken = prevTok
	})

	require.NoError(t, HTTPNotifier{}.Notify(superAdminCtx(), Watch{ID: uuid.New()}, StatusCompleted, "x"))
	assert.False(t, sawHeader, "no X-ACTION-TOKEN header must be sent when the token is unset")
}

// ---------------------------------------------------------------------------
// NotifyWatchRegistered
// ---------------------------------------------------------------------------

func TestNotifyWatchRegistered_PostsWatchRegisteredType(t *testing.T) {
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

	w := &Watch{ID: uuid.New(), TenantID: uuid.New()}
	session := "C0123ABCD-1699900000.001500"
	require.NoError(t, NotifyWatchRegistered(superAdminCtx(), w, session))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/llm/response", got.path)
	assert.Equal(t, "watch_registered", got.body["type"])
	assert.Equal(t, session, got.body["conversation_id"])
	assert.Equal(t, session, got.body["session_id"])
	assert.Equal(t, w.ID.String(), got.body["watch_id"])
}

func TestNotifyWatchRegistered_NoURLOrSession_SkipsCleanly(t *testing.T) {
	prev := config.Config.NotificationServerUrl
	t.Cleanup(func() { config.Config.NotificationServerUrl = prev })

	// No URL configured → no-op success even with a valid session.
	config.Config.NotificationServerUrl = ""
	require.NoError(t, NotifyWatchRegistered(superAdminCtx(), &Watch{ID: uuid.New()}, "C1-1.0"))

	// URL configured but empty session → no-op success (nothing to route to).
	config.Config.NotificationServerUrl = "http://127.0.0.1:0"
	require.NoError(t, NotifyWatchRegistered(superAdminCtx(), &Watch{ID: uuid.New()}, ""))
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
