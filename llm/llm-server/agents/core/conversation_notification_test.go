package core

import (
	"nudgebee/llm/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveNotificationSessionId guards the fix for the empty-conversation_id
// regression: a followup answer routed through the resume-v2 path returns a
// response with no SessionId, which previously went on the wire as
// conversation_id="" and got 404'd by the notification server — dropping every
// followup/final message after the first turn.
func TestResolveNotificationSessionId(t *testing.T) {
	const slackSession = "C08MHJWAA1Z-1779282615.385979"

	tests := []struct {
		name         string
		respSession  string
		reqSession   string
		wantSession  string
		wantFallback bool
	}{
		{
			name:         "uses response session when present",
			respSession:  slackSession,
			reqSession:   "OTHER-should-not-win",
			wantSession:  slackSession,
			wantFallback: false,
		},
		{
			name:         "falls back to request session when response empty",
			respSession:  "",
			reqSession:   slackSession,
			wantSession:  slackSession,
			wantFallback: true,
		},
		{
			name:         "both empty stays empty rather than inventing an id",
			respSession:  "",
			reqSession:   "",
			wantSession:  "",
			wantFallback: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, usedFallback := resolveNotificationSessionId(
				NBAgentResponse{SessionId: tc.respSession},
				NBAgentRequest{SessionId: tc.reqSession},
			)
			assert.Equal(t, tc.wantSession, got)
			assert.Equal(t, tc.wantFallback, usedFallback)
		})
	}
}

// TestRenderFollowupQuestion: Slack followups convert to mrkdwn, others pass through.
func TestRenderFollowupQuestion(t *testing.T) {
	const markdown = "# Plan\nApprove **this**?"

	t.Run("slack session converts markdown", func(t *testing.T) {
		got := renderFollowupQuestion(markdown, "C08MHJWAA1Z-1779282615.385979", "acc")
		assert.Equal(t, "*Plan*\nApprove *this*?", got)
	})

	t.Run("non-slack session keeps original markdown", func(t *testing.T) {
		got := renderFollowupQuestion(markdown, "fd343b9a-63e2-459c-8c22-3361f308d076", "acc")
		assert.Equal(t, markdown, got)
	})
}

// TestRenderFinalResponse: same gate as TestRenderFollowupQuestion, applied to
// the "final" response text - guards the fix where this case unconditionally
// called convertMarkdownToSlackMarkdown regardless of destination platform,
// corrupting Teams/Google-Chat messages with Slack-only mrkdwn syntax
// (**bold**->*bold*, [x](y)-><y|x>) that those platforms don't render.
func TestRenderFinalResponse(t *testing.T) {
	const markdown = "# Plan\nApprove **this**?"

	t.Run("slack session converts markdown", func(t *testing.T) {
		got := renderFinalResponse(markdown, "C08MHJWAA1Z-1779282615.385979", "acc")
		assert.Equal(t, "*Plan*\nApprove *this*?", got)
	})

	t.Run("non-slack session keeps original markdown", func(t *testing.T) {
		got := renderFinalResponse(markdown, "fd343b9a-63e2-459c-8c22-3361f308d076", "acc")
		assert.Equal(t, markdown, got)
	})
}

// TestChatCitationPageURL: Slack only links absolute http(s) URLs, so the page
// is left out when the app's address is not one.
func TestChatCitationPageURL(t *testing.T) {
	saved := config.Config.BaseUrl
	t.Cleanup(func() { config.Config.BaseUrl = saved })

	config.Config.BaseUrl = "https://app.example.com/"
	assert.Equal(t, "https://app.example.com/ask-nudgebee?accountId=acc&session_id=C0123456789-1792769289.338369",
		chatCitationPageURL("acc", "C0123456789-1792769289.338369"))
	assert.Equal(t, "", chatCitationPageURL("", "C0123456789-1.2"))

	config.Config.BaseUrl = "nudgebee"
	assert.Equal(t, "", chatCitationPageURL("acc", "C0123456789-1.2"))
}
