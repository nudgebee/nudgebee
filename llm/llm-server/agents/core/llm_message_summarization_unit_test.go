package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// fakeLLMModel is a hermetic llms.Model used to exercise the LLM-generation
// path (summarization, and any other caller of GenerateAndTrackLLMContent)
// without real provider credentials or network calls. It records what it was
// asked to generate and returns a canned response (or error).
type fakeLLMModel struct {
	response     string
	err          error
	calls        int
	lastMessages []llms.MessageContent
}

func (f *fakeLLMModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	f.calls++
	f.lastMessages = messages
	if f.err != nil {
		return nil, f.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:    f.response,
			StopReason: "stop",
			GenerationInfo: map[string]any{
				"PromptTokens":     1,
				"CompletionTokens": 1,
				"TotalTokens":      2,
			},
		}},
	}, nil
}

func (f *fakeLLMModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

// withFakeLLMModel overrides the newLLMModel seam so GenerateAndTrackLLMContent
// uses the supplied fake instead of constructing a real provider client. The
// override is restored on test cleanup. Provider/model selection is supplied
// per-request via llmOverrideContext (no global config mutation), so this is
// safe under concurrent test execution.
func withFakeLLMModel(t *testing.T, fake llms.Model) {
	t.Helper()

	prevFn := newLLMModel
	newLLMModel = func(_ string, _ string, _ string, _ bool, _ string, _ ...*LLMConfigResolution) (llms.Model, error) {
		return fake, nil
	}
	t.Cleanup(func() { newLLMModel = prevFn })
}

// llmOverrideContext returns a RequestContext carrying explicit per-request
// provider/model overrides. ResolveLLMConfig reads these (highest precedence)
// from the context, so the generation path resolves a model without mutating
// the global config.Config — keeping these tests free of cross-test data races.
func llmOverrideContext() *security.RequestContext {
	base := security.NewRequestContextForSuperAdmin()
	goCtx := context.WithValue(base.GetContext(), ContextKeyLlmProviderOverride, "openai")
	goCtx = context.WithValue(goCtx, ContextKeyLlmModelOverride, "gpt-4o")
	return security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
}

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
	assert.Equal(t, 1, fake.calls, "the injected model should be called exactly once")
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
	require.Equal(t, 1, fake.calls)
	require.NotEmpty(t, fake.lastMessages)
	// The prompt sent to the model must embed the original content verbatim.
	var sent strings.Builder
	for _, m := range fake.lastMessages {
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
