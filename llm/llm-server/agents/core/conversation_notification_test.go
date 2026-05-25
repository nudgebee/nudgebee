package core

import (
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
		got := renderFollowupQuestion(markdown, "C08MHJWAA1Z-1779282615.385979")
		assert.Equal(t, "*Plan*\nApprove *this*?", got)
	})

	t.Run("non-slack session keeps original markdown", func(t *testing.T) {
		got := renderFollowupQuestion(markdown, "fd343b9a-63e2-459c-8c22-3361f308d076")
		assert.Equal(t, markdown, got)
	})
}
