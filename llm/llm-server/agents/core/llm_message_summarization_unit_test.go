package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// The hermetic LLM-model fake and seam helpers (fakeLLMModel, fakeTurn,
// withFakeLLMModel, llmOverrideContext) live in llm_test_helpers_test.go so any
// test in this package can reuse them.

// TestGenerateAndTrackLLMContent_SeamUsesInjectedModel proves the seam itself:
// the central generation chokepoint (called from ~80 sites — planners, agents,
// summarization) can be driven by a fake model. This is what makes the rest of
// the LLM path hermetically testable, not just summarization.
func TestGenerateAndTrackLLMContent_SeamUsesInjectedModel(t *testing.T) {
	fake := &fakeLLMModel{response: "hello from fake"}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	resp, err := GenerateAndTrackLLMContent(
		ctx, "user", "" /*accountId*/, "conv", "msg", "agent",
		false, /*trackContent — no DB writes*/
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "summarize this")},
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	assert.Equal(t, "hello from fake", resp.Choices[0].Content)
	assert.Equal(t, 1, fake.callCount(), "the injected model should be called exactly once")
}

// TestSummarizeTextChunk_FakeLLM verifies the leaf summarization call end-to-end
// against a fake model: it wraps the content in the summarization prompt, calls
// the model, and returns the model's text.
func TestSummarizeTextChunk_FakeLLM(t *testing.T) {
	fake := &fakeLLMModel{response: "CONCISE SUMMARY"}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	content := "a long block of text that needs summarizing, with an ID abc-123 to preserve"

	got := SummarizeTextChunk(ctx, nil /*llm param is unused*/, content, "", "agent", "conv", "msg", "user")

	assert.Equal(t, "CONCISE SUMMARY", got)
	require.Equal(t, 1, fake.callCount())
	require.NotEmpty(t, fake.lastSent())
	// The prompt sent to the model must embed the original content verbatim.
	var sent strings.Builder
	for _, m := range fake.lastSent() {
		for _, p := range m.Parts {
			if tc, ok := p.(llms.TextContent); ok {
				sent.WriteString(tc.Text)
			}
		}
	}
	assert.Contains(t, sent.String(), content, "content should be embedded in the summarization prompt")
	assert.Contains(t, sent.String(), "summarize", "prompt should instruct the model to summarize")
}

// TestSummarizeTextChunk_ModelError_ReturnsEmpty verifies the documented failure
// contract: on an LLM error the chunk summarizer returns "" (caller treats an
// empty summary as "skip"), rather than propagating the error or panicking.
func TestSummarizeTextChunk_ModelError_ReturnsEmpty(t *testing.T) {
	fake := &fakeLLMModel{err: errors.New("provider exploded")}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	got := SummarizeTextChunk(ctx, nil, "some content", "", "agent", "conv", "msg", "user")

	assert.Equal(t, "", got)
}

// --- SummarizeContent branching ---
// These hermetic tests cover the size-based routing in SummarizeContent that
// previously could only be exercised against a live provider (the now-removed
// TestSummarizeContent_LargeInput e2e smoke test). The override context resolves
// to gpt-4o (max ~16k tokens ⇒ per-chunk budget ~8k), which sets the thresholds.

// TestSummarizeContent_ShortContent_ReturnedUnchanged: content below the
// min-token threshold is returned verbatim with no LLM call.
func TestSummarizeContent_ShortContent_ReturnedUnchanged(t *testing.T) {
	fake := &fakeLLMModel{response: "SHOULD NOT BE CALLED"}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	content := "a short message well under the summarization threshold"

	got := SummarizeContent(ctx, nil, content, "", "agent", "conv", "msg", "user")

	assert.Equal(t, content, got, "content below the min-token threshold is returned unchanged")
	assert.Equal(t, 0, fake.callCount(), "short content must not trigger an LLM call")
}

// TestSummarizeContent_MediumContent_SingleChunk: content above the min-token
// threshold but within one chunk is summarized in a single call.
func TestSummarizeContent_MediumContent_SingleChunk(t *testing.T) {
	fake := &fakeLLMModel{response: "ONE-SHOT SUMMARY"}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	content := strings.Repeat("This sentence carries enough tokens to require summarization. ", 30)

	got := SummarizeContent(ctx, nil, content, "", "agent", "conv", "msg", "user")

	assert.Equal(t, "ONE-SHOT SUMMARY", got)
	assert.Equal(t, 1, fake.callCount(), "medium content is summarized in a single LLM call")
}

// TestSummarizeContent_LargeContent_ChunkedAndCombined: content that exceeds the
// per-chunk budget is split, each chunk summarized, and the results combined.
func TestSummarizeContent_LargeContent_ChunkedAndCombined(t *testing.T) {
	fake := &fakeLLMModel{response: "CHUNKSUM"}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	// Comfortably exceeds the ~8k-token per-chunk budget so multiple chunks are produced.
	content := strings.Repeat("This is a long line of content that must be chunked before summarization. ", 2000)

	got := SummarizeContent(ctx, nil, content, "", "agent", "conv", "msg", "user")

	assert.GreaterOrEqual(t, fake.callCount(), 2, "oversized content is split into multiple summarization calls")
	assert.Contains(t, got, "CHUNKSUM", "the combined result is built from the per-chunk summaries")
}

// --- Generation-core: truncation/continuation loop ---

// TestGenerateAndTrackLLMContent_ContinuesOnTruncation: when the model stops with
// a max_tokens reason, GenerateAndTrackLLMContent runs the continuation loop and
// concatenates chunks until a non-truncated stop reason ends it.
func TestGenerateAndTrackLLMContent_ContinuesOnTruncation(t *testing.T) {
	fake := &fakeLLMModel{turns: []fakeTurn{
		{content: "part one ", stopReason: "max_tokens"},
		{content: "part two", stopReason: "stop"},
	}}
	withFakeLLMModel(t, fake)

	ctx := llmOverrideContext()
	resp, err := GenerateAndTrackLLMContent(
		ctx, "user", "" /*accountId*/, "conv", "msg", "agent",
		false, /*trackContent*/
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "write a long answer")},
		false,
	)

	require.NoError(t, err)
	require.NotEmpty(t, resp.Choices)
	assert.Equal(t, "part one part two", resp.Choices[0].Content, "truncated response is continued and concatenated")
	assert.Equal(t, 2, fake.callCount(), "one initial call plus one continuation")
}
