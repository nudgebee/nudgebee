package core

// Shared hermetic-test helpers for the LLM-generation path.
//
// These let any test in package `core` exercise code that calls
// GenerateAndTrackLLMContent (summarization, planners, solver, critiquer,
// followup, memory, …) WITHOUT real provider credentials or network calls:
//
//   - fakeLLMModel / fakeTurn — an in-memory llms.Model returning scripted
//     responses (single-response or per-call sequenced via `turns`).
//   - withFakeLLMModel — overrides the package-level `newLLMModel` seam so the
//     fake is injected, and restores it on test cleanup.
//   - llmOverrideContext — a RequestContext that resolves a provider/model via
//     per-request context overrides (no global config.Config mutation), so tests
//     stay parallel-safe and free of cross-test contamination.
//
// Prefer these over hand-rolling another llms.Model fake.

import (
	"context"
	"sync"
	"testing"

	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// fakeTurn scripts one model response for the sequenced fake — used to drive
// multi-call paths such as the truncation/continuation loop.
type fakeTurn struct {
	content    string
	stopReason string // defaults to "stop" when empty
	err        error
}

// fakeLLMModel is a hermetic llms.Model used to exercise the LLM-generation
// path (summarization, and any other caller of GenerateAndTrackLLMContent)
// without real provider credentials or network calls. It records what it was
// asked to generate and returns a canned response (or error). Set `turns` to
// script a different response per call; otherwise every call returns `response`
// (or `err`) with a "stop" reason.
type fakeLLMModel struct {
	mu           sync.Mutex // guards calls/lastMessages — the model is shared across goroutines on parallel paths
	response     string
	err          error
	turns        []fakeTurn
	calls        int
	lastMessages []llms.MessageContent
}

// callCount returns how many times the model was invoked (lock-safe).
func (f *fakeLLMModel) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// lastSent returns the messages from the most recent invocation (lock-safe).
func (f *fakeLLMModel) lastSent() []llms.MessageContent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastMessages
}

func newFakeContentResponse(content, stopReason string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:    content,
			StopReason: stopReason,
			GenerationInfo: map[string]any{
				"PromptTokens":     1,
				"CompletionTokens": 1,
				"TotalTokens":      2,
			},
		}},
	}
}

func (f *fakeLLMModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastMessages = messages
	i := f.calls
	f.calls++
	if len(f.turns) > 0 {
		turn := f.turns[len(f.turns)-1] // clamp: reuse the last scripted turn for extra calls
		if i < len(f.turns) {
			turn = f.turns[i]
		}
		if turn.err != nil {
			return nil, turn.err
		}
		stop := turn.stopReason
		if stop == "" {
			stop = "stop"
		}
		return newFakeContentResponse(turn.content, stop), nil
	}
	if f.err != nil {
		return nil, f.err
	}
	return newFakeContentResponse(f.response, "stop"), nil
}

func (f *fakeLLMModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

// withFakeLLMModel overrides the package-level newLLMModel seam so
// GenerateAndTrackLLMContent uses the supplied fake instead of constructing a
// real provider client, restoring the original on test cleanup.
//
// NOT parallel-safe: newLLMModel is a process-global, so do not call
// t.Parallel() in a test that uses this helper (nor in any sibling that could
// run concurrently with it) — the swap would race. This is the standard
// trade-off of the global-seam idiom; threading the model through the ~80
// callers of GenerateAndTrackLLMContent instead would defeat the seam's
// purpose. The model var is the only global touched — provider/model selection
// still flows per-request via llmOverrideContext (no config.Config mutation) —
// so the default sequential tests are race-free, as the -race suite confirms.
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
