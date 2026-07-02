package egressfilter

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestWrapModel_RedactMode_NoRedactor_FallsBackToDetect pins the OSS
// contract: when the gate resolves ActionRedact but no Redactor is
// installed, the wrapper degrades to detect behaviour (forwards the
// original payload, emits a FilterEvent, records "detect" outcome) and
// warns once. Without this fallback, an OSS build with a custom gate
// that returned ActionRedact would panic (nil call) or silently forward
// the raw secret.
func TestWrapModel_RedactMode_NoRedactor_FallsBackToDetect(t *testing.T) {
	// Install a gate that returns ActionRedact but leave Redactor nil.
	prevGate := ActionGate
	prevRedactor := Redactor
	ActionGate = func(mode Mode, _ Result) Action {
		if mode == ModeRedact {
			return ActionRedact
		}
		return ActionAudit
	}
	Redactor = nil
	// Reset the sync.Once so this test's WARN is observable to a manual
	// runner without carrying state from previous tests.
	warnRedactWithoutRedactorOnce = sync.Once{}
	t.Cleanup(func() {
		ActionGate = prevGate
		Redactor = prevRedactor
	})

	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeRedact)

	origText := "hello ghp_Abcdefghijklmnopqrstuvwxyz0123456789 world"
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: origText},
		}},
	}
	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.NoError(t, err, "no Redactor should NOT block the call — degrades to detect")
	assert.Equal(t, 1, inner.callCount())
	require.Len(t, inner.lastMessages, 1)

	// Inner must have received the ORIGINAL text (no mutation attempted).
	got, _ := ExtractPartText(inner.lastMessages[0].Parts[0])
	assert.Equal(t, origText, got,
		"provider must receive the original payload when no Redactor is installed")
}
