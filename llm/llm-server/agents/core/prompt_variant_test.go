package core

import (
	"testing"

	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func TestPromptVariantForRequest(t *testing.T) {
	topLevelQuery := NBAgentRequest{AgentId: "a1", Query: "list pods", OriginalQuery: "list pods"}
	topLevelInvestigation := NBAgentRequest{AgentId: "a1", Query: "why is checkout-svc failing", OriginalQuery: "why is checkout-svc failing"}
	// Sub-agent: mechanical delegated brief, but the top-level ask was an investigation.
	subAgent := NBAgentRequest{AgentId: "kubectl", ParentAgentId: "a1", Query: "list pods", OriginalQuery: "why is checkout-svc failing"}
	invSource := NBAgentRequest{AgentId: "a1", Query: "list pods", OriginalQuery: "list pods", ConversationSource: ConversationSourceInvestigation}
	origQueryEmpty := NBAgentRequest{AgentId: "a1", Query: "list pods"} // OriginalQuery empty → fall back to Query
	bothQueriesEmpty := NBAgentRequest{AgentId: "a1"}                   // Both empty → degenerate → full prompt

	assert.Equal(t, llmruntime.PromptVariantLean, promptVariantForRequest(topLevelQuery), "top-level plain retrieval → lean")
	assert.Equal(t, "", promptVariantForRequest(topLevelInvestigation), "top-level investigation → full")
	assert.Equal(t, "", promptVariantForRequest(subAgent), "sub-agent classifies on OriginalQuery → full under an investigation")
	assert.Equal(t, "", promptVariantForRequest(invSource), "investigation conversation source → full")
	assert.Equal(t, llmruntime.PromptVariantLean, promptVariantForRequest(origQueryEmpty), "empty OriginalQuery falls back to Query → lean")
	assert.Equal(t, "", promptVariantForRequest(bothQueriesEmpty), "both queries empty → full")
}

func TestApplyPromptVariant_AlwaysResets(t *testing.T) {
	base := security.NewRequestContextForSuperAdmin()

	// Top-level plain retrieval → lean, readable by the same accessor the cache key uses.
	lean := applyPromptVariant(base, NBAgentRequest{AgentId: "a1", OriginalQuery: "list pods"})
	assert.Equal(t, llmruntime.PromptVariantLean, llmruntime.PromptVariantFromCtx(lean))

	// A sub-agent invoked with the parent's (lean) context must NOT inherit "lean":
	// applyPromptVariant re-stamps every agent, mirroring applyAgentModelTier.
	sub := applyPromptVariant(lean, NBAgentRequest{AgentId: "kubectl", ParentAgentId: "a1", Query: "list pods", OriginalQuery: "list pods"})
	assert.Equal(t, "", llmruntime.PromptVariantFromCtx(sub), "sub-agent must reset the inherited lean variant to full")

	// A full (investigation) top-level turn resolves to "" and does not mutate base.
	inv := applyPromptVariant(base, NBAgentRequest{AgentId: "a1", OriginalQuery: "why is checkout failing"})
	assert.Equal(t, "", llmruntime.PromptVariantFromCtx(inv))
	assert.Equal(t, "", llmruntime.PromptVariantFromCtx(base))
}

// A lean turn dropped the notebook discipline from its prompt, so the runtime
// notebook/answer-contract nudges (which gate on notebookSectionEnabled) must
// stay silent — otherwise the model is nudged about concepts it was never taught.
func TestNotebookSectionEnabled_LeanModeSilencesNotebook(t *testing.T) {
	lean := &NBReActPlanner3{leanMode: true}
	assert.False(t, lean.notebookSectionEnabled(), "lean turn must disable the notebook runtime")

	full := &NBReActPlanner3{leanMode: false} // nil agent → default enabled, unchanged behavior
	assert.True(t, full.notebookSectionEnabled(), "non-lean default keeps the notebook runtime")
}
