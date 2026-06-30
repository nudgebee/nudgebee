package core

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// ─────────────────────────────────────────────────────────────────────────────
// agentModelCategory — the agent-declared category resolver
// ─────────────────────────────────────────────────────────────────────────────

// catTestAgent is a minimal NBAgent that declares no category.
type catTestAgent struct{}

func (catTestAgent) GetName() string                                              { return "cat-test" }
func (catTestAgent) GetNameAliases() []string                                     { return nil }
func (catTestAgent) GetDescription() string                                       { return "" }
func (catTestAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool { return nil }
func (catTestAgent) GetSystemPrompt(*security.RequestContext, NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (catTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }

// catTestCategorisedAgent additionally implements NBAgentCategoryProvider.
type catTestCategorisedAgent struct {
	catTestAgent
	category ModelTier
}

func (a catTestCategorisedAgent) GetModelCategory() ModelTier { return a.category }

func TestAgentModelCategory(t *testing.T) {
	// An agent that does not implement NBAgentCategoryProvider → Retrieval default.
	assert.Equal(t, ModelTierRetrieval, agentModelCategory(catTestAgent{}),
		"agent without the optional interface → Retrieval (fleet default)")

	// An agent that declares a category → that category.
	for _, tier := range []ModelTier{ModelTierReasoning, ModelTierRetrieval, ModelTierSummary} {
		assert.Equal(t, tier, agentModelCategory(catTestCategorisedAgent{category: tier}),
			"declared category %s is returned", tier)
	}

	// An agent may still declare the empty tier to opt into the global default
	// (the escape hatch) instead of the Retrieval floor.
	assert.Equal(t, ModelTier(""), agentModelCategory(catTestCategorisedAgent{category: ""}))
}

// applyAgentModelTier must override an inherited tier for a category-less agent
// — stamping the Retrieval default — so a tool sub-agent invoked under a
// Reasoning-tier parent (its context already carries ModelTierReasoning) does
// not silently run on the pro model. A categorised agent stamps its own tier.
func TestApplyAgentModelTier_ResetsInheritedTier(t *testing.T) {
	// Simulate a sub-agent invoked with the parent investigation's context,
	// which already carries the Reasoning tier.
	parentCtx := security.NewRequestContext(
		context.WithValue(context.Background(), ContextKeyModelTier, ModelTierReasoning),
		nil, slog.Default(), nil, nil,
	)
	assert.Equal(t, ModelTierReasoning, modelTierFromContext(parentCtx),
		"precondition: parent context carries the Reasoning tier")

	// A category-less tool agent must NOT inherit the parent's Reasoning tier;
	// it is stamped with the Retrieval default instead.
	resetCtx := applyAgentModelTier(parentCtx, catTestAgent{})
	assert.Equal(t, ModelTierRetrieval, modelTierFromContext(resetCtx),
		"category-less agent overrides the inherited tier → Retrieval default")

	// A categorised agent stamps its own tier even over an inherited one.
	for _, tier := range []ModelTier{ModelTierReasoning, ModelTierRetrieval, ModelTierSummary} {
		got := applyAgentModelTier(parentCtx, catTestCategorisedAgent{category: tier})
		assert.Equal(t, tier, modelTierFromContext(got),
			"declared category %s must be stamped onto the context", tier)
	}

	// Defensive guards: must not panic on a nil ctx or a zero-value
	// RequestContext whose internal context.Context is nil (e.g. planner stubs).
	assert.NotPanics(t, func() {
		assert.Nil(t, applyAgentModelTier(nil, catTestAgent{}))
		zero := applyAgentModelTier(&security.RequestContext{}, catTestCategorisedAgent{category: ModelTierReasoning})
		assert.Equal(t, ModelTierReasoning, modelTierFromContext(zero),
			"zero-value ctx falls back to context.Background() and still stamps the tier")
	})
}

// End-to-end through the real resolver: with the deployed tier config
// (global = flash, reasoning = pro, retrieval = qwen), a category-less sub-agent
// invoked under a Reasoning-tier parent must resolve the RETRIEVAL-tier model
// (the fleet default), not the inherited pro model. This exercises the full
// ResolveLLMConfig layered resolution — the same path that picks the model
// written to llm_conversation_token_usage at runtime.
func TestApplyAgentModelTier_E2E_CategoryLessResolvesRetrievalNotPro(t *testing.T) {
	// Mirror the deployed config: global flash, reasoning pro, retrieval qwen.
	pinGlobalModel(t, "googleai", "gemini-3-flash-preview")
	setEnvKey(t, "llm_tier_model_reasoning", "gemini-3.1-pro-preview")
	setEnvKey(t, "llm_tier_provider_reasoning", "googleai")
	setEnvKey(t, "llm_tier_model_retrieval", "qwen-retrieval")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")

	// A sub-agent is invoked with the parent investigation's context, which
	// already carries the Reasoning tier (set when the parent agent declared it).
	parentCtx := newCtxWithKVs(ContextKeyModelTier, ModelTierReasoning)

	// Baseline (the bug): inheriting the parent's Reasoning tier resolves pro.
	resInherited, err := ResolveLLMConfig(parentCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro-preview", resInherited.Model,
		"baseline: a category-less agent that inherits the parent Reasoning tier resolves the pro model")

	// The fix: a category-less agent is stamped the Retrieval default → resolves
	// the retrieval-tier model, not the inherited pro model.
	fixedCtx := applyAgentModelTier(parentCtx, catTestAgent{})
	resFixed, err := ResolveLLMConfig(fixedCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "qwen-retrieval", resFixed.Model,
		"fix: a category-less sub-agent resolves the Retrieval-tier model, not the inherited pro model")
	assert.NotEqual(t, "gemini-3.1-pro-preview", resFixed.Model)

	// A genuinely reasoning-tier agent still opts into pro — the fix does not
	// downgrade agents that declare the category.
	catCtx := applyAgentModelTier(parentCtx, catTestCategorisedAgent{category: ModelTierReasoning})
	resCat, err := ResolveLLMConfig(catCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro-preview", resCat.Model,
		"a reasoning-category agent still resolves the pro model")
}

// pinGlobalFallback sets the ENV-global fallback chain for the test.
func pinGlobalFallback(t *testing.T, value string) {
	t.Helper()
	prev := config.Config.LlmModelFallbacks
	config.Config.LlmModelFallbacks = value
	t.Cleanup(func() { config.Config.LlmModelFallbacks = prev })
}

// The model-fallback chain (getLLMFallbackModelName) is category-aware: a
// category can declare its own llm_tier_model_fallbacks_<tier> list, slotted
// between the global and agent layers — same precedence as the primary model.
func TestGetLLMFallbackModelName_TierLayer(t *testing.T) {
	t.Run("env-tier fallback resolves", func(t *testing.T) {
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "model-a,model-b")
		assert.Equal(t, "model-a,model-b", getLLMFallbackModelName("", "", ModelTierSummary, false))
	})

	t.Run("tier fallback beats global fallback", func(t *testing.T) {
		pinGlobalFallback(t, "global-fb")
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "tier-fb")
		assert.Equal(t, "tier-fb", getLLMFallbackModelName("", "", ModelTierSummary, false))
	})

	t.Run("db-tier fallback beats env-tier", func(t *testing.T) {
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "env-tier-fb")
		seedDBConfig(t, "acct-fb", map[string]string{"llm_tier_model_fallbacks_summary": "db-tier-fb"})
		assert.Equal(t, "db-tier-fb", getLLMFallbackModelName("acct-fb", "", ModelTierSummary, false))
	})

	t.Run("agent fallback beats tier fallback", func(t *testing.T) {
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "tier-fb")
		setEnvKey(t, "llm_model_fallbacks_agentx", "agent-fb")
		assert.Equal(t, "agent-fb", getLLMFallbackModelName("", "agentx", ModelTierSummary, true))
	})

	t.Run("untagged skips the tier fallback layer", func(t *testing.T) {
		pinGlobalFallback(t, "global-fb")
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "tier-fb")
		assert.Equal(t, "global-fb", getLLMFallbackModelName("", "", ModelTier(""), false),
			"untagged → tier fallback layer skipped → global fallback")
	})

	t.Run("category isolation", func(t *testing.T) {
		setEnvKey(t, "llm_tier_model_fallbacks_summary", "summary-fb")
		assert.NotEqual(t, "summary-fb", getLLMFallbackModelName("", "", ModelTierReasoning, false),
			"Reasoning does not see Summary's fallback list")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Categories under test. Every ResolveLLMConfig layer below is verified against
// each category it can apply to. The empty tier ("") is the untagged/normal-flow
// call — it never opts into the tier layers.
//
// Precedence (lowest → highest), per the package docstring. ENV block first,
// then DB block on top; explicit per-request overrides above both. DB always
// beats ENV at any specificity:
//
//	                            untagged   Retrieval   Summary
//	  env-global                   ✓          ✓           ✓
//	  env-tier                     n/a         ✓           ✓      (untagged skips tier layers)
//	  env-agent                    ✓          ✓           ✓
//	  db-global                    ✓          ✓           ✓      ← DB block sits above ENV block
//	  db-tier                      n/a         ✓           ✓
//	  db-agent                     ✓          ✓           ✓
//	  conversation                 ✓          ✓           ✓
//	  context-override             ✓          ✓           ✓
// ─────────────────────────────────────────────────────────────────────────────

var everyTier = []ModelTier{ModelTier(""), ModelTierReasoning, ModelTierRetrieval, ModelTierSummary}

// categoryTiers are the 3 opt-in categories — all have a tier config layer.
var categoryTiers = []ModelTier{ModelTierReasoning, ModelTierRetrieval, ModelTierSummary}

// ─── helpers ─────────────────────────────────────────────────────────────────

// pinGlobalModel sets the ENV-global provider/model config for the test.
func pinGlobalModel(t *testing.T, provider, model string) {
	t.Helper()
	p, m := config.Config.LlmProvider, config.Config.LlmModel
	config.Config.LlmProvider = provider
	config.Config.LlmModel = model
	t.Cleanup(func() {
		config.Config.LlmProvider = p
		config.Config.LlmModel = m
	})
}

// setEnvKey sets a dynamic viper config key (agent- or tier-scoped) for the test.
func setEnvKey(t *testing.T, key, value string) {
	t.Helper()
	prev := config.Config.GetString(key, "")
	config.Config.SetString(key, value)
	t.Cleanup(func() { config.Config.SetString(key, prev) })
}

// seedDBConfig pre-populates the LLM integration-config cache so ResolveLLMConfig
// resolves the DB layers without touching a database.
func seedDBConfig(t *testing.T, accountId string, cfg map[string]string) {
	t.Helper()
	llmIntegrationConfigCacheMutex.Lock()
	llmIntegrationConfigCache[accountId] = struct {
		config map[string]string
		ts     time.Time
	}{config: cfg, ts: time.Now()}
	llmIntegrationConfigCacheMutex.Unlock()
	t.Cleanup(func() {
		llmIntegrationConfigCacheMutex.Lock()
		delete(llmIntegrationConfigCache, accountId)
		llmIntegrationConfigCacheMutex.Unlock()
	})
}

// seedConversationOverride pre-populates the conversation-override cache so the
// conversation layer resolves without a database.
func seedConversationOverride(t *testing.T, conversationId, provider, model string) {
	t.Helper()
	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = conversationOverrideEntry{
		provider: provider,
		model:    model,
		ts:       time.Now(),
	}
	conversationOverrideCacheMutex.Unlock()
	t.Cleanup(func() {
		conversationOverrideCacheMutex.Lock()
		delete(conversationOverrideCache, conversationId)
		conversationOverrideCacheMutex.Unlock()
	})
}

// newCtxWithKVs builds a RequestContext carrying the given key/value pairs.
func newCtxWithKVs(kv ...any) *security.RequestContext {
	parent := context.Background()
	for i := 0; i+1 < len(kv); i += 2 {
		parent = context.WithValue(parent, kv[i], kv[i+1])
	}
	return security.NewRequestContext(parent, security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// erroringConversationDao is an IConversationDao whose conversation lookup
// always fails — used to exercise the conversation-layer error path. Only
// GetConversation is implemented; ResolveLLMConfig calls nothing else on it.
type erroringConversationDao struct {
	IConversationDao
}

func (erroringConversationDao) GetConversation(string) (Conversation, error) {
	return Conversation{}, assert.AnError
}

// ─────────────────────────────────────────────────────────────────────────────
// modelTierFromContext
// ─────────────────────────────────────────────────────────────────────────────

func TestModelTierFromContext_DefaultsToUntagged(t *testing.T) {
	assert.Equal(t, ModelTier(""), modelTierFromContext(nil), "nil context → untagged")

	ctx := newCtxWithKVs(ContextKeyCacheScope, CacheScopeGlobal)
	assert.Equal(t, ModelTier(""), modelTierFromContext(ctx), "no tier key → untagged")
}

func TestModelTierFromContext_ReadsTaggedTier(t *testing.T) {
	for _, tier := range everyTier {
		ctx := newCtxWithKVs(ContextKeyModelTier, tier)
		assert.Equal(t, tier, modelTierFromContext(ctx), "tier %s round-trips", tier)
	}
}

func TestModelTierFromContext_IgnoresWrongTypedValue(t *testing.T) {
	// A non-ModelTier value stored under the key (e.g. a bare string) is ignored.
	ctx := newCtxWithKVs(ContextKeyModelTier, "summary")
	assert.Equal(t, ModelTier(""), modelTierFromContext(ctx),
		"a wrong-typed tier value falls back to the untagged tier")
}

// ─────────────────────────────────────────────────────────────────────────────
// ResolveLLMConfig — every layer verified, each against every category it
// applies to. Each test configures its target layer plus a lower layer, so it
// also proves precedence at that boundary.
// ─────────────────────────────────────────────────────────────────────────────

// Layer 1 — env-global, untagged.
func TestResolveLLMConfig_EnvGlobalLayer(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-env-global")

	ctx := newCtxWithKVs(ContextKeyModelTier, ModelTier(""))
	res, err := ResolveLLMConfig(ctx, "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "env-global", res.Source)
	assert.Equal(t, "openai", res.Provider)
	assert.Equal(t, "gpt-env-global", res.Model)
}

// Layer 2 — db-global beats env-global (untagged).
func TestResolveLLMConfig_DBGlobalLayer(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-env-global")
	seedDBConfig(t, "acct-dbglobal", map[string]string{
		"llm_provider":   "anthropic",
		"llm_model_name": "claude-db-global",
	})

	ctx := newCtxWithKVs(ContextKeyModelTier, ModelTier(""))
	res, err := ResolveLLMConfig(ctx, "acct-dbglobal", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "db-global", res.Source, "db-global beats env-global")
	assert.Equal(t, "claude-db-global", res.Model)
}

// env-tier beats env-global within the ENV block, for each tier category. DB
// is intentionally NOT seeded here — see TestResolveLLMConfig_DBGlobalBeatsEnvTier
// for the cross-source precedence (DB always beats ENV).
func TestResolveLLMConfig_EnvTierLayer(t *testing.T) {
	for _, tier := range categoryTiers {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_tier_provider_"+string(tier), "anthropic")
			setEnvKey(t, "llm_tier_model_"+string(tier), "claude-env-tier")

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "", "", "")
			assert.NoError(t, err)
			assert.Equal(t, "env-tier", res.Source, "env-tier beats env-global within the ENV block")
			assert.Equal(t, "claude-env-tier", res.Model)
		})
	}
}

// Layer 4 — db-tier beats env-tier, for each tier category.
func TestResolveLLMConfig_DBTierLayer(t *testing.T) {
	for _, tier := range categoryTiers {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_tier_provider_"+string(tier), "googleai")
			setEnvKey(t, "llm_tier_model_"+string(tier), "gemini-env-tier")
			seedDBConfig(t, "acct-dbtier-"+string(tier), map[string]string{
				"llm_tier_provider_" + string(tier): "anthropic",
				"llm_tier_model_" + string(tier):    "claude-db-tier",
			})

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "acct-dbtier-"+string(tier), "", "")
			assert.NoError(t, err)
			assert.Equal(t, "db-tier", res.Source, "db-tier beats env-tier")
			assert.Equal(t, "claude-db-tier", res.Model)
		})
	}
}

