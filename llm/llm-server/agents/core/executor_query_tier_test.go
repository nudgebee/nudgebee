package core

import (
	"context"
	"testing"

	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setDownshift toggles the query model-downshift flag and returns a restore func.
func setDownshift(v bool) func() {
	prev := config.Config.LlmServerOrchestratorQueryModelDownshiftEnabled
	config.Config.LlmServerOrchestratorQueryModelDownshiftEnabled = v
	return func() { config.Config.LlmServerOrchestratorQueryModelDownshiftEnabled = prev }
}

// Exercise the shared executor's tier stamp, model resolution, and engine
// selection together: either engine must use the same downshift policy.
func TestQueryModelDownshift_BothEngines(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-3-flash-preview")
	setEnvKey(t, "llm_tier_provider_reasoning", "googleai")
	setEnvKey(t, "llm_tier_model_reasoning", "gemini-3.1-pro-preview")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	setEnvKey(t, "llm_tier_model_summary", "gemini-3-flash-preview")
	previous := config.Config.LlmServerPlannerEngine
	t.Cleanup(func() { config.Config.LlmServerPlannerEngine = previous })
	agent := catTestCategorisedAgent{category: llmruntime.ModelTierReasoning}
	for _, engine := range []struct {
		name   string
		engine string
		react4 bool
	}{{"react3", PlannerEngineReAct3, false}, {"react4", PlannerEngineReAct4, true}} {
		t.Run(engine.name, func(t *testing.T) {
			config.Config.LlmServerPlannerEngine = engine.engine
			for _, tc := range []struct {
				name    string
				enabled bool
				request NBAgentRequest
				want    llmruntime.ModelTier
				model   string
			}{
				{"disabled query", false, NBAgentRequest{OriginalQuery: "list pods"}, llmruntime.ModelTierReasoning, "gemini-3.1-pro-preview"},
				{"enabled query", true, NBAgentRequest{OriginalQuery: "list pods"}, llmruntime.ModelTierSummary, "gemini-3-flash-preview"},
				{"investigation", true, NBAgentRequest{OriginalQuery: "why is the api pod crashlooping"}, llmruntime.ModelTierReasoning, "gemini-3.1-pro-preview"},
				{"sub-agent", true, NBAgentRequest{OriginalQuery: "list pods", AgentId: "child", ParentAgentId: "parent"}, llmruntime.ModelTierReasoning, "gemini-3.1-pro-preview"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					defer setDownshift(tc.enabled)()
					ctx := applyAgentModelTier(security.NewRequestContextForSuperAdmin(), agent, tc.request)
					require.Equal(t, tc.want, llmruntime.ModelTierFromContext(ctx))
					require.Equal(t, tc.model, llmruntime.GetLLMModelName(ctx, "", "googleai", agent.GetName(), true, ""))
					require.Equal(t, engine.react4, useReAct4Engine(ctx, agent, tc.request))
				})
			}
		})
	}
}

// TestIsTopLevelPlainRetrievalTurn covers the single classification that drives both
// the lean prompt variant and the query model downshift.
func TestIsTopLevelPlainRetrievalTurn(t *testing.T) {
	cases := []struct {
		name string
		req  NBAgentRequest
		want bool
	}{
		{"top-level query", NBAgentRequest{OriginalQuery: "list pods in the default namespace"}, true},
		{"top-level investigation", NBAgentRequest{OriginalQuery: "why is the api pod crashlooping"}, false},
		{"sub-agent brief (parent set)", NBAgentRequest{OriginalQuery: "list pods", AgentId: "a2", ParentAgentId: "a1"}, false},
		{"empty query → false (keep full/pro)", NBAgentRequest{}, false},
		{"investigation source overrides", NBAgentRequest{OriginalQuery: "list pods", ConversationSource: ConversationSourceInvestigation}, false},
		{"falls back to Query when OriginalQuery empty", NBAgentRequest{Query: "list pods"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isTopLevelPlainRetrievalTurn(c.req))
		})
	}
}

