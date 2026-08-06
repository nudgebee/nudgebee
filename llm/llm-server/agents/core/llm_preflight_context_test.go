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
	t.Run("no-op when a human message is present", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}}},
		}
		out := ensureUserMessage(in, "Qwen/Qwen3.6-35B-A3B-FP8")
		assert.Len(t, out, 2)
		assert.Equal(t, llms.ChatMessageTypeHuman, out[1].Role)
	})

	t.Run("appends a minimal human turn for a system-only Qwen prompt", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		out := ensureUserMessage(in, "Qwen/Qwen3.6-35B-A3B-FP8")
		assert.Len(t, out, 2)
		assert.Equal(t, llms.ChatMessageTypeSystem, out[0].Role, "cacheable system prefix must be untouched and first")
		assert.Equal(t, llms.ChatMessageTypeHuman, out[len(out)-1].Role, "user turn is appended LAST")
		assert.Equal(t, "Continue.", humanText(out))
	})

	t.Run("no-op for non-Qwen models that accept system-only prompts", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		out := ensureUserMessage(in, "gpt-4o")
		assert.Len(t, out, 1, "no user turn appended for models that accept system-only prompts")
	})

	t.Run("does not mutate the caller's slice", func(t *testing.T) {
		in := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		}
		_ = ensureUserMessage(in, "Qwen/Qwen3.6-35B-A3B-FP8")
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