// env-agent beats env-tier + env-global within the ENV block, for every
// category. DB is intentionally NOT seeded here — DB layers always win at the
// cross-source level (see TestResolveLLMConfig_DBGlobalBeatsEnvAgent).
func TestResolveLLMConfig_EnvAgentLayer(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_tier_provider_"+string(tier), "googleai")
			setEnvKey(t, "llm_tier_model_"+string(tier), "gemini-env-tier")
			setEnvKey(t, "llm_provider_agentx", "anthropic")
			setEnvKey(t, "llm_model_name_agentx", "claude-env-agent")

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "", "agentx", "")
			assert.NoError(t, err)
			assert.Equal(t, "env-agent", res.Source, "env-agent beats env-tier and env-global within the ENV block")
			assert.Equal(t, "claude-env-agent", res.Model)
		})
	}
}

// Layer 6 — db-agent beats env-agent, for every category.
func TestResolveLLMConfig_DBAgentLayer(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_provider_agentx", "openai")
			setEnvKey(t, "llm_model_name_agentx", "gpt-env-agent")
			seedDBConfig(t, "acct-dbagent-"+string(tier), map[string]string{
				"llm_provider_agentx":   "anthropic",
				"llm_model_name_agentx": "claude-db-agent",
			})

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "acct-dbagent-"+string(tier), "agentx", "")
			assert.NoError(t, err)
			assert.Equal(t, "db-agent", res.Source, "db-agent beats env-agent")
			assert.Equal(t, "claude-db-agent", res.Model)
		})
	}
}

