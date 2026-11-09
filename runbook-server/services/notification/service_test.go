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

func TestSendNotification_StampsAutomationEventAndAccount(t *testing.T) {
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/messages/send", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"platform": "slack", "status": "sent", "message_ts": "1.2"}})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	resp, err := SendNotification(ctx, SendImNotificationRequest{
		Platform:  "slack",
		Channel:   "C123",
		AccountID: "acc-1",
		Body:      "run complete",
		EventType: EventTaskCompleted,
	})

	assert.NoError(t, err)
	assert.Equal(t, "1.2", resp.MessageTs)
	assert.Equal(t, SourceAutomation, capturedBody["source"])
	assert.Equal(t, EventTaskCompleted, capturedBody["event_type"])
	params, _ := capturedBody["parameters"].(map[string]any)
	assert.Equal(t, "acc-1", params["account_id"])
	assert.Equal(t, "run complete", params["message"])
	assert.NotNil(t, capturedBody["channels"])
	assert.Nil(t, capturedBody["thread"])
}

func TestSendNotification_ThreadedBodyKeepsEventAndAccount(t *testing.T) {
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"platform": "system", "status": "suppressed", "reason": "rule"}})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	resp, err := SendNotification(ctx, SendImNotificationRequest{
		Platform:  "slack",
		Channel:   "C123",
		ThreadId:  "1.1",
		AccountID: "acc-1",
		Body:      "step done",
		EventType: EventTaskProgress,
	})

	assert.NoError(t, err)
	assert.Equal(t, StatusSuppressed, resp.Status)
	assert.Equal(t, SourceAutomation, capturedBody["source"])
	assert.Equal(t, EventTaskProgress, capturedBody["event_type"])
	params, _ := capturedBody["parameters"].(map[string]any)
	assert.Equal(t, "acc-1", params["account_id"])
	thread, _ := capturedBody["thread"].(map[string]any)
	assert.Equal(t, "1.1", thread["message_ts"])
	assert.Nil(t, capturedBody["channels"])
}

func TestSendNotification_RejectsUnknownEventType(t *testing.T) {
	ctx := newTestRequestContext(t)
	for _, eventType := range []string{"", "automation.something.else"} {
		_, err := SendNotification(ctx, SendImNotificationRequest{
			Platform:  "slack",
			Channel:   "C123",
			AccountID: "acc-1",
			EventType: eventType,
		})
		assert.Error(t, err, eventType)
	}
}

func TestSendEmail_SuccessFalseResponseIsAnError(t *testing.T) {
	// Regression test for #41711: a 200 response with success=false (e.g. the
	// SMTP send failed) must surface as an error, not be reported as a
	// successful "send email" automation step.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "Failed to send email to any recipient",
		})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	resp, err := SendEmail(ctx, SendEmailRequest{
		Recipients: []string{"user@example.com"},
		Subject:    "hello",
		Body:       "body",
	})

	assert.Error(t, err)
	assert.False(t, resp.Success)
}

func TestSendEmail_SuccessTrueResponseIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"sent_to": []string{"user@example.com"},
		})
	}))
	defer server.Close()
	withTestNotificationServerUrl(t, server.URL)

	ctx := newTestRequestContext(t)
	resp, err := SendEmail(ctx, SendEmailRequest{
		Recipients: []string{"user@example.com"},
		Subject:    "hello",
		Body:       "body",
	})

	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, []string{"user@example.com"}, resp.SentTo)
}