// TestResolveModelTier is the core of #12: a top-level query on a Reasoning
// orchestrator downshifts to Summary ONLY when the flag is on; everything else
// keeps the agent's declared tier.
func TestResolveModelTier(t *testing.T) {
	reasoning := catTestCategorisedAgent{category: llmruntime.ModelTierReasoning}
	query := NBAgentRequest{OriginalQuery: "list pods in the default namespace"}
	investigation := NBAgentRequest{OriginalQuery: "why is the api pod crashlooping"}
	subAgentQuery := NBAgentRequest{OriginalQuery: "list pods", AgentId: "a2", ParentAgentId: "a1"}

	t.Run("flag off → base tier (no-op, byte-identical to today)", func(t *testing.T) {
		defer setDownshift(false)()
		assert.Equal(t, llmruntime.ModelTierReasoning, resolveModelTier(nil, reasoning, query))
	})
	t.Run("flag on + top-level query + Reasoning → Summary", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, llmruntime.ModelTierSummary, resolveModelTier(nil, reasoning, query))
	})
	t.Run("flag on + investigation → Reasoning (no downshift)", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, llmruntime.ModelTierReasoning, resolveModelTier(nil, reasoning, investigation))
	})
	t.Run("flag on + sub-agent query → no downshift (not top-level)", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, llmruntime.ModelTierReasoning, resolveModelTier(nil, reasoning, subAgentQuery))
	})
	t.Run("flag on + non-Reasoning agent → unchanged (only Reasoning downshifts)", func(t *testing.T) {
		defer setDownshift(true)()
		retrieval := catTestCategorisedAgent{category: llmruntime.ModelTierRetrieval}
		assert.Equal(t, llmruntime.ModelTierRetrieval, resolveModelTier(nil, retrieval, query))
	})
}

// ctxWithModelConfigKey stamps one explicit-model-config context key, mirroring
// what conversation.go does before the executor runs.
func ctxWithModelConfigKey(key llmruntime.LLMContextKey, val any) *security.RequestContext {
	base := security.NewRequestContextForSuperAdmin()
	goCtx := context.WithValue(base.GetContext(), key, val)
	return security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
}

// TestResolveModelTier_ExplicitConfigNeverDownshifted: a turn that carries ANY
// user-chosen model configuration keeps the agent's declared tier even when the
// downshift flag is on — the flag optimizes the default path only.
func TestResolveModelTier_ExplicitConfigNeverDownshifted(t *testing.T) {
	defer setDownshift(true)()
	reasoning := catTestCategorisedAgent{category: llmruntime.ModelTierReasoning}
	query := NBAgentRequest{OriginalQuery: "list pods in the default namespace"}

	cases := []struct {
		name string
		ctx  *security.RequestContext
	}{
		{"blanket provider override", ctxWithModelConfigKey(llmruntime.ContextKeyLlmProviderOverride, "openai")},
		{"blanket model override", ctxWithModelConfigKey(llmruntime.ContextKeyLlmModelOverride, "gpt-5")},
		{"per-tier picks", ctxWithModelConfigKey(llmruntime.ContextKeyLlmTierModelOverrides,
			llmruntime.ConversationTierOverrides{Picks: map[string]llmruntime.TierModelPick{"reasoning": {Provider: "openai", Model: "gpt-5"}}})},
		{"config-source pin", ctxWithModelConfigKey(llmruntime.ContextKeyLlmConfigSourceOverride, "db:11111111-1111-1111-1111-111111111111:all")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, llmruntime.ModelTierReasoning, resolveModelTier(c.ctx, reasoning, query),
				"explicit config must never be downshifted")
		})
	}

	t.Run("no explicit config → downshift still applies", func(t *testing.T) {
		base := security.NewRequestContextForSuperAdmin()
		assert.Equal(t, llmruntime.ModelTierSummary, resolveModelTier(base, reasoning, query))
	})
	t.Run("empty tier-picks map does not block the downshift", func(t *testing.T) {
		ctx := ctxWithModelConfigKey(llmruntime.ContextKeyLlmTierModelOverrides, llmruntime.ConversationTierOverrides{})
		assert.Equal(t, llmruntime.ModelTierSummary, resolveModelTier(ctx, reasoning, query))
	})
}