// Layer 7 — conversation override beats the agent layer, for every category.
func TestResolveLLMConfig_ConversationLayer(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_provider_agentx", "anthropic")
			setEnvKey(t, "llm_model_name_agentx", "claude-env-agent")
			seedConversationOverride(t, "conv-"+string(tier), "googleai", "gemini-conv")

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "", "agentx", "conv-"+string(tier))
			assert.NoError(t, err)
			assert.Equal(t, "conversation", res.Source, "conversation beats the agent layer")
			assert.Equal(t, "gemini-conv", res.Model)
			assert.True(t, res.IsOverridden)
		})
	}
}

// Layer 8 — explicit context override beats the conversation layer, every category.
func TestResolveLLMConfig_ContextOverrideLayer(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			seedConversationOverride(t, "conv-ov-"+string(tier), "googleai", "gemini-conv")

			ctx := newCtxWithKVs(
				ContextKeyModelTier, tier,
				ContextKeyLlmProviderOverride, "anthropic",
				ContextKeyLlmModelOverride, "claude-override",
			)
			res, err := ResolveLLMConfig(ctx, "", "", "conv-ov-"+string(tier))
			assert.NoError(t, err)
			assert.Equal(t, "context-override", res.Source, "explicit override beats conversation")
			assert.Equal(t, "claude-override", res.Model)
			assert.True(t, res.IsOverridden)
		})
	}
}

// A Retrieval/Summary call with only a global layer keeps the global model —
// there is no implicit downgrade. The same expectation holds for Reasoning and
// untagged (normal-flow) calls.
func TestResolveLLMConfig_NoImplicitDowngrade(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-5-pro")
			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "", "", "")
			assert.NoError(t, err)
			assert.Equal(t, "gpt-5-pro", res.Model, "tier %s keeps the full global model", tier)
			assert.Equal(t, "env-global", res.Source)
		})
	}
}

