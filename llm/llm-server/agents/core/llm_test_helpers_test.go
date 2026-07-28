package core

// Shared test helpers for the LLM-generation path in package `core`.
//
//   - withFakeLLMModel — overrides the package-level `newLLMModel` seam so a
//     test-supplied llms.Model fake is injected, restoring the original on
//     cleanup. Tests bring their own fake (e.g. stallThenSucceedModel,
//     optionCapturingLLMModel) — this only swaps the seam.
//   - newFakeContentResponse — builds a minimal llms.ContentResponse with a
//     token-usage GenerationInfo, for fakes to return.
//   - llmOverrideContext — a RequestContext carrying per-request provider/model
//     overrides so the generation path resolves a model without mutating the
//     global config.Config (keeps tests parallel-safe).
//
// NOTE: the fuller EE suite also ships a generic fakeLLMModel/fakeTurn fake;
// it's omitted here until a synced OSS test needs it (golangci-lint's `unused`
// rejects dead helpers). Restore it from EE alongside the test that uses it.

import (
	"context"
	"testing"

	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

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

// withFakeLLMModel overrides the package-level newLLMModel seam so
// GenerateAndTrackLLMContent uses the supplied fake instead of constructing a
// real provider client, restoring the original on test cleanup.
//
// NOT parallel-safe: newLLMModel is a process-global, so do not call
// t.Parallel() in a test that uses this helper. Provider/model selection still
// flows per-request via llmOverrideContext (no config.Config mutation), so the
// default sequential tests are race-free, as the -race suite confirms.
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
