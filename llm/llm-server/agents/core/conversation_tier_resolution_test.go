package core

import (
	"context"
	"log/slog"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var everyTier = []llmruntime.ModelTier{llmruntime.ModelTier(""), llmruntime.ModelTierReasoning, llmruntime.ModelTierRetrieval, llmruntime.ModelTierSummary}

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
	t.Cleanup(llmruntime.SeedIntegrationConfigForTest(accountId, cfg))
}

// newCtxWithKVs builds a RequestContext carrying the given key/value pairs.
func newCtxWithKVs(kv ...any) *security.RequestContext {
	parent := context.Background()
	for i := 0; i+1 < len(kv); i += 2 {
		parent = context.WithValue(parent, kv[i], kv[i+1])
	}
	return security.NewRequestContext(parent, security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// catTestAgent is a minimal NBAgent that declares no category.
type catTestAgent struct{}

func (catTestAgent) GetName() string { return "cat-test" }

func (catTestAgent) GetNameAliases() []string { return nil }

func (catTestAgent) GetDescription() string { return "" }

func (catTestAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool { return nil }

func (catTestAgent) GetSystemPrompt(*security.RequestContext, NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}

func (catTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }

// catTestCategorisedAgent additionally implements NBAgentCategoryProvider.
type catTestCategorisedAgent struct {
	catTestAgent
	category llmruntime.ModelTier
}

func (a catTestCategorisedAgent) GetModelCategory() llmruntime.ModelTier { return a.category }

// catTestClassificationAgent is a category-less agent whose planner type is
// Classification — the single-shot-classifier default path.
type catTestClassificationAgent struct{ catTestAgent }

func (catTestClassificationAgent) GetPlannerType() AgentPlannerType {
	return AgentPlannerTypeClassification
}

// catTestClassificationCategorisedAgent is a classification agent that DOES
// declare a category, to prove the explicit override still wins.
type catTestClassificationCategorisedAgent struct {
	catTestClassificationAgent
	category llmruntime.ModelTier
}

func (a catTestClassificationCategorisedAgent) GetModelCategory() llmruntime.ModelTier {
	return a.category
}

func TestAgentModelCategory(t *testing.T) {
	// A nil agent → Retrieval default (no panic).
	assert.Equal(t, llmruntime.ModelTierRetrieval, agentModelCategory(nil),
		"nil agent → Retrieval default (no panic)")

	// An agent that does not implement NBAgentCategoryProvider → Retrieval default.
	assert.Equal(t, llmruntime.ModelTierRetrieval, agentModelCategory(catTestAgent{}),
		"agent without the optional interface → Retrieval (fleet default)")

	// A category-less classification agent → Summary default (single-shot
	// analysis/extraction, not the cheap Retrieval floor).
	assert.Equal(t, llmruntime.ModelTierSummary, agentModelCategory(catTestClassificationAgent{}),
		"category-less classification agent → Summary default")

	// An explicit category still wins even for a classification agent.
	assert.Equal(t, llmruntime.ModelTierRetrieval,
		agentModelCategory(catTestClassificationCategorisedAgent{category: llmruntime.ModelTierRetrieval}),
		"declared category overrides the classification Summary default")

	// An agent that declares a category → that category.
	for _, tier := range []llmruntime.ModelTier{llmruntime.ModelTierReasoning, llmruntime.ModelTierRetrieval, llmruntime.ModelTierSummary} {
		assert.Equal(t, tier, agentModelCategory(catTestCategorisedAgent{category: tier}),
			"declared category %s is returned", tier)
	}

	// An agent may still declare the empty tier to opt into the global default
	// (the escape hatch) instead of the Retrieval floor.
	assert.Equal(t, llmruntime.ModelTier(""), agentModelCategory(catTestCategorisedAgent{category: ""}))
}

func TestIsSingleShotClassifier(t *testing.T) {
	assert.True(t, isSingleShotClassifier(catTestClassificationAgent{}),
		"classification planner type → single-shot classifier")
	assert.False(t, isSingleShotClassifier(catTestAgent{}),
		"react planner type → not a single-shot classifier")
	assert.False(t, isSingleShotClassifier(nil),
		"nil agent → not a single-shot classifier (no panic)")
}

// applyAgentModelTier must override an inherited tier for a category-less agent
// — stamping the Retrieval default — so a tool sub-agent invoked under a
// Reasoning-tier parent (its context already carries ModelTierReasoning) does
// not silently run on the pro model. A categorised agent stamps its own tier.
func TestApplyAgentModelTier_ResetsInheritedTier(t *testing.T) {
	// Simulate a sub-agent invoked with the parent investigation's context,
	// which already carries the Reasoning tier.
	parentCtx := security.NewRequestContext(
		context.WithValue(context.Background(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierReasoning),
		nil, slog.Default(), nil, nil,
	)
	assert.Equal(t, llmruntime.ModelTierReasoning, llmruntime.ModelTierFromContext(parentCtx),
		"precondition: parent context carries the Reasoning tier")

	// A category-less tool agent must NOT inherit the parent's Reasoning tier;
	// it is stamped with the Retrieval default instead.
	resetCtx := applyAgentModelTier(parentCtx, catTestAgent{}, NBAgentRequest{})
	assert.Equal(t, llmruntime.ModelTierRetrieval, llmruntime.ModelTierFromContext(resetCtx),
		"category-less agent overrides the inherited tier → Retrieval default")

	// A categorised agent stamps its own tier even over an inherited one.
	for _, tier := range []llmruntime.ModelTier{llmruntime.ModelTierReasoning, llmruntime.ModelTierRetrieval, llmruntime.ModelTierSummary} {
		got := applyAgentModelTier(parentCtx, catTestCategorisedAgent{category: tier}, NBAgentRequest{})
		assert.Equal(t, tier, llmruntime.ModelTierFromContext(got),
			"declared category %s must be stamped onto the context", tier)
	}

	// Defensive guards: must not panic on a nil ctx or a zero-value
	// RequestContext whose internal context.Context is nil (e.g. planner stubs).
	assert.NotPanics(t, func() {
		assert.Nil(t, applyAgentModelTier(nil, catTestAgent{}, NBAgentRequest{}))
		zero := applyAgentModelTier(&security.RequestContext{}, catTestCategorisedAgent{category: llmruntime.ModelTierReasoning}, NBAgentRequest{})
		assert.Equal(t, llmruntime.ModelTierReasoning, llmruntime.ModelTierFromContext(zero),
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
	parentCtx := newCtxWithKVs(llmruntime.ContextKeyModelTier, llmruntime.ModelTierReasoning)

	// Baseline (the bug): inheriting the parent's Reasoning tier resolves pro.
	resInherited, err := llmruntime.ResolveLLMConfig(parentCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro-preview", resInherited.Model,
		"baseline: a category-less agent that inherits the parent Reasoning tier resolves the pro model")

	// The fix: a category-less agent is stamped the Retrieval default → resolves
	// the retrieval-tier model, not the inherited pro model.
	fixedCtx := applyAgentModelTier(parentCtx, catTestAgent{}, NBAgentRequest{})
	resFixed, err := llmruntime.ResolveLLMConfig(fixedCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "qwen-retrieval", resFixed.Model,
		"fix: a category-less sub-agent resolves the Retrieval-tier model, not the inherited pro model")
	assert.NotEqual(t, "gemini-3.1-pro-preview", resFixed.Model)

	// A genuinely reasoning-tier agent still opts into pro — the fix does not
	// downgrade agents that declare the category.
	catCtx := applyAgentModelTier(parentCtx, catTestCategorisedAgent{category: llmruntime.ModelTierReasoning}, NBAgentRequest{})
	resCat, err := llmruntime.ResolveLLMConfig(catCtx, "", "kubectl", "")
	assert.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro-preview", resCat.Model,
		"a reasoning-category agent still resolves the pro model")
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

// erroringConversationDao is an IConversationDao whose conversation lookup
// always fails — used to exercise the conversation-layer error path. Only
// GetConversation is implemented; ResolveLLMConfig calls nothing else on it.
type erroringConversationDao struct {
	IConversationDao
}

func (erroringConversationDao) GetConversation(string) (Conversation, error) {
	return Conversation{}, assert.AnError
}

// Layer 7 — conversation override beats the agent layer, for every category.
func TestResolveLLMConfig_ConversationLayer(t *testing.T) {
	for _, tier := range everyTier {
		t.Run(string(tier), func(t *testing.T) {
			pinGlobalModel(t, "openai", "gpt-env-global")
			setEnvKey(t, "llm_provider_agentx", "anthropic")
			setEnvKey(t, "llm_model_name_agentx", "claude-env-agent")
			seedConversationOverride(t, "conv-"+string(tier), "googleai", "gemini-conv")

			ctx := newCtxWithKVs(llmruntime.ContextKeyModelTier, tier)
			res, err := llmruntime.ResolveLLMConfig(ctx, "", "agentx", "conv-"+string(tier))
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
				llmruntime.ContextKeyModelTier, tier,
				llmruntime.ContextKeyLlmProviderOverride, "anthropic",
				llmruntime.ContextKeyLlmModelOverride, "claude-override",
			)
			res, err := llmruntime.ResolveLLMConfig(ctx, "", "", "conv-ov-"+string(tier))
			assert.NoError(t, err)
			assert.Equal(t, "context-override", res.Source, "explicit override beats conversation")
			assert.Equal(t, "claude-override", res.Model)
			assert.True(t, res.IsOverridden)
		})
	}
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
			llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary,
			llmruntime.ContextKeyLlmProviderOverride, "p-override",
			llmruntime.ContextKeyLlmModelOverride, "m-override",
		)
		res, err := llmruntime.ResolveLLMConfig(ctx, "acct-full", "agentx", "conv-full")
		assert.NoError(t, err)
		assert.Equal(t, "context-override", res.Source)
		assert.Equal(t, "m-override", res.Model)
		assert.Len(t, res.Hierarchy, 8, "all eight layers recorded in the hierarchy")
	})

	t.Run("conversation wins when no override (beats db-agent)", func(t *testing.T) {
		configureAll(t)
		ctx := newCtxWithKVs(llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary)
		res, err := llmruntime.ResolveLLMConfig(ctx, "acct-full", "agentx", "conv-full")
		assert.NoError(t, err)
		assert.Equal(t, "conversation", res.Source, "conversation beats db-agent and every lower layer")
		assert.Equal(t, "m-conv", res.Model)
	})
}

// A conversation row with no model set (empty provider/model) must not become
// the active layer — resolution falls through to the agent layer.
func TestResolveLLMConfig_EmptyConversationOverrideSkipped(t *testing.T) {
	pinGlobalModel(t, "openai", "gpt-5-pro")
	setEnvKey(t, "llm_provider_agentx", "anthropic")
	setEnvKey(t, "llm_model_name_agentx", "claude-agent")
	seedConversationOverride(t, "conv-empty", "", "")

	res, err := llmruntime.ResolveLLMConfig(nil, "", "agentx", "conv-empty")
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

	prev := PeekConversationDao()
	SetConversationDao(erroringConversationDao{})
	t.Cleanup(func() { SetConversationDao(prev) })

	// conv-missing is not in the override cache → GetConversationOverride hits
	// the DAO → error → ResolveLLMConfig must skip the conversation layer.
	res, err := llmruntime.ResolveLLMConfig(nil, "", "agentx", "conv-missing")
	assert.NoError(t, err, "a failed conversation lookup must not fail resolution")
	assert.Equal(t, "env-agent", res.Source, "conversation layer skipped on lookup error")
	assert.Equal(t, "claude-agent", res.Model)
}

// seedConversationOverrideTier pre-populates the override cache with a
// per-tier picks block for the given conversation, so resolver tests don't
// have to touch the DB.
func seedConversationOverrideTier(t *testing.T, conversationId string, picks map[string]llmruntime.TierModelPick) {
	t.Helper()
	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = conversationOverrideEntry{
		tierOverrides: llmruntime.ConversationTierOverrides{Picks: picks},
		ts:            time.Now(),
	}
	conversationOverrideCacheMutex.Unlock()
	t.Cleanup(func() {
		conversationOverrideCacheMutex.Lock()
		delete(conversationOverrideCache, conversationId)
		conversationOverrideCacheMutex.Unlock()
	})
}

// Resolver: conversation-tier override wins for a tier-tagged call; an
// untagged call in the same conversation falls through (does NOT silently
// substitute a per-tier model). This matches the explicit UX choice
// surfaced to the user as a Per-category warning in the chat composer.
func TestResolveLLMConfig_ConversationTierOverride(t *testing.T) {
	pinGlobalModel(t, "googleai", "gemini-2.5-flash")
	convId := "conv-tier-override"
	seedConversationOverrideTier(t, convId, map[string]llmruntime.TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
	})

	// Tagged call (Reasoning) → conversation-tier override wins.
	ctx := newCtxWithKVs(llmruntime.ContextKeyModelTier, llmruntime.ModelTierReasoning)
	res, err := llmruntime.ResolveLLMConfig(ctx, "", "", convId)
	assert.NoError(t, err)
	assert.Equal(t, "anthropic", res.Provider)
	assert.Equal(t, "claude-opus-4-7", res.Model)
	assert.Equal(t, "conversation-tier", res.Source)

	// Untagged call (no tier) → conversation-tier layer skipped → falls
	// through to the global default (gemini).
	untaggedCtx := newCtxWithKVs()
	res2, err := llmruntime.ResolveLLMConfig(untaggedCtx, "", "", convId)
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
	seedConversationOverrideTier(t, convId, map[string]llmruntime.TierModelPick{
		"reasoning": {Provider: "anthropic", Model: "claude-opus-4-7"},
	})

	ctxOverride := llmruntime.ConversationTierOverrides{Picks: map[string]llmruntime.TierModelPick{
		"reasoning": {Provider: "bedrock", Model: "meta.llama3-1-70b-instruct-v1:0"},
	}}
	ctx := newCtxWithKVs(
		llmruntime.ContextKeyModelTier, llmruntime.ModelTierReasoning,
		llmruntime.ContextKeyLlmTierModelOverrides, ctxOverride,
	)
	res, err := llmruntime.ResolveLLMConfig(ctx, "", "", convId)
	assert.NoError(t, err)
	assert.Equal(t, "bedrock", res.Provider, "context-tier override wins over conversation-tier")
	assert.Equal(t, "meta.llama3-1-70b-instruct-v1:0", res.Model)
	assert.Equal(t, "context-override-tier", res.Source)
}