// An explicit per-request override beats every other layer.
func TestResolveLLMConfig_ContextOverrideBeatsEnvGlobal(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")

	ctx := newCtxWithKVs(
		ContextKeyModelTier, ModelTierSummary,
		ContextKeyLlmProviderOverride, "anthropic",
		ContextKeyLlmModelOverride, "claude-override",
	)
	res, err := ResolveLLMConfig(ctx, "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "context-override", res.Source, "override beats env-global")
	assert.Equal(t, "claude-override", res.Model)
}

// Full stack — every one of the eight layers configured at once. The topmost
// configured layer wins, and all eight are recorded in the hierarchy.
func TestResolveLLMConfig_FullStackPrecedence(t *testing.T) {
	configureAll := func(t *testing.T) {
		pinGlobalModel(t, "p-envglobal", "m-envglobal")
		setEnvKey(t, "llm_tier_provider_summary", "p-envtier")
		setEnvKey(t, "llm_tier_model_summary", "m-envtier")
		setEnvKey(t, "llm_provider_agentx", "p-envagent")
		setEnvKey(t, "llm_model_name_agentx", "m-envagent")
		seedDBConfig(t, "acct-full", map[string]string{
			"llm_provider":              "p-dbglobal",
			"llm_model_name":            "m-dbglobal",
			"llm_tier_provider_summary": "p-dbtier",
			"llm_tier_model_summary":    "m-dbtier",
			"llm_provider_agentx":       "p-dbagent",
			"llm_model_name_agentx":     "m-dbagent",
		})
		seedConversationOverride(t, "conv-full", "p-conv", "m-conv")
	}

	t.Run("context-override wins over the full stack", func(t *testing.T) {
		configureAll(t)
		ctx := newCtxWithKVs(
			ContextKeyModelTier, ModelTierSummary,
			ContextKeyLlmProviderOverride, "p-override",
			ContextKeyLlmModelOverride, "m-override",
		)
		res, err := ResolveLLMConfig(ctx, "acct-full", "agentx", "conv-full")
		assert.NoError(t, err)
		assert.Equal(t, "context-override", res.Source)
		assert.Equal(t, "m-override", res.Model)
		assert.Len(t, res.Hierarchy, 8, "all eight layers recorded in the hierarchy")
	})

	t.Run("conversation wins when no override (beats db-agent)", func(t *testing.T) {
		configureAll(t)
		ctx := newCtxWithKVs(ContextKeyModelTier, ModelTierSummary)
		res, err := ResolveLLMConfig(ctx, "acct-full", "agentx", "conv-full")
		assert.NoError(t, err)
		assert.Equal(t, "conversation", res.Source, "conversation beats db-agent and every lower layer")
		assert.Equal(t, "m-conv", res.Model)
	})
}

// Incomplete tier config — provider XOR model, never both — does not fire the
// tier layer; resolution falls through to env-global and the call keeps the
// full global model (there is no implicit downgrade).
func TestResolveLLMConfig_IncompleteTierConfigFallsBackToGlobal(t *testing.T) {
	t.Run("model without provider", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-5-pro")
		setEnvKey(t, "llm_tier_model_summary", "gemini-2.5-flash")

		ctx := newCtxWithKVs(ContextKeyModelTier, ModelTierSummary)
		res, err := ResolveLLMConfig(ctx, "", "", "")
		assert.NoError(t, err)
		assert.Equal(t, "gpt-5-pro", res.Model, "model without provider → tier layer skipped")
		assert.Equal(t, "env-global", res.Source)
	})
	t.Run("provider without model", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-5-pro")
		setEnvKey(t, "llm_tier_provider_summary", "googleai")

		ctx := newCtxWithKVs(ContextKeyModelTier, ModelTierSummary)
		res, err := ResolveLLMConfig(ctx, "", "", "")
		assert.NoError(t, err)
		assert.Equal(t, "gpt-5-pro", res.Model, "provider without model → tier layer skipped")
		assert.Equal(t, "env-global", res.Source)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Edge cases — half-set overrides, skipped layers
// ─────────────────────────────────────────────────────────────────────────────

// An explicit override needs BOTH provider and model — a half-set override is
// ignored and resolution falls through to the configured hierarchy.
func TestResolveLLMConfig_HalfSetContextOverrideIgnored(t *testing.T) {
	t.Run("provider without model", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-5-pro")
		ctx := newCtxWithKVs(ContextKeyModelTier, ModelTier(""), ContextKeyLlmProviderOverride, "anthropic")
		res, err := ResolveLLMConfig(ctx, "", "", "")
		assert.NoError(t, err)
		assert.Equal(t, "env-global", res.Source, "half-set override ignored")
		assert.Equal(t, "openai", res.Provider)
		assert.Equal(t, "gpt-5-pro", res.Model)
	})
	t.Run("model without provider", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-5-pro")
		ctx := newCtxWithKVs(ContextKeyModelTier, ModelTier(""), ContextKeyLlmModelOverride, "claude-x")
		res, err := ResolveLLMConfig(ctx, "", "", "")
		assert.NoError(t, err)
		assert.Equal(t, "env-global", res.Source, "half-set override ignored")
		assert.Equal(t, "openai", res.Provider)
		assert.Equal(t, "gpt-5-pro", res.Model)
	})
}

// A conversation row with no model set (empty provider/model) must not become
// the active layer — resolution falls through to the agent layer.
func TestResolveLLMConfig_EmptyConversationOverrideSkipped(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_provider_agentx", "anthropic")
	setEnvKey(t, "llm_model_name_agentx", "claude-agent")
	seedConversationOverride(t, "conv-empty", "", "")

	res, err := ResolveLLMConfig(nil, "", "agentx", "conv-empty")
	assert.NoError(t, err)
	assert.Equal(t, "env-agent", res.Source, "empty conversation override is skipped")
	assert.Equal(t, "claude-agent", res.Model)
	assert.False(t, res.IsOverridden)
}

// A failed conversation lookup (DAO error) must be swallowed — the conversation
// layer is skipped and resolution continues, it does not fail the whole call.
func TestResolveLLMConfig_ConversationLookupErrorSkipsLayer(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_provider_agentx", "anthropic")
	setEnvKey(t, "llm_model_name_agentx", "claude-agent")

	prev := conversationDao
	conversationDao = erroringConversationDao{}
	t.Cleanup(func() { conversationDao = prev })

	// conv-missing is not in the override cache → GetConversationOverride hits
	// the DAO → error → ResolveLLMConfig must skip the conversation layer.
	res, err := ResolveLLMConfig(nil, "", "agentx", "conv-missing")
	assert.NoError(t, err, "a failed conversation lookup must not fail resolution")
	assert.Equal(t, "env-agent", res.Source, "conversation layer skipped on lookup error")
	assert.Equal(t, "claude-agent", res.Model)
}

