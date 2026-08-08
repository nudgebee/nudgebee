package core

import (
	"strings"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// humanText returns the text of the first human message, or "" if none.
func humanText(msgs []llms.MessageContent) string {
	for _, m := range msgs {
		if m.Role == llms.ChatMessageTypeHuman {
			if len(m.Parts) > 0 {
				if tc, ok := m.Parts[0].(llms.TextContent); ok {
					return tc.Text
				}
			}
		}
	}
	return ""
}

func TestEnsureUserMessage(t *testing.T) {
	t.Run("no-op when a non-empty human message is present", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}}},
		}
		out, _ := ensureUserMessage(in)
		assert.Len(t, out, 2)
		assert.Equal(t, llms.ChatMessageTypeHuman, out[1].Role)
	})

	t.Run("appends a minimal human turn for a system-only prompt", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		out, _ := ensureUserMessage(in)
		assert.Len(t, out, 2)
		assert.Equal(t, llms.ChatMessageTypeSystem, out[0].Role, "cacheable system prefix must be untouched and first")
		assert.Equal(t, llms.ChatMessageTypeHuman, out[len(out)-1].Role, "user turn is appended LAST")
		assert.Equal(t, "Continue.", humanText(out))
	})

	t.Run("model-agnostic: appends regardless of model name", func(t *testing.T) {
		// The guard used to be gated on the model name containing "qwen"; a self-hosted
		// endpoint's served-model-name alias often omits it, so the guard must not depend
		// on the label. A system-only prompt is user-message-less for any model.
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		out, _ := ensureUserMessage(in)
		assert.Len(t, out, 2, "user turn appended even when the label isn't a recognizable Qwen name")
		assert.Equal(t, llms.ChatMessageTypeHuman, out[len(out)-1].Role)
	})

	t.Run("replaces content in-place when the only user message has empty content", func(t *testing.T) {
		// Appending would produce [System, Human(""), Human("Continue.")] — two
		// consecutive Human turns, which Anthropic's Messages API (strict
		// alternating roles) rejects with 400. In-place replace preserves the
		// alternating structure so the guard works for every provider, not just
		// Qwen. Also: dynamically find the first TextContent part rather than
		// hardcoding index 0 — a Human turn can carry image or binary parts
		// alongside text and hardcoding would corrupt non-text content.
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "   "}}},
		}
		out, _ := ensureUserMessage(in)
		assert.Len(t, out, 2, "empty user turn must be replaced in-place, NOT followed by a second Human turn")
		assert.Equal(t, llms.ChatMessageTypeHuman, out[1].Role)
		var text string
		for _, part := range out[1].Parts {
			if tc, ok := part.(llms.TextContent); ok {
				text = tc.Text
				break
			}
		}
		assert.Equal(t, "Continue.", text)
		// Caller's slice must not be mutated — the whitespace content must survive.
		assert.Equal(t, "   ", in[1].Parts[0].(llms.TextContent).Text, "caller's original message content must be preserved")
	})

	t.Run("in-place replace preserves image/binary parts alongside empty text", func(t *testing.T) {
		// A Human message with an image + empty text should be considered
		// usable (the image is usable content), so the guard is a no-op.
		// This test pins that hasUsableContent respects non-text parts.
		imagePart := llms.ImageURLContent{URL: "data:image/png;base64,aGk="}
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{imagePart, llms.TextContent{Text: ""}}},
		}
		out, _ := ensureUserMessage(in)
		assert.Len(t, out, 2, "human with an image is usable content, no rewrite needed")
		// Original image part unchanged.
		assert.Equal(t, imagePart, out[1].Parts[0])
	})

	t.Run("does not mutate the caller's slice", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		_, _ = ensureUserMessage(in)
		assert.Len(t, in, 1, "caller slice length must be unchanged")
	})
}

func TestApplyPreflightContextWindowCap(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	// huggingface provider routes CountTokens through the offline fallback vocab,
	// so these assertions are deterministic and network-free.
	const provider = "huggingface"
	const model = "Qwen/Qwen3.6-35B-A3B-FP8"

	t.Run("small in-window prompt is returned unchanged", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "you are a helpful assistant"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "what broke?"}}},
		}
		out := applyPreflightContextWindowCap(ctx, in, provider, model, &LLMConfigResolution{MaxContext: 32768}, "test")
		assert.Equal(t, in[0].Parts[0], out[0].Parts[0], "system message untouched")
		assert.Equal(t, "what broke?", humanText(out), "query untouched")
	})

	t.Run("over-window prompt is trimmed to fit without touching the query", func(t *testing.T) {
		bigScratchpad := strings.Repeat("tool observation line with some detail. ", 3000) // ~117KB
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: bigScratchpad}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "what is the root cause?"}}},
		}
		before, err := CalculateTotalTokens(ctx, in, provider, model)
		assert.NoError(t, err)

		// Small window so the trim path is exercised deterministically.
		out := applyPreflightContextWindowCap(ctx, in, provider, model, &LLMConfigResolution{MaxContext: 2000}, "test")

		after, err := CalculateTotalTokens(ctx, out, provider, model)
		assert.NoError(t, err)

		assert.Less(t, after, before, "trimming must reduce the total")
		assert.LessOrEqual(t, after, 2000, "trimmed prompt must fit inside the model window")
		assert.Equal(t, "what is the root cause?", humanText(out), "the user query is never trimmed")
		assert.Less(t, len(out[0].Parts[0].(llms.TextContent).Text), len(bigScratchpad), "the largest (scratchpad) message is shrunk")
		// Caller slice must be untouched.
		assert.Equal(t, bigScratchpad, in[0].Parts[0].(llms.TextContent).Text, "caller slice must not be mutated")
	})

	t.Run("nil resolution falls back to the model default window; small prompt unchanged", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("x", 500)}}},
		}
		out := applyPreflightContextWindowCap(ctx, in, provider, model, nil, "test")
		assert.Equal(t, in[0].Parts[0], out[0].Parts[0])
	})
}

// TestEnsureUserMessage_RewriteReason pins the rewrite-reason return channel
// that the caller (llm_common.go's GenerateAndTrackLLMContent) logs from —
// without a distinguishing reason, the WARN log would collapse "we appended
// a whole new turn" (usually a system-only prompt) and "we replaced an empty
// existing turn" (a caller passed query="") into one class, losing the
// signal about WHICH shape the offending caller emitted.
func TestEnsureUserMessage_RewriteReason(t *testing.T) {
	t.Run("no-op returns empty reason", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}}},
		}
		_, reason := ensureUserMessage(in)
		assert.Equal(t, ensureUserMessageNoOp, reason)
	})
	t.Run("system-only prompt reports appended", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		_, reason := ensureUserMessage(in)
		assert.Equal(t, ensureUserMessageAppended, reason)
	})
	t.Run("empty user turn reports replaced", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "   "}}},
		}
		_, reason := ensureUserMessage(in)
		assert.Equal(t, ensureUserMessageReplaced, reason)
	})
}
