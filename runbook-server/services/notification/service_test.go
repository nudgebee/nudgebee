package notification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// newTestRequestContext builds a RequestContext with a freshly generated
// tenant id. Tests don't hit a real DB so any well-formed UUID works.
func newTestRequestContext(t *testing.T) *security.RequestContext {
	t.Helper()
	tenantID := uuid.NewString()
	sc := &security.SecurityContext{}
	scBytes, err := json.Marshal(map[string]any{"TenantId": tenantID})
	assert.NoError(t, err)
	assert.NoError(t, sc.UnmarshalJSON(scBytes))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func withTestNotificationServerUrl(t *testing.T, url string) {
	t.Helper()
	old := config.Config.NotificationServerUrl
	config.Config.NotificationServerUrl = url
	t.Cleanup(func() { config.Config.NotificationServerUrl = old })
}

func TestJoinChannel_ForwardsBindAccountAndSessionId(t *testing.T) {
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/channels/join", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	_, err := JoinChannel(ctx, JoinChannelRequest{
		Platform:      "slack",
		ChannelID:     "C123",
		AccountID:     "acc-1",
		BindAccount:   true,
		BindSessionID: "fingerprint-abc",
	})

	assert.NoError(t, err)
	assert.NotNil(t, capturedBody)
	assert.Equal(t, true, capturedBody["bind_account"])
	assert.Equal(t, "fingerprint-abc", capturedBody["session_id"])
	assert.Equal(t, "fingerprint-abc", capturedBody["bind_session_id"])
	assert.Equal(t, "C123", capturedBody["channel_id"])
	assert.Equal(t, "acc-1", capturedBody["account_id"])
}

func TestJoinChannel_BindAccountWithoutSessionIdOmitsBindSessionId(t *testing.T) {
	// Regression test: bind_account=true with no caller-supplied BindSessionID must
	// NOT forward the generated-UUID fallback as bind_session_id — that field
	// exists precisely so notifications-server can't mistake a generated id for
	// a real conversation key and durably bind future @mentions to it.
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	_, err := JoinChannel(ctx, JoinChannelRequest{
		Platform:    "slack",
		ChannelID:   "C123",
		AccountID:   "acc-1",
		BindAccount: true,
		// BindSessionID intentionally left unset.
	})

	assert.NoError(t, err)
	assert.NotNil(t, capturedBody)
	assert.Equal(t, true, capturedBody["bind_account"])
	_, hasBindSessionID := capturedBody["bind_session_id"]
	assert.False(t, hasBindSessionID, "bind_session_id must be omitted when the caller didn't supply a SessionID")
}

func TestJoinChannel_DefaultsBindAccountFalseAndGeneratesSessionId(t *testing.T) {
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	_, err := JoinChannel(ctx, JoinChannelRequest{
		Platform:  "slack",
		ChannelID: "C123",
		AccountID: "acc-1",
		// BindAccount and BindSessionID intentionally left unset — this is the plain
		// "add the bot to a channel" call every other workflow task makes today.
	})

	assert.NoError(t, err)
	assert.NotNil(t, capturedBody)
	assert.Equal(t, false, capturedBody["bind_account"])
	sessionID, _ := capturedBody["session_id"].(string)
	assert.NotEmpty(t, sessionID, "session_id should fall back to a generated value when not provided")
	_, parseErr := uuid.Parse(sessionID)
	assert.NoError(t, parseErr, "generated session_id should be a UUID")
}