// Partial layer config — a layer with provider XOR model set, never both — must
// be skipped entirely (it must not set a half-filled provider/model pair).
func TestResolveLLMConfig_PartialLayerConfigIsSkipped(t *testing.T) {
	t.Run("env-global provider without model → no resolution", func(t *testing.T) {
		pinGlobalModel(t, "openai", "")
		_, err := ResolveLLMConfig(nil, "", "", "")
		assert.Error(t, err, "a half-set env-global yields no resolution")
	})
	t.Run("db-global provider without model → env-global stands", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-env-global")
		seedDBConfig(t, "acct-pdbg", map[string]string{"llm_provider": "anthropic"})
		res, err := ResolveLLMConfig(nil, "acct-pdbg", "", "")
		assert.NoError(t, err)
		assert.Equal(t, "env-global", res.Source, "partial db-global skipped")
		assert.Equal(t, "openai", res.Provider, "partial layer did not leak its provider")
		assert.Equal(t, "gpt-env-global", res.Model)
	})
	t.Run("env-agent provider without model → env-global stands", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-env-global")
		setEnvKey(t, "llm_provider_agentx", "anthropic")
		res, err := ResolveLLMConfig(nil, "", "agentx", "")
		assert.NoError(t, err)
		assert.Equal(t, "env-global", res.Source, "partial env-agent skipped")
		assert.Equal(t, "openai", res.Provider)
	})
	t.Run("db-agent provider without model → env-agent stands", func(t *testing.T) {
		pinGlobalModel(t, "openai", "gpt-env-global")
		setEnvKey(t, "llm_provider_agentx", "anthropic")
		setEnvKey(t, "llm_model_name_agentx", "claude-agent")
		seedDBConfig(t, "acct-pdba", map[string]string{"llm_provider_agentx": "googleai"})
		res, err := ResolveLLMConfig(nil, "acct-pdba", "agentx", "")
		assert.NoError(t, err)
		assert.Equal(t, "env-agent", res.Source, "partial db-agent skipped")
		assert.Equal(t, "claude-agent", res.Model)
	})
}

// The Hierarchy records every resolved layer and marks exactly the winning one
// Active — this is what the UI renders.
func TestResolveLLMConfig_HierarchyMarksActiveLayer(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-env-global")
	seedDBConfig(t, "acct-hier", map[string]string{
		"llm_provider":   "anthropic",
		"llm_model_name": "claude-db-global",
	})

	res, err := ResolveLLMConfig(nil, "acct-hier", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "db-global", res.Source)
	assert.GreaterOrEqual(t, len(res.Hierarchy), 2, "lower layers stay recorded in the hierarchy")

	active := 0
	for _, layer := range res.Hierarchy {
		if layer.Active {
			active++
			assert.Equal(t, res.Source, layer.Level, "the Active layer matches Source")
		}
	}
	assert.Equal(t, 1, active, "exactly one hierarchy layer is Active")
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier-selection behaviour
// ─────────────────────────────────────────────────────────────────────────────

// An untagged call never consults the tier layers, even when tier config exists.
func TestResolveLLMConfig_UntaggedSkipsTierLayers(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	setEnvKey(t, "llm_tier_model_summary", "gemini-summary")
	setEnvKey(t, "llm_tier_provider_retrieval", "anthropic")
	setEnvKey(t, "llm_tier_model_retrieval", "claude-retrieval")

	ctx := newCtxWithKVs(ContextKeyModelTier, ModelTier(""))
	res, err := ResolveLLMConfig(ctx, "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "gpt-5-pro", res.Model, "an untagged call does not consult tier config")
	assert.Equal(t, "env-global", res.Source)
}

// Tier configs are isolated in both directions — a Retrieval call sees only
// Retrieval config, a Summary call sees only Summary config.
func TestResolveLLMConfig_TierIsolation(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_tier_provider_retrieval", "anthropic")
	setEnvKey(t, "llm_tier_model_retrieval", "claude-retrieval")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	setEnvKey(t, "llm_tier_model_summary", "gemini-summ")

	rt, err := ResolveLLMConfig(newCtxWithKVs(ContextKeyModelTier, ModelTierRetrieval), "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "claude-retrieval", rt.Model, "Retrieval sees only its own tier config")

	summ, err := ResolveLLMConfig(newCtxWithKVs(ContextKeyModelTier, ModelTierSummary), "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "gemini-summ", summ.Model, "Summary sees only its own tier config")
}

// The per-request resolution cache is keyed by tier — an untagged result must
// not be served to a Summary call for the same account/agent/conversation
// when the two tiers resolve to different layers/models.
func TestResolveLLMConfig_PerRequestCacheKeyedByTier(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	setEnvKey(t, "llm_tier_model_summary", "gemini-summary")
	cache := NewLLMResolutionCache()

	untaggedCtx := newCtxWithKVs(ContextKeyLLMResolution, cache)
	r1, err := ResolveLLMConfig(untaggedCtx, "", "", "")
	assert.NoError(t, err)
	r2, err := ResolveLLMConfig(untaggedCtx, "", "", "")
	assert.NoError(t, err)
	assert.Same(t, r1, r2, "same tier → cache hit returns the same resolution")

	summCtx := newCtxWithKVs(ContextKeyLLMResolution, cache, ContextKeyModelTier, ModelTierSummary)
	rs, err := ResolveLLMConfig(summCtx, "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "gpt-5-pro", r1.Model, "untagged entry resolves through env-global")
	assert.Equal(t, "gemini-summary", rs.Model, "Summary resolves independently — cache key includes the tier")
}

// No resolvable configuration anywhere → error.
func TestResolveLLMConfig_ErrorsWhenNoConfig(t *testing.T) {
	pinGlobalModel(t, "", "")

	_, err := ResolveLLMConfig(nil, "", "", "")
	assert.Error(t, err, "no resolvable configuration → error")
}

// ─────────────────────────────────────────────────────────────────────────────
// Cross-source precedence — DB always beats ENV at any specificity.
//
// These tests pin the 2026-05 behavioural shift (see CLAUDE.md → Architecture
// Decisions). Before the change, a stale ENV-agent override would silently win
// over a tenant-canonical DB-global value, fragmenting Google AI CachedContent
// ownership across calls (= 403 PERMISSION_DENIED storms). DB authority is
// now total: DB-global beats env-tier and env-agent; DB-tier beats env-agent.
// ─────────────────────────────────────────────────────────────────────────────

// DB-global beats env-tier even though env-tier is more "specific" than global.
// Cross-source precedence wins over within-source specificity.
func TestResolveLLMConfig_DBGlobalBeatsEnvTier(t *testing.T) {
	for _, tier := range categoryTiers {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_tier_provider_"+string(tier), "anthropic")
			setEnvKey(t, "llm_tier_model_"+string(tier), "claude-env-tier")
			seedDBConfig(t, "acct-dbg-envtier-"+string(tier), map[string]string{
				"llm_provider":   "googleai",
				"llm_model_name": "gemini-db-global",
			})

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "acct-dbg-envtier-"+string(tier), "", "")
			assert.NoError(t, err)
			assert.Equal(t, "db-global", res.Source, "DB block sits above ENV block; db-global beats env-tier")
			assert.Equal(t, "gemini-db-global", res.Model)
		})
	}
}

// DB-global beats env-agent — the regression that produced the original 403
// storm. A tenant-set DB-global API key must not be silently overridden by an
// operator's stale agent-specific ENV.
func TestResolveLLMConfig_DBGlobalBeatsEnvAgent(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_provider_agentx", "anthropic")
			setEnvKey(t, "llm_model_name_agentx", "claude-env-agent")
			seedDBConfig(t, "acct-dbg-envagent-"+string(tier), map[string]string{
				"llm_provider":   "googleai",
				"llm_model_name": "gemini-db-global",
			})

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "acct-dbg-envagent-"+string(tier), "agentx", "")
			assert.NoError(t, err)
			assert.Equal(t, "db-global", res.Source, "db-global beats env-agent (DB > ENV at any specificity)")
			assert.Equal(t, "gemini-db-global", res.Model)
		})
	}
}

