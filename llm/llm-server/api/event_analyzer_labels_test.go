package api

import (
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestSanitizeEventLabelsForPrompt is the llm-server defense-in-depth guardrail
// for the RCA-prompt label bloat (nudgebee-enterprise#35899). It is generic and
// key-agnostic: any oversized label value is bounded by size alone — the guard
// has no knowledge of which key or provider produced it. Oversized values are
// replaced inline with a workspace-file pointer and recorded in `pending` for the
// caller to persist on the run branch (this function does no I/O).
func TestSanitizeEventLabelsForPrompt(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("offloads an oversized value regardless of key", func(t *testing.T) {
		// A big value under an arbitrary key — the guard keys on size, not name.
		bigVal := strings.Repeat("pod=app,status=200;", 500)
		raw, err := common.MarshalJson(map[string]any{
			"alertname": "HighLatency",
			"namespace": "prod",
			"_series":   bigVal, // could be any key from any provider
		})
		assert.NoError(t, err)

		pending := map[string]string{}
		out, ok := sanitizeEventLabelsForPrompt(ctx, string(raw), "evt1", pending).(string)
		assert.True(t, ok)
		// The oversized blob content is gone, replaced by a pointer with a grep hint.
		assert.NotContains(t, out, "status=200")
		assert.Contains(t, out, "workspace file")
		assert.Contains(t, out, "grep")
		// The full value is queued for the caller to write on the run branch.
		assert.Len(t, pending, 1)
		for _, content := range pending {
			assert.Contains(t, content, "status=200")
		}
		// Diagnostic labels survive untouched.
		parsed, err := common.UnmarshalJsonAsMap([]byte(out))
		assert.NoError(t, err)
		assert.Equal(t, "HighLatency", parsed["alertname"])
		assert.Equal(t, "prod", parsed["namespace"])
	})

	t.Run("bounds any oversized value from any provider", func(t *testing.T) {
		huge := strings.Repeat("x", maxEventLabelValueBytes+100)
		labels := `{"alertname":"A","blob":"` + huge + `"}`

		pending := map[string]string{}
		out, ok := sanitizeEventLabelsForPrompt(ctx, labels, "evt2", pending).(string)
		assert.True(t, ok)
		assert.NotContains(t, out, huge)
		assert.Contains(t, out, "workspace file")
		assert.Len(t, pending, 1)
		parsed, err := common.UnmarshalJsonAsMap([]byte(out))
		assert.NoError(t, err)
		assert.Equal(t, "A", parsed["alertname"])
	})

	t.Run("passes small clean labels through unchanged, nothing queued", func(t *testing.T) {
		labels := `{"alertname":"A","namespace":"prod"}`
		pending := map[string]string{}
		assert.Equal(t, labels, sanitizeEventLabelsForPrompt(ctx, labels, "evt3", pending))
		assert.Empty(t, pending)
	})

	t.Run("non-string and empty input returned unchanged", func(t *testing.T) {
		pending := map[string]string{}
		m := map[string]any{"k": "v"}
		assert.Equal(t, m, sanitizeEventLabelsForPrompt(ctx, m, "evt4", pending))
		assert.Equal(t, "", sanitizeEventLabelsForPrompt(ctx, "", "evt4", pending))
		assert.Nil(t, sanitizeEventLabelsForPrompt(ctx, nil, "evt4", pending))
		assert.Empty(t, pending)
	})

	t.Run("bounds an oversized non-object string", func(t *testing.T) {
		blob := strings.Repeat("y", maxEventLabelValueBytes+500)
		pending := map[string]string{}
		out, ok := sanitizeEventLabelsForPrompt(ctx, blob, "evt5", pending).(string)
		assert.True(t, ok)
		assert.Less(t, len(out), len(blob))
		assert.Contains(t, out, "grep")
		assert.Len(t, pending, 1)
	})
}

// TestCapInvestigationPrompt is the universal last-resort backstop: whatever
// field or future provider inflates the assembled RCA prompt, no single prompt
// can exceed maxInvestigationPromptBytes, the event-context head + required
// instruction tail always survive, and the full prompt is queued for workspace
// offload so nothing is lost.
func TestCapInvestigationPrompt(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("within budget is unchanged, nothing queued", func(t *testing.T) {
		p := "small event investigation prompt"
		pending := map[string]string{}
		assert.Equal(t, p, capInvestigationPrompt(ctx, p, "evt6", pending))
		assert.Empty(t, pending)
	})

	t.Run("oversized prompt is bounded, head and tail survive, full text queued", func(t *testing.T) {
		head := "HEAD:event-id-and-definition"
		tail := "TAIL:### Related Alerts Check REQUIRED"
		p := head + strings.Repeat("Z", maxInvestigationPromptBytes*2) + tail

		pending := map[string]string{}
		out := capInvestigationPrompt(ctx, p, "evt7", pending)
		// Bounded near the cap (truncation marker + the grep-hint pointer add a
		// little), and far below the 2× input — the point is it can't run away.
		assert.LessOrEqual(t, len(out), maxInvestigationPromptBytes+1024)
		assert.Less(t, len(out), len(p))
		assert.Contains(t, out, head)
		assert.Contains(t, out, tail)
		// The full untruncated prompt is queued for the run-branch write.
		assert.Len(t, pending, 1)
		for _, content := range pending {
			assert.Equal(t, p, content)
		}
	})
}

// TestSaveOverflowToWorkspace_Disabled verifies the run-branch writer no-ops
// (rather than panicking on a nil DB) when there is no session.
func TestSaveOverflowToWorkspace_Disabled(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	assert.Equal(t, "", saveOverflowToWorkspace(ctx, "acct", "", "f.txt", "data"))
}
