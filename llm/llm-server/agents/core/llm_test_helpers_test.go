package core

// Shared test helpers for the LLM-generation path in package `core`.
//
// NOTE: this file currently carries only llmOverrideContext, the sole helper
// referenced by OSS's synced tests. The hermetic fakeLLMModel / withFakeLLMModel
// fakes from EE's suite are intentionally omitted here — the tests that exercise
// them (summarization / memory-v2) are not yet synced to OSS, and golangci-lint's
// `unused` linter rejects dead helpers. Restore them alongside those tests.

import (
	"context"

	"nudgebee/llm/security"
)

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