// DB-tier beats env-agent — even at a higher specificity, ENV cannot override
// any DB layer.
func TestResolveLLMConfig_DBTierBeatsEnvAgent(t *testing.T) {
	for _, tier := range categoryTiers {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_provider_agentx", "anthropic")
			setEnvKey(t, "llm_model_name_agentx", "claude-env-agent")
			seedDBConfig(t, "acct-dbt-envagent-"+string(tier), map[string]string{
				"llm_tier_provider_" + string(tier): "googleai",
				"llm_tier_model_" + string(tier):    "gemini-db-tier",
			})

			ctx := newCtxWithKVs(ContextKeyModelTier, tier)
			res, err := ResolveLLMConfig(ctx, "acct-dbt-envagent-"+string(tier), "agentx", "")
			assert.NoError(t, err)
			assert.Equal(t, "db-tier", res.Source, "db-tier beats env-agent (DB > ENV at any specificity)")
			assert.Equal(t, "gemini-db-tier", res.Model)
		})
	}
}

// getLLMApiKey: DB-global beats env-agent. This is the precise failure path
// that fragmented Google AI cache ownership: cache slot created under DB-global
// key, sibling agent call presented env-agent key → 403 PERMISSION_DENIED.
func TestGetLLMApiKey_DBGlobalBeatsEnvAgent(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "key-env-global"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	setEnvKey(t, "llm_provider_agentx", "googleai")
	setEnvKey(t, "llm_provider_api_key_agentx", "key-env-agent")

	seedDBConfig(t, "acct-key", map[string]string{
		"llm_provider":         "googleai",
		"llm_provider_api_key": "key-db-global",
	})

	res, err := ResolveLLMConfig(nil, "acct-key", "agentx", "")
	assert.NoError(t, err)
	key := getLLMApiKey("acct-key", "googleai", "agentx", true, res)
	assert.Equal(t, "key-db-global", key, "DB-global API key wins over ENV-agent override")
}

// getLLMApiKey: DB-agent beats env-agent (specific-DB beats specific-ENV).
func TestGetLLMApiKey_DBAgentBeatsEnvAgent(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	setEnvKey(t, "llm_provider_agentx", "googleai")
	setEnvKey(t, "llm_provider_api_key_agentx", "key-env-agent")
	seedDBConfig(t, "acct-key-dba", map[string]string{
		"llm_provider":                "googleai",
		"llm_provider_agentx":         "googleai",
		"llm_provider_api_key_agentx": "key-db-agent",
	})

	res, err := ResolveLLMConfig(nil, "acct-key-dba", "agentx", "")
	assert.NoError(t, err)
	key := getLLMApiKey("acct-key-dba", "googleai", "agentx", true, res)
	assert.Equal(t, "key-db-agent", key, "DB-agent API key wins over ENV-agent")
}

// getLLMFallbackModelName: db-global beats env-agent. Regression test for the
// same precedence inversion at the fallback-chain resolver.
func TestGetLLMFallbackModelName_DBGlobalBeatsEnvAgent(t *testing.T) {
	pinGlobalFallback(t, "env-global-fb")
	setEnvKey(t, "llm_model_fallbacks_agentx", "env-agent-fb")
	seedDBConfig(t, "acct-fb-dbg-envagent", map[string]string{
		"llm_model_fallbacks": "db-global-fb",
	})

	got := getLLMFallbackModelName("acct-fb-dbg-envagent", "agentx", ModelTier(""), true)
	assert.Equal(t, "db-global-fb", got, "DB-global fallback beats ENV-agent fallback")
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier-credential resolution (ENV-tier / DB-tier layers in getLLMApi*)
// ─────────────────────────────────────────────────────────────────────────────

// resolutionWithTier returns an LLMConfigResolution stub with the given tier set.
// Bypasses ResolveLLMConfig so the test can target getLLMApi* in isolation.
func resolutionWithTier(tier ModelTier, dbConfig map[string]string) *LLMConfigResolution {
	return &LLMConfigResolution{Tier: tier, dbConfig: dbConfig}
}

// getLLMApiKey: ENV-tier credential fires when tier ENV provider matches the
// resolved provider.
func TestGetLLMApiKey_TierEnvHit(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-env-tier-retrieval")

	res := resolutionWithTier(ModelTierRetrieval, nil)
	key := getLLMApiKey("", "googleai", "", false, res)
	assert.Equal(t, "key-env-tier-retrieval", key,
		"ENV-tier API key picked up when tier provider matches resolved provider")
}

// getLLMApiKey: ENV-tier credential is NOT used when tier ENV provider differs
// from the resolved provider — prevents a Bedrock-tier key from being handed
// to an Anthropic SDK call.
func TestGetLLMApiKey_TierProviderMismatchSkipped(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-2.5-flash")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "key-env-global"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	// Tier slot points at a different provider than the call's provider arg.
	setEnvKey(t, "llm_tier_provider_retrieval", "anthropic")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-tier-anthropic")

	res := resolutionWithTier(ModelTierRetrieval, nil)
	key := getLLMApiKey("", "googleai", "", false, res)
	assert.Equal(t, "key-env-global", key,
		"tier API key must not leak across providers — falls back to ENV-global")
}

// getLLMApiKey: DB-tier credential fires when tier DB provider matches.
func TestGetLLMApiKey_TierDBHit(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	dbConfig := map[string]string{
		"llm_provider":                "anthropic",
		"llm_provider_api_key":        "key-db-global-anthropic",
		"llm_tier_provider_retrieval": "googleai",
		"llm_tier_api_key_retrieval":  "key-db-tier-googleai",
	}
	seedDBConfig(t, "acct-tier-db", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMApiKey("acct-tier-db", "googleai", "", false, res)
	assert.Equal(t, "key-db-tier-googleai", key,
		"DB-tier API key picked up when tier DB provider matches resolved provider")
}

// getLLMApiKey: DB-tier beats ENV-tier (DB always beats ENV at the same scope).
func TestGetLLMApiKey_DBTierBeatsEnvTier(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-env-tier")
	dbConfig := map[string]string{
		"llm_tier_provider_retrieval": "googleai",
		"llm_tier_api_key_retrieval":  "key-db-tier",
	}
	seedDBConfig(t, "acct-db-beats-env-tier", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMApiKey("acct-db-beats-env-tier", "googleai", "", false, res)
	assert.Equal(t, "key-db-tier", key,
		"DB-tier API key beats ENV-tier API key")
}

// getLLMApiKey: DB-agent beats DB-tier (more-specific layer wins, matching the
// provider+model precedence rule).
func TestGetLLMApiKey_DBAgentBeatsDBTier(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	dbConfig := map[string]string{
		"llm_tier_provider_retrieval": "googleai",
		"llm_tier_api_key_retrieval":  "key-db-tier",
		"llm_provider_agentx":         "googleai",
		"llm_provider_api_key_agentx": "key-db-agent",
	}
	seedDBConfig(t, "acct-agent-beats-tier", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMApiKey("acct-agent-beats-tier", "googleai", "agentx", true, res)
	assert.Equal(t, "key-db-agent", key,
		"DB-agent API key beats DB-tier API key")
}

// getLLMApiKey: an untagged call (empty tier) must never read tier slots even
// if they're set — guards against the empty-tier code path accidentally
// matching against the provider arg.
func TestGetLLMApiKey_UntaggedSkipsTierLayers(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "key-env-global"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-tier-should-not-fire")

	res := resolutionWithTier(ModelTier(""), nil)
	key := getLLMApiKey("", "googleai", "", false, res)
	assert.Equal(t, "key-env-global", key,
		"untagged call must skip tier layers")
}

// getLLMRegion: smoke test for the same tier path on a non-API-key resolver.
func TestGetLLMRegion_TierEnvHit(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	setEnvKey(t, "llm_tier_provider_retrieval", "bedrock")
	setEnvKey(t, "llm_tier_region_retrieval", "us-west-2")

	res := resolutionWithTier(ModelTierRetrieval, nil)
	region := getLLMRegion("", "bedrock", "", false, res)
	assert.Equal(t, "us-west-2", region,
		"ENV-tier region picked up when tier provider matches resolved provider")
}

// getLLMApiKey: ENV-tier credential beats ENV-global. Mirrors the
// provider+model precedence rule at the cred level.
func TestGetLLMApiKey_EnvTierBeatsEnvGlobal(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "key-env-global"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	// Tier provider matches global so the env-tier branch fires and overrides.
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-env-tier")

	res := resolutionWithTier(ModelTierRetrieval, nil)
	key := getLLMApiKey("", "googleai", "", false, res)
	assert.Equal(t, "key-env-tier", key, "ENV-tier API key beats ENV-global when tier provider matches")
}

// getLLMApiKey: DB-global beats ENV-tier. Cross-block precedence — the DB block
// always sits above the ENV block at any specificity.
func TestGetLLMApiKey_DBGlobalBeatsEnvTier(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-env-tier")
	dbConfig := map[string]string{
		"llm_provider":         "googleai",
		"llm_provider_api_key": "key-db-global",
	}
	seedDBConfig(t, "acct-db-global-beats-env-tier", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMApiKey("acct-db-global-beats-env-tier", "googleai", "", false, res)
	assert.Equal(t, "key-db-global", key, "DB-global API key beats ENV-tier API key (cross-block precedence)")
}

// getLLMApiKey: DB-tier beats ENV-agent. The DB block sits above any ENV layer,
// and DB-tier is specifically between DB-global and DB-agent.
func TestGetLLMApiKey_DBTierBeatsEnvAgent(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	setEnvKey(t, "llm_provider_agentx", "googleai")
	setEnvKey(t, "llm_provider_api_key_agentx", "key-env-agent")
	dbConfig := map[string]string{
		"llm_tier_provider_retrieval": "googleai",
		"llm_tier_api_key_retrieval":  "key-db-tier",
	}
	seedDBConfig(t, "acct-db-tier-beats-env-agent", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMApiKey("acct-db-tier-beats-env-agent", "googleai", "agentx", true, res)
	assert.Equal(t, "key-db-tier", key, "DB-tier API key beats ENV-agent API key")
}

// getLLMApiKey: across-tier isolation — a retrieval-tier API key must not
// leak into a summary-tier call when both tier slots are configured.
func TestGetLLMApiKey_TierIsolation(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-x")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_api_key_retrieval", "key-retrieval")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	setEnvKey(t, "llm_tier_api_key_summary", "key-summary")

	// Same call args, different tier — must pick the matching tier's key.
	resR := resolutionWithTier(ModelTierRetrieval, nil)
	resS := resolutionWithTier(ModelTierSummary, nil)
	assert.Equal(t, "key-retrieval", getLLMApiKey("", "googleai", "", false, resR), "Retrieval tier picks retrieval key")
	assert.Equal(t, "key-summary", getLLMApiKey("", "googleai", "", false, resS), "Summary tier picks summary key")
}

// getLLMAccessKey: smoke test that resolveLLMSecret's new tierKeyFormat
// parameter wires through the AWS-credential resolvers correctly.
func TestGetLLMAccessKey_TierDBHit(t *testing.T) {
	pinGlobalModel(t, "anthropic", "claude-opus-4-7")
	dbConfig := map[string]string{
		"llm_tier_provider_retrieval":   "bedrock",
		"llm_tier_access_key_retrieval": "AKIA-TIER-DB",
	}
	seedDBConfig(t, "acct-tier-access", dbConfig)

	res := resolutionWithTier(ModelTierRetrieval, dbConfig)
	key := getLLMAccessKey("acct-tier-access", "bedrock", "", false, res)
	assert.Equal(t, "AKIA-TIER-DB", key,
		"DB-tier AWS access key picked up via resolveLLMSecret tier path")
}

// ─────────────────────────────────────────────────────────────────────────────
// Cache-key creds fingerprint (llmClientCache + generateCacheKey)
// ─────────────────────────────────────────────────────────────────────────────

// credsFingerprint isolates two cred sets that differ in any byte.
func TestCredsFingerprint_DistinctInputsDistinctHashes(t *testing.T) {
	a := credsFingerprint("key-A", "", "", "", "", "", "")
	b := credsFingerprint("key-B", "", "", "", "", "", "")
	c := credsFingerprint("key-A", "https://endpoint", "", "", "", "", "")
	assert.NotEqual(t, a, b, "different api keys → different hashes")
	assert.NotEqual(t, a, c, "endpoint difference alone changes the hash")

	// Same inputs → same hash (deterministic).
	a2 := credsFingerprint("key-A", "", "", "", "", "", "")
	assert.Equal(t, a, a2, "deterministic for identical input")

	// Length: 4 bytes truncated, hex-encoded → 8 chars.
	assert.Len(t, a, 8, "fingerprint is 8 hex chars")
}

// resolveCredsFingerprint pulls live values from the resolvers — same (provider,
// model, account) with different per-tier api keys produces different
// fingerprints, which in turn produces different llmClientCache keys.
func TestResolveCredsFingerprint_TierOverrideShiftsBucket(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-2.5-flash")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "GLOBAL-KEY"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	// No tier override → fingerprint derived from global key.
	fpGlobal := resolveCredsFingerprint("", "googleai", "", false, resolutionWithTier(ModelTier(""), nil))

	// Reasoning-tier override resolves to a different api key.
	setEnvKey(t, "llm_tier_provider_reasoning", "googleai")
	setEnvKey(t, "llm_tier_api_key_reasoning", "REASONING-KEY")
	fpReasoning := resolveCredsFingerprint("", "googleai", "", false, resolutionWithTier(ModelTierReasoning, nil))

	assert.NotEqual(t, fpGlobal, fpReasoning,
		"different resolved api key → different cache fingerprint → distinct llmClientCache bucket")
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversation per-tier override + context-tier override (Phase 3 + 4)
// ─────────────────────────────────────────────────────────────────────────────

// seedConversationOverrideTier pre-populates the override cache with a
// per-tier picks block for the given conversation, so resolver tests don't
// have to touch the DB.
func seedConversationOverrideTier(t *testing.T, conversationId string, picks map[string]TierModelPick) {
	t.Helper()
	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = conversationOverrideEntry{
		tierOverrides: ConversationTierOverrides{Picks: picks},
		ts:            time.Now(),
	}
	conversationOverrideCacheMutex.Unlock()
	t.Cleanup(func() {
		conversationOverrideCacheMutex.Lock()
		delete(conversationOverrideCache, conversationId)
		conversationOverrideCacheMutex.Unlock()
	})
}

// Round-trip Value/Scan: an in-memory ConversationTierOverrides marshals to
// bytes via Value() and unmarshals back via Scan() to the same Picks map.
func TestConversationTierOverrides_ValueScanRoundTrip(t *testing.T) {
	orig := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
		"retrieval": {Provider: "googleai", Model: "gemini-2.5-flash"},
	}}
	v, err := orig.Value()
	assert.NoError(t, err)
	raw, ok := v.([]byte)
	assert.True(t, ok, "Value should marshal to []byte when populated")

	var got ConversationTierOverrides
	assert.NoError(t, got.Scan(raw))
	assert.Equal(t, orig.Picks, got.Picks, "round-trip preserves the picks map")

	// Empty struct must Value() to nil (writes SQL NULL).
	emptyV, err := ConversationTierOverrides{}.Value()
	assert.NoError(t, err)
	assert.Nil(t, emptyV, "empty Picks marshals to SQL NULL")

	// Scan of nil clears Picks.
	got.Picks = map[string]TierModelPick{"x": {Provider: "p", Model: "m"}}
	assert.NoError(t, got.Scan(nil))
	assert.Nil(t, got.Picks, "Scan(nil) clears Picks")
}

// Resolver: conversation-tier override wins for a tier-tagged call; an
// untagged call in the same conversation falls through (does NOT silently
// substitute a per-tier model). This matches the explicit UX choice
// surfaced to the user as a Per-category warning in the chat composer.
func TestResolveLLMConfig_ConversationTierOverride(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-2.5-flash")
	convId := "conv-tier-override"
	seedConversationOverrideTier(t, convId, map[string]TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
	})

	// Tagged call (Reasoning) → conversation-tier override wins.
	ctx := newCtxWithKVs(ContextKeyModelTier, ModelTierReasoning)
	res, err := ResolveLLMConfig(ctx, "", "", convId)
	assert.NoError(t, err)
	assert.Equal(t, "anthropic", res.Provider)
	assert.Equal(t, "claude-opus-4-7", res.Model)
	assert.Equal(t, "conversation-tier", res.Source)

	// Untagged call (no tier) → conversation-tier layer skipped → falls
	// through to the global default (gemini).
	untaggedCtx := newCtxWithKVs()
	res2, err := ResolveLLMConfig(untaggedCtx, "", "", convId)
	assert.NoError(t, err)
	assert.Equal(t, "googleai", res2.Provider)
	assert.Equal(t, "gemini-2.5-flash", res2.Model)
	assert.NotEqual(t, "conversation-tier", res2.Source)
}

// Resolver: context-tier override is the highest precedence — beats the
// conversation-tier override for the same tier.
func TestResolveLLMConfig_ContextTierOverrideBeatsConversationTier(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-2.5-flash")
	convId := "conv-ctx-vs-conv-tier"
	seedConversationOverrideTier(t, convId, map[string]TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
	})

	ctxOverride := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "bedrock", Model: "meta.llama3-1-70b-instruct-v1:0"},
	}}
	ctx := newCtxWithKVs(
		ContextKeyModelTier, ModelTierReasoning,
		ContextKeyLlmTierModelOverrides, ctxOverride,
	)
	res, err := ResolveLLMConfig(ctx, "", "", convId)
	assert.NoError(t, err)
	assert.Equal(t, "bedrock", res.Provider, "context-tier override wins over conversation-tier")
	assert.Equal(t, "meta.llama3-1-70b-instruct-v1:0", res.Model)
	assert.Equal(t, "context-override-tier", res.Source)
}

// ConversationTierOverrides.Get: returns false for a missing tier or for a
// half-set pick (provider or model empty). Resolver depends on this to fall
// through rather than producing an invalid (empty-provider, model) result.
func TestConversationTierOverrides_GetSkipsHalfSet(t *testing.T) {
	c := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
		"halfset_p": {Provider: "", Model: "model-x"},
		"halfset_m": {Provider: "googleai", Model: ""},
	}}
	if _, ok := c.Get("reasoning"); !ok {
		t.Fatalf("expected reasoning to be returned")
	}
	if _, ok := c.Get("halfset_p"); ok {
		t.Fatalf("half-set (missing provider) must not be returned")
	}
	if _, ok := c.Get("halfset_m"); ok {
		t.Fatalf("half-set (missing model) must not be returned")
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatalf("missing tier key must not be returned")
	}
}
