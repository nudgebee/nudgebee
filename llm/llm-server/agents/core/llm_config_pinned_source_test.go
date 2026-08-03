package core

// Unit + integration tests for per-request LLM config-source pinning: parsing,
// config-name synthesis, resolver env-slot lookups, request-vs-slot validation
// (blocks silent slot-primary substitution), fallback model picking, downstream
// resolver short-circuits, and the end-to-end path through ResolveLLMConfig.

import (
	"context"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLLMIntegrations primes the per-integration cache so db-slot tests exercise
// the real resolution path without a database. The cache is what
// getLLMIntegrationsForAccount consults first, so only the SQL is bypassed —
// grouping, slot reads, and the visibility check all run for real.
func seedLLMIntegrations(t *testing.T, accountId string, integrations ...llmIntegration) {
	t.Helper()
	llmIntegrationsCacheMutex.Lock()
	llmIntegrationsCache[accountId] = struct {
		integrations []llmIntegration
		ts           time.Time
	}{integrations: integrations, ts: time.Now()}
	llmIntegrationsCacheMutex.Unlock()
	t.Cleanup(func() {
		llmIntegrationsCacheMutex.Lock()
		delete(llmIntegrationsCache, accountId)
		llmIntegrationsCacheMutex.Unlock()
	})
}

func TestParseConfigSourceId_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want parsedConfigSource
	}{
		{"env:global", parsedConfigSource{Layer: "env", Scope: "global"}},
		{"env:tier:reasoning", parsedConfigSource{Layer: "env", Scope: "tier", Name: "reasoning"}},
		{"env:tier:summary", parsedConfigSource{Layer: "env", Scope: "tier", Name: "summary"}},
		{"env:agent:soul_consolidate", parsedConfigSource{Layer: "env", Scope: "agent", Name: "soul_consolidate"}},
		{"db:6f2c-1a2b", parsedConfigSource{Layer: "db", Scope: "global", IntegrationUuid: "6f2c-1a2b"}},
		{"db:abc:tier:reasoning", parsedConfigSource{Layer: "db", Scope: "tier", Name: "reasoning", IntegrationUuid: "abc"}},
		{"db:xyz:agent:memory_compose", parsedConfigSource{Layer: "db", Scope: "agent", Name: "memory_compose", IntegrationUuid: "xyz"}},
	}
	for _, c := range cases {
		got, err := parseConfigSourceId(c.in)
		assert.NoError(t, err, "parse %q", c.in)
		assert.Equal(t, c.want, *got, "parsed shape for %q", c.in)
	}
}

func TestParseConfigSourceId_Invalid(t *testing.T) {
	cases := []string{
		"",                    // empty
		"env",                 // no scope
		"env:tier",            // tier scope requires a name
		"env:tier:",           // empty name
		"env:agent",           // agent scope requires a name
		"env:garbage",         // unknown scope
		"http:tier:reasoning", // unknown layer
		"db",                  // db requires uuid
		"db:",                 // empty uuid
		"db:uuid:tier",        // db tier needs a name
		"db:uuid:agent:",      // db agent needs a non-empty name
		"db:uuid:wrong:x",     // unknown db sub-scope
		"env:global:extra",    // env:global takes no name
	}
	for _, in := range cases {
		_, err := parseConfigSourceId(in)
		assert.Error(t, err, "should fail on %q", in)
	}
}

func TestConfigNameFor(t *testing.T) {
	// env slots are operator-owned — the integration name is ignored.
	envCases := map[string]string{
		"env:global":                 "System · Default",
		"env:tier:reasoning":         "System · Reasoning tier",
		"env:tier:summary":           "System · Summary tier",
		"env:agent:soul_consolidate": "System · Agent: soul_consolidate",
	}
	for id, want := range envCases {
		assert.Equal(t, want, ConfigNameFor(id, "ignored"), "config name for %q", id)
	}

	// db slots are tenant-owned — they read as the integration's own name.
	dbCases := map[string]string{
		"db:11111111-2222-3333-4444-555555555555":                      "Acme Azure",
		"db:11111111-2222-3333-4444-555555555555:tier:reasoning":       "Acme Azure · Reasoning tier",
		"db:11111111-2222-3333-4444-555555555555:agent:memory_compose": "Acme Azure · Agent: memory_compose",
	}
	for id, want := range dbCases {
		assert.Equal(t, want, ConfigNameFor(id, "Acme Azure"), "config name for %q", id)
	}

	// Malformed ids gracefully fall back to the raw id so the UI still shows
	// something instead of blowing up.
	assert.Equal(t, "not-a-real-id", ConfigNameFor("not-a-real-id", ""))
}

func TestDeriveConfigSource_Primary(t *testing.T) {
	assert.Equal(t, "env:global", deriveConfigSource("env:global", false))
	assert.Equal(t, "env:tier:reasoning", deriveConfigSource("env:tier:reasoning", false))
	assert.Equal(t, "env:tier:summary", deriveConfigSource("env:tier:summary", false))
	assert.Equal(t, "env:agent:soul_consolidate", deriveConfigSource("env:agent:soul_consolidate", false))
	// db-scoped sources carry the owning integration's uuid.
	assert.Equal(t, "db:int-1", deriveConfigSource("db:int-1", false))
	assert.Equal(t, "db:int-1:tier:reasoning", deriveConfigSource("db:int-1:tier:reasoning", false))
	assert.Equal(t, "db:int-1:agent:memory_compose", deriveConfigSource("db:int-1:agent:memory_compose", false))
}

func TestDeriveConfigSource_FallbackStripsToParent(t *testing.T) {
	// Fallback rows resolve to the PARENT slot's source id so requests round-
	// trip to the correct slot's endpoint / api-key. Strip only happens when
	// isFallback is true — the label alone is not enough (see the "agent
	// named 'fallback'" edge case below).
	assert.Equal(t, "env:global", deriveConfigSource("env:global:fallback", true))
	assert.Equal(t, "env:tier:reasoning", deriveConfigSource("env:tier:reasoning:fallback", true))
	assert.Equal(t, "env:tier:summary", deriveConfigSource("env:tier:summary:fallback", true))
	assert.Equal(t, "env:agent:soul_consolidate", deriveConfigSource("env:agent:soul_consolidate:fallback", true))
	assert.Equal(t, "db:int-1", deriveConfigSource("db:int-1:fallback", true))
	assert.Equal(t, "db:int-1:tier:reasoning", deriveConfigSource("db:int-1:tier:reasoning:fallback", true))
}

func TestDeriveConfigSource_AgentNamedFallback_PrimaryIsIntact(t *testing.T) {
	// Edge case that motivates the isFallback param: an agent literally named
	// "fallback" registers a primary row labeled "env:agent:fallback". If we
	// stripped ":fallback" unconditionally, this primary would parse as
	// "env:agent" and disappear from the picker, while the agent's fallback
	// rows would look like the primary. Passing isFallback=false disables
	// stripping and lets the primary through unmodified.
	assert.Equal(t, "env:agent:fallback", deriveConfigSource("env:agent:fallback", false),
		"primary of agent 'fallback' must be pickable")
	assert.Equal(t, "env:agent:fallback", deriveConfigSource("env:agent:fallback:fallback", true),
		"fallback of agent 'fallback' must round-trip to the parent slot")
}

func TestDeriveConfigSource_MalformedIsSkipped(t *testing.T) {
	// Any label that fails parseConfigSourceId (typos, incomplete refactors)
	// falls through to "" rather than being emitted as a pickable row.
	assert.Equal(t, "", deriveConfigSource("garbage", false))
	assert.Equal(t, "", deriveConfigSource("env:global:extra", false))     // extra segment
	assert.Equal(t, "", deriveConfigSource("env:tier:", false))            // empty name
	assert.Equal(t, "", deriveConfigSource("legacy-hyphen-scheme", false)) // old label leaks in
}

// resolveFromPinnedSource reads env-slot credentials through viper, so slots are
// set with setEnvKey (viper.Set) rather than t.Setenv — other tests in this
// package already use viper.Set for the same keys, and a viper.Set value
// permanently shadows the process environment. The global slot is set through
// the config struct fields directly. The one exception is the per-agent scan in
// GetAllConfiguredModels, which walks os.Environ() and therefore needs a real
// environment variable.
func TestResolveFromPinnedSource_EnvGlobal(t *testing.T) {
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	origEndpoint := config.Config.LlmProviderApiEndpoint
	origKey := config.Config.LlmProviderApiKey
	origType := config.Config.LlmProviderApiType
	origRegion := config.Config.LlmProviderRegion
	t.Cleanup(func() {
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
		config.Config.LlmProviderApiEndpoint = origEndpoint
		config.Config.LlmProviderApiKey = origKey
		config.Config.LlmProviderApiType = origType
		config.Config.LlmProviderRegion = origRegion
	})
	config.Config.LlmProvider = "huggingface"
	config.Config.LlmModel = "Qwen/Qwen3.6-35B-A3B-FP8"
	config.Config.LlmProviderApiEndpoint = "https://ep-a.example"
	config.Config.LlmProviderApiKey = "hf-key-a"
	config.Config.LlmProviderApiType = "openai"
	config.Config.LlmProviderRegion = "us-east-2"

	res, err := resolveFromPinnedSource(nil, "env:global", "", "", ModelTierReasoning)
	assert.NoError(t, err)
	assert.Equal(t, "huggingface", res.Provider)
	assert.Equal(t, "Qwen/Qwen3.6-35B-A3B-FP8", res.Model)
	assert.Equal(t, "https://ep-a.example", res.PinnedEndpoint)
	assert.Equal(t, "hf-key-a", res.PinnedApiKey)
	assert.Equal(t, "openai", res.PinnedApiType)
	assert.Equal(t, "us-east-2", res.PinnedRegion)
	assert.Equal(t, "env:global", res.PinnedConfigSource)
	assert.Equal(t, "pinned:env:global", res.Source)
	assert.True(t, res.IsOverridden)
}

func TestResolveFromPinnedSource_EnvTier(t *testing.T) {
	// Two HF endpoints on distinct tier slots — the whole point of this feature.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://endpoint-a.hf")
	setEnvKey(t, "llm_tier_api_key_reasoning", "hf-key-a")

	setEnvKey(t, "llm_tier_provider_summary", "huggingface")
	setEnvKey(t, "llm_tier_model_summary", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_summary", "https://endpoint-b.hf")
	setEnvKey(t, "llm_tier_api_key_summary", "hf-key-b")

	resR, err := resolveFromPinnedSource(nil, "env:tier:reasoning", "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "https://endpoint-a.hf", resR.PinnedEndpoint)
	assert.Equal(t, "hf-key-a", resR.PinnedApiKey)

	resS, err := resolveFromPinnedSource(nil, "env:tier:summary", "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "https://endpoint-b.hf", resS.PinnedEndpoint, "same model, different tier → different endpoint")
	assert.Equal(t, "hf-key-b", resS.PinnedApiKey, "same model, different tier → different api key")
}

func TestResolveFromPinnedSource_EnvAgent(t *testing.T) {
	setEnvKey(t, "llm_provider_soul_consolidate", "googleai")
	setEnvKey(t, "llm_model_name_soul_consolidate", "gemini-3.1-pro-preview")
	setEnvKey(t, "llm_provider_api_endpoint_soul_consolidate", "https://ep-agent.example")

	res, err := resolveFromPinnedSource(nil, "env:agent:soul_consolidate", "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "googleai", res.Provider)
	assert.Equal(t, "gemini-3.1-pro-preview", res.Model)
	assert.Equal(t, "https://ep-agent.example", res.PinnedEndpoint)
}

func TestResolveFromPinnedSource_MissingProvider(t *testing.T) {
	// Tier slot with nothing configured anywhere → hard error, no silent
	// fall-through to a different slot's credentials.
	origProvider := config.Config.LlmProvider
	t.Cleanup(func() { config.Config.LlmProvider = origProvider })
	config.Config.LlmProvider = ""

	_, err := resolveFromPinnedSource(nil, "env:tier:retrieval", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no provider set")
}

func TestResolveFromPinnedSource_TierProviderInheritsGlobal(t *testing.T) {
	// A slot configured as "global provider + per-tier model" is listed by
	// GetAllConfiguredModels (its tier provider falls back to global), so the
	// resolver must be able to serve it. Provider inherits; credentials do not.
	origProvider := config.Config.LlmProvider
	origEndpoint := config.Config.LlmProviderApiEndpoint
	t.Cleanup(func() {
		config.Config.LlmProvider = origProvider
		config.Config.LlmProviderApiEndpoint = origEndpoint
	})
	config.Config.LlmProvider = "openai"
	config.Config.LlmProviderApiEndpoint = "https://global.example"

	setEnvKey(t, "llm_tier_model_retrieval", "gpt-4o-mini")
	setEnvKey(t, "llm_tier_api_endpoint_retrieval", "https://retrieval.example")

	res, err := resolveFromPinnedSource(nil, "env:tier:retrieval", "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "openai", res.Provider, "provider inherits from the layer's global slot")
	assert.Equal(t, "gpt-4o-mini", res.Model)
	assert.Equal(t, "https://retrieval.example", res.PinnedEndpoint,
		"credentials stay slot-local — no inheritance from global")
}

// ─── db-scoped slots ───────────────────────────────────────────────────────

// Note on coverage: these seed the integration cache, so the SQL visibility
// filter in getLLMIntegrationsForAccount (and the common.Decrypt call on
// is_encrypted rows) is not exercised here — it needs a live metastore. What is
// exercised is everything built on top: uuid → integration lookup, rejection of
// ids the account can't see, and the per-slot key shapes.

func TestResolveFromPinnedSource_DbSlot(t *testing.T) {
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:   "int-1",
		Name: "Acme Azure",
		Config: map[string]string{
			"llm_provider":                        "azure",
			"llm_model_name":                      "gpt-4o",
			"llm_provider_api_endpoint":           "https://acme.openai.azure.com",
			"llm_provider_api_key":                "azure-key",
			"llm_provider_api_version":            "2024-06-01",
			"llm_tier_provider_reasoning":         "azure",
			"llm_tier_model_reasoning":            "o3-mini",
			"llm_tier_api_endpoint_reasoning":     "https://acme-reasoning.openai.azure.com",
			"llm_tier_api_key_reasoning":          "azure-reasoning-key",
			"llm_provider_memory_compose":         "openai",
			"llm_model_name_memory_compose":       "gpt-4o-mini",
			"llm_provider_api_key_memory_compose": "openai-key",
		},
	})

	global, err := resolveFromPinnedSource(nil, "db:int-1", "acct-1", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "azure", global.Provider)
	assert.Equal(t, "gpt-4o", global.Model)
	assert.Equal(t, "https://acme.openai.azure.com", global.PinnedEndpoint)
	assert.Equal(t, "azure-key", global.PinnedApiKey)
	assert.Equal(t, "2024-06-01", global.PinnedApiVersion)
	assert.Equal(t, "db:int-1", global.PinnedConfigSource)
	assert.True(t, global.IsOverridden)

	tier, err := resolveFromPinnedSource(nil, "db:int-1:tier:reasoning", "acct-1", "", ModelTierReasoning)
	assert.NoError(t, err)
	assert.Equal(t, "o3-mini", tier.Model)
	assert.Equal(t, "https://acme-reasoning.openai.azure.com", tier.PinnedEndpoint,
		"tier slot must use its own endpoint, not the integration's global one")
	assert.Equal(t, "azure-reasoning-key", tier.PinnedApiKey)

	agent, err := resolveFromPinnedSource(nil, "db:int-1:agent:memory_compose", "acct-1", "memory_compose", "")
	assert.NoError(t, err)
	assert.Equal(t, "openai", agent.Provider)
	assert.Equal(t, "gpt-4o-mini", agent.Model)
	assert.Equal(t, "openai-key", agent.PinnedApiKey)
}

func TestResolveFromPinnedSource_DbSlot_ForeignIntegrationRejected(t *testing.T) {
	// Tenant A's account can only see its own integrations, so pinning a uuid
	// that belongs to someone else must fail rather than resolve. This is the
	// last line of defence behind the query's visibility filter.
	seedLLMIntegrations(t, "acct-a", llmIntegration{
		Id:     "int-a",
		Name:   "Tenant A LLM",
		Config: map[string]string{"llm_provider": "azure", "llm_model_name": "gpt-4o"},
	})

	_, err := resolveFromPinnedSource(nil, "db:int-b", "acct-a", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not an LLM integration available to this account")
}

func TestResolveFromPinnedSource_DbSlot_RequiresAccount(t *testing.T) {
	// Without an account there is no tenant boundary to check against, so a db
	// pin can't be resolved safely.
	_, err := resolveFromPinnedSource(nil, "db:int-1", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires an account context")
}

func TestResolveFromPinnedSource_DbSlot_EmptySlotErrors(t *testing.T) {
	// Visible integration, but the requested tier isn't configured on it.
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:     "int-1",
		Name:   "Acme Azure",
		Config: map[string]string{"llm_provider": "azure", "llm_model_name": "gpt-4o"},
	})

	_, err := resolveFromPinnedSource(nil, "db:int-1:tier:summary", "acct-1", "", ModelTierSummary)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no model set")
}

func TestResolveFromPinnedSource_InvalidSourceId(t *testing.T) {
	_, err := resolveFromPinnedSource(nil, "not-a-real-id", "", "", "")
	assert.Error(t, err)
}

// ─── Request-vs-slot validation ────────────────────────────────────────────

// ctxWithProviderModel returns a security.RequestContext with the request-level
// provider/model overrides stamped on. Used to simulate a client sending both
// LlmProvider+LlmModelName alongside a pinned source.
func ctxWithProviderModel(provider, model string) *security.RequestContext {
	base := security.NewRequestContextForSuperAdmin()
	goCtx := base.GetContext()
	if provider != "" {
		goCtx = context.WithValue(goCtx, ContextKeyLlmProviderOverride, provider)
	}
	if model != "" {
		goCtx = context.WithValue(goCtx, ContextKeyLlmModelOverride, model)
	}
	return security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
}

func TestResolveFromPinnedSource_RequestProviderMismatch_Errors(t *testing.T) {
	// Slot has huggingface/Qwen; request lies with openai/gpt-4.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-a.hf")

	ctx := ctxWithProviderModel("openai", "gpt-4")
	_, err := resolveFromPinnedSource(ctx, "env:tier:reasoning", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pins provider")
}

func TestResolveFromPinnedSource_RequestModelMismatch_Errors(t *testing.T) {
	// Slot's primary model is Qwen with no fallbacks; request wants a model
	// that isn't the primary and isn't listed.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-a.hf")
	// no fallback set

	ctx := ctxWithProviderModel("huggingface", "meta-llama/Llama-3-70b")
	_, err := resolveFromPinnedSource(ctx, "env:tier:reasoning", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot serve model")
}

func TestResolveFromPinnedSource_FallbackModelIsHonoured(t *testing.T) {
	// Slot's primary is Qwen; fallback list has Llama-3-70b. Client pins the
	// slot AND asks for the fallback model — resolver must return the fallback
	// model with the SLOT's endpoint/api-key.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_model_fallbacks_reasoning", "meta-llama/Llama-3-70b, mistralai/Mixtral-8x7b")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-shared.hf")
	setEnvKey(t, "llm_tier_api_key_reasoning", "shared-key")

	ctx := ctxWithProviderModel("huggingface", "meta-llama/Llama-3-70b")
	res, err := resolveFromPinnedSource(ctx, "env:tier:reasoning", "", "", "")
	assert.NoError(t, err)
	assert.Equal(t, "huggingface", res.Provider)
	assert.Equal(t, "meta-llama/Llama-3-70b", res.Model, "fallback model must win over slot's primary")
	assert.Equal(t, "https://ep-shared.hf", res.PinnedEndpoint, "endpoint stays the slot's, not derived from the model")
	assert.Equal(t, "shared-key", res.PinnedApiKey)
}

func TestResolveFromPinnedSource_HalfSetOverride_Errors(t *testing.T) {
	// LlmProvider alone (without LlmModelName) is ambiguous — reject.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-a.hf")

	ctx := ctxWithProviderModel("huggingface", "")
	_, err := resolveFromPinnedSource(ctx, "env:tier:reasoning", "", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either both or neither")
}

func TestPinnedConfigSourceFromContext(t *testing.T) {
	assert.Equal(t, "", pinnedConfigSourceFromContext(nil))

	base := security.NewRequestContextForSuperAdmin()
	// Empty ctx value.
	assert.Equal(t, "", pinnedConfigSourceFromContext(base))

	// Set the key on a derived ctx.
	goCtx := context.WithValue(base.GetContext(), ContextKeyLlmConfigSourceOverride, "env:tier:reasoning")
	stamped := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
	assert.Equal(t, "env:tier:reasoning", pinnedConfigSourceFromContext(stamped))
}

// ─── ResolveLLMConfig wiring (integration) ────────────────────────────────

func TestResolveLLMConfig_PinnedBranch_UsesSlotCredentials(t *testing.T) {
	// End-to-end: stamp the context key that handleConversationRequest sets,
	// call ResolveLLMConfig, verify Pinned* fields propagate. Guards against
	// the pinned branch being accidentally bypassed in future refactors.
	setEnvKey(t, "llm_tier_provider_summary", "huggingface")
	setEnvKey(t, "llm_tier_model_summary", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_summary", "https://ep-summary.hf")
	setEnvKey(t, "llm_tier_api_key_summary", "summary-key")

	base := security.NewRequestContextForSuperAdmin()
	goCtx := context.WithValue(base.GetContext(), ContextKeyLlmConfigSourceOverride, "env:tier:summary")
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	res, err := ResolveLLMConfig(ctx, "", "some_agent", "")
	assert.NoError(t, err)
	assert.Equal(t, "huggingface", res.Provider)
	assert.Equal(t, "Qwen/Qwen3.6-35B-A3B-FP8", res.Model)
	assert.Equal(t, "https://ep-summary.hf", res.PinnedEndpoint)
	assert.Equal(t, "summary-key", res.PinnedApiKey)
	assert.Equal(t, "env:tier:summary", res.PinnedConfigSource)
	assert.Equal(t, "some_agent", res.AgentName)
}

func TestResolveLLMConfig_PinnedBranch_UsesPerRequestCache(t *testing.T) {
	// Sub-agent calls in the same request must reuse the cached pinned
	// resolution instead of re-reading env vars each time. Verifies the
	// cache-set / cache-get path in the pinned branch by asserting the
	// second call returns the SAME pointer (same *LLMConfigResolution).
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-a.hf")

	base := security.NewRequestContextForSuperAdmin()
	goCtx := base.GetContext()
	goCtx = context.WithValue(goCtx, ContextKeyLlmConfigSourceOverride, "env:tier:reasoning")
	// The per-request cache is normally installed by the HTTP handler; wire
	// it up manually here so the pinned branch can populate it.
	goCtx = context.WithValue(goCtx, ContextKeyLLMResolution, NewLLMResolutionCache())
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	first, err := ResolveLLMConfig(ctx, "acct-1", "planner", "conv-x")
	assert.NoError(t, err)
	assert.NotNil(t, first)

	second, err := ResolveLLMConfig(ctx, "acct-1", "planner", "conv-x")
	assert.NoError(t, err)
	assert.Same(t, first, second, "second call must reuse cached pinned resolution")

	// Different (agent, tier) tuple gets a separate cache entry — that's by
	// design so each sub-agent's AgentName/Tier fields don't collide.
	third, err := ResolveLLMConfig(ctx, "acct-1", "summariser", "conv-x")
	assert.NoError(t, err)
	assert.NotSame(t, first, third, "different agent name should produce a distinct cached resolution")
	assert.Equal(t, first.PinnedEndpoint, third.PinnedEndpoint, "but the underlying pinned credentials must match")
}

// ─── GetAllConfiguredModels emission ───────────────────────────────────────

// Regression guard on the picker's row set — asserts that GetAllConfiguredModels
// emits the shape the UI actually consumes, and specifically that the
// agent-named-"fallback" edge case doesn't disappear from the list. If a
// future addModel call site drops the primary-set guard, mixes up the label
// format, or breaks deriveConfigSource, this test fires.
func TestGetAllConfiguredModels_EmitsExpectedRows(t *testing.T) {
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	origFb := config.Config.LlmModelFallbacks
	t.Cleanup(func() {
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
		config.Config.LlmModelFallbacks = origFb
	})
	// Global slot: primary + one fallback.
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3.1-pro-preview"
	config.Config.LlmModelFallbacks = "gemini-3-flash-preview"

	// Two HF tier slots for the same model — the flagship two-endpoints case.
	setEnvKey(t, "llm_tier_provider_reasoning", "huggingface")
	setEnvKey(t, "llm_tier_model_reasoning", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_reasoning", "https://ep-a.hf")
	setEnvKey(t, "llm_tier_provider_summary", "huggingface")
	setEnvKey(t, "llm_tier_model_summary", "Qwen/Qwen3.6-35B-A3B-FP8")
	setEnvKey(t, "llm_tier_api_endpoint_summary", "https://ep-b.hf")

	// Fallback-only tier slot (no primary): must NOT emit fallback rows —
	// the resolver can't serve them without an endpoint.
	setEnvKey(t, "llm_tier_model_fallbacks_retrieval", "orphaned-fallback-model")

	// Agent literally named "fallback" — the isFallback-param edge case.
	// Its primary must land in the picker; its own fallback list must too.
	t.Setenv("LLM_PROVIDER_FALLBACK", "openai")
	setEnvKey(t, "llm_model_name_fallback", "gpt-4o")
	setEnvKey(t, "llm_model_fallbacks_fallback", "gpt-4o-mini")

	got, err := GetAllConfiguredModels("")
	assert.NoError(t, err)

	// Index rows by (source, model) so ordering doesn't matter.
	type key struct{ source, model string }
	byKey := map[key]ModelConfig{}
	for _, r := range got {
		byKey[key{r.Source, r.Model}] = r
	}

	// 1. env:global primary + its one fallback both pickable, both point at env:global.
	globalPrimary, ok := byKey[key{"env:global", "gemini-3.1-pro-preview"}]
	assert.True(t, ok, "env:global primary must be present")
	assert.Equal(t, "env:global", globalPrimary.ConfigSource)
	assert.Equal(t, "System · Default", globalPrimary.ConfigName)
	assert.False(t, globalPrimary.IsFallback)

	globalFb, ok := byKey[key{"env:global:fallback", "gemini-3-flash-preview"}]
	assert.True(t, ok, "env:global fallback must be present")
	assert.Equal(t, "env:global", globalFb.ConfigSource, "fallback ConfigSource must strip to parent slot")
	assert.Equal(t, "System · Default (fallback)", globalFb.ConfigName)
	assert.True(t, globalFb.IsFallback)

	// 2. Two HF-tier slots for the SAME model — distinct rows, distinct sources.
	reasoning := byKey[key{"env:tier:reasoning", "Qwen/Qwen3.6-35B-A3B-FP8"}]
	summary := byKey[key{"env:tier:summary", "Qwen/Qwen3.6-35B-A3B-FP8"}]
	assert.Equal(t, "env:tier:reasoning", reasoning.ConfigSource)
	assert.Equal(t, "env:tier:summary", summary.ConfigSource)
	assert.Equal(t, "System · Reasoning tier", reasoning.ConfigName)
	assert.Equal(t, "System · Summary tier", summary.ConfigName)

	// 3. Retrieval slot has fallbacks configured but NO primary — no row emitted.
	for _, r := range got {
		assert.NotEqual(t, "orphaned-fallback-model", r.Model,
			"slots with only fallbacks (no primary) must not emit any rows — resolver can't serve them")
	}

	// 4. Agent literally named "fallback": primary + its own fallback both present.
	// Both carry the SAME ConfigSource (env:agent:fallback); they're distinguished
	// only by model + IsFallback. This is the whole reason deriveConfigSource
	// keeps the isFallback parameter — dropping it here would silently unpick
	// the primary row.
	agentPrimary, ok := byKey[key{"env:agent:fallback", "gpt-4o"}]
	assert.True(t, ok, "primary of agent 'fallback' must be pickable")
	assert.Equal(t, "env:agent:fallback", agentPrimary.ConfigSource,
		"agent 'fallback' primary must round-trip to its own source id")
	assert.False(t, agentPrimary.IsFallback)

	agentFb, ok := byKey[key{"env:agent:fallback:fallback", "gpt-4o-mini"}]
	assert.True(t, ok, "fallback of agent 'fallback' must be pickable")
	assert.Equal(t, "env:agent:fallback", agentFb.ConfigSource,
		"agent 'fallback' fallback row must share the primary's source id")
	assert.True(t, agentFb.IsFallback)
}

// Db rows are emitted per integration so each one is independently pinnable,
// and each row's ConfigSource must round-trip through resolveFromPinnedSource.
func TestGetAllConfiguredModels_EmitsDbRows(t *testing.T) {
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	t.Cleanup(func() {
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
	})
	// Keep ENV out of the way so the assertions below are unambiguous.
	config.Config.LlmProvider = ""
	config.Config.LlmModel = ""

	seedLLMIntegrations(t, "acct-1",
		llmIntegration{
			Id:   "int-1",
			Name: "Acme Azure",
			Config: map[string]string{
				"llm_provider":              "azure",
				"llm_model_name":            "gpt-4o",
				"llm_model_fallbacks":       "gpt-4o-mini",
				"llm_provider_api_endpoint": "https://acme.openai.azure.com",
				// Tier slot with no provider of its own — inherits the
				// integration's global provider, same as the resolver does.
				"llm_tier_model_reasoning": "o3-mini",
			},
		},
		llmIntegration{
			Id:   "int-2",
			Name: "Acme Bedrock",
			Config: map[string]string{
				"llm_provider":   "bedrock",
				"llm_model_name": "anthropic.claude-sonnet-4",
			},
		},
	)

	got, err := GetAllConfiguredModels("acct-1")
	assert.NoError(t, err)

	byKey := map[string]ModelConfig{}
	for _, r := range got {
		byKey[r.Source] = r
	}

	// Two integrations → two distinct global rows, each labelled with its own name.
	azure, ok := byKey["db:int-1"]
	assert.True(t, ok, "integration int-1's global slot must be listed")
	assert.Equal(t, "db:int-1", azure.ConfigSource)
	assert.Equal(t, "Acme Azure", azure.ConfigName)
	assert.Equal(t, "gpt-4o", azure.Model)

	bedrock, ok := byKey["db:int-2"]
	assert.True(t, ok, "integration int-2's global slot must be listed")
	assert.Equal(t, "db:int-2", bedrock.ConfigSource)
	assert.Equal(t, "Acme Bedrock", bedrock.ConfigName)

	// Fallback row strips back to the parent integration's slot.
	fb, ok := byKey["db:int-1:fallback"]
	assert.True(t, ok, "integration fallback row must be listed")
	assert.Equal(t, "db:int-1", fb.ConfigSource)
	assert.Equal(t, "Acme Azure (fallback)", fb.ConfigName)
	assert.True(t, fb.IsFallback)

	// Tier row inherits the integration's provider.
	tier, ok := byKey["db:int-1:tier:reasoning"]
	assert.True(t, ok, "tier slot inheriting the integration provider must be listed")
	assert.Equal(t, "azure", tier.Provider)
	assert.Equal(t, "db:int-1:tier:reasoning", tier.ConfigSource)
	assert.Equal(t, "Acme Azure · Reasoning tier", tier.ConfigName)

	// Every emitted db row must actually resolve — a listed-but-unpinnable row
	// is the failure mode this assertion exists to catch.
	for _, r := range got {
		if r.ConfigSource == "" || r.IsFallback {
			continue
		}
		_, err := resolveFromPinnedSource(nil, r.ConfigSource, "acct-1", "", "")
		assert.NoError(t, err, "listed row %q must be pinnable", r.ConfigSource)
	}
}

// ─── Downstream resolver short-circuits ────────────────────────────────────

// Guards: if a future refactor removes a short-circuit at the top of one of the
// credential resolvers, the pinned Pinned* value would silently be ignored and
// the layered walk would kick in, potentially returning a different endpoint /
// key. These tests fail loudly on that regression.

func pinnedResolutionFixture() *LLMConfigResolution {
	return &LLMConfigResolution{
		Provider:           "huggingface",
		Model:              "Qwen/Qwen3.6-35B-A3B-FP8",
		Source:             "pinned:env:tier:reasoning",
		PinnedConfigSource: "env:tier:reasoning",
		PinnedEndpoint:     "https://pinned.example",
		PinnedApiKey:       "pinned-key",
		PinnedApiType:      "openai",
		PinnedApiVersion:   "v1",
		PinnedRegion:       "us-east-2",
	}
}

func TestGetLLMApiEndpoint_ShortCircuitsOnPinnedResolution(t *testing.T) {
	res := pinnedResolutionFixture()
	got := getLLMApiEndpoint("", "some-other-provider", "some-agent", true, res)
	assert.Equal(t, "https://pinned.example", got,
		"pinned endpoint must win even when the layered walk would return a different value")
}

func TestGetLLMApiKey_ShortCircuitsOnPinnedResolution(t *testing.T) {
	res := pinnedResolutionFixture()
	got := getLLMApiKey("", "some-other-provider", "some-agent", true, res)
	assert.Equal(t, "pinned-key", got)
}

func TestGetLLMApiType_ShortCircuitsOnPinnedResolution(t *testing.T) {
	res := pinnedResolutionFixture()
	got := getLLMApiType("", "some-other-provider", "some-agent", true, res)
	assert.Equal(t, "openai", got)
}

func TestGetLLMApiVersion_ShortCircuitsOnPinnedResolution(t *testing.T) {
	res := pinnedResolutionFixture()
	got := getLLMApiVersion("", "some-other-provider", "some-agent", true, res)
	assert.Equal(t, "v1", got)
}

func TestGetLLMRegion_ShortCircuitsOnPinnedResolution(t *testing.T) {
	res := pinnedResolutionFixture()
	got := getLLMRegion("", "some-other-provider", "some-agent", true, res)
	assert.Equal(t, "us-east-2", got)
}

// ─── credential identity ───────────────────────────────────────────────────

func TestBuildCredentials_ModelIsNotPartOfIdentity(t *testing.T) {
	// One integration, one api key, different models per tier. That is ONE
	// place to send requests — the model is a request parameter, not part of
	// the credential — so it must collapse to a single credential carrying
	// every model.
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:   "int-1",
		Name: "piyush-llm",
		Config: map[string]string{
			"llm_provider":             "googleai",
			"llm_model_name":           "gemini-3-flash",
			"llm_provider_api_key":     "key1",
			"llm_tier_model_reasoning": "gemini-3.1-pro",
			"llm_tier_model_summary":   "gemini-2.5-flash",
		},
	})

	models := []ModelConfig{
		{Provider: "googleai", Model: "gemini-3-flash", ConfigSource: "db:int-1", ConfigName: "piyush-llm"},
		{Provider: "googleai", Model: "gemini-3.1-pro", ConfigSource: "db:int-1:tier:reasoning", ConfigName: "piyush-llm · Reasoning tier"},
		{Provider: "googleai", Model: "gemini-2.5-flash", ConfigSource: "db:int-1:tier:summary", ConfigName: "piyush-llm · Summary tier"},
	}

	creds := buildCredentials(nil, "acct-1", models)

	require.Len(t, creds, 1, "same key + endpoint = one credential, whatever the models")
	assert.Equal(t, "piyush-llm", creds[0].Name, "the parent slot names it")
	assert.Equal(t, "db:int-1", creds[0].ConfigSource)
	assert.Len(t, creds[0].Sources, 3, "all three slots are recorded")
	assert.Len(t, creds[0].Models, 3, "and all three models hang off it")
}

func TestBuildCredentials_SeparateKeysAreSeparateCredentials(t *testing.T) {
	// The case that motivated all of this: a second api key added on one tier
	// of the SAME integration is a genuinely different credential.
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:   "int-1",
		Name: "piyush-llm",
		Config: map[string]string{
			"llm_provider":             "googleai",
			"llm_model_name":           "gemini-3-flash",
			"llm_provider_api_key":     "key1",
			"llm_tier_model_summary":   "gemini-2.5-flash",
			"llm_tier_api_key_summary": "key2",
		},
	})

	models := []ModelConfig{
		{Provider: "googleai", Model: "gemini-3-flash", ConfigSource: "db:int-1", ConfigName: "piyush-llm"},
		{Provider: "googleai", Model: "gemini-2.5-flash", ConfigSource: "db:int-1:tier:summary", ConfigName: "piyush-llm · Summary tier"},
	}

	creds := buildCredentials(nil, "acct-1", models)

	require.Len(t, creds, 2, "different api key = different credential")
	names := []string{creds[0].Name, creds[1].Name}
	assert.Contains(t, names, "piyush-llm")
	assert.Contains(t, names, "piyush-llm · Summary tier")
}

func TestBuildCredentials_AwsCredentialsCount(t *testing.T) {
	// access/secret/session are part of the destination — two tiers differing
	// only in AWS identity must not be folded together.
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:   "int-1",
		Name: "Acme Bedrock",
		Config: map[string]string{
			"llm_provider":                "bedrock",
			"llm_model_name":              "claude-sonnet",
			"llm_provider_access_key":     "AKIA-one",
			"llm_provider_secret_key":     "secret-one",
			"llm_tier_model_summary":      "claude-haiku",
			"llm_tier_access_key_summary": "AKIA-two",
			"llm_tier_secret_key_summary": "secret-two",
		},
	})

	models := []ModelConfig{
		{Provider: "bedrock", Model: "claude-sonnet", ConfigSource: "db:int-1", ConfigName: "Acme Bedrock"},
		{Provider: "bedrock", Model: "claude-haiku", ConfigSource: "db:int-1:tier:summary", ConfigName: "Acme Bedrock · Summary tier"},
	}

	creds := buildCredentials(nil, "acct-1", models)

	assert.Len(t, creds, 2, "different AWS identity = different credential")
}

func TestBuildCredentials_ProviderWhitespaceDoesNotSplit(t *testing.T) {
	// Dev data contains providers stored as "googleai " — cosmetic dirt must
	// not read as a separate credential.
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:     "int-1",
		Name:   "Acme",
		Config: map[string]string{"llm_provider": "googleai", "llm_model_name": "gemini-pro", "llm_provider_api_key": "k"},
	})

	models := []ModelConfig{
		{Provider: "googleai", Model: "gemini-pro", ConfigSource: "db:int-1", ConfigName: "Acme"},
		{Provider: "googleai ", Model: "gemini-pro", ConfigSource: "db:int-1", ConfigName: "Acme"},
	}

	creds := buildCredentials(nil, "acct-1", models)

	assert.Len(t, creds, 1, "trailing whitespace is not a different provider")
}

func TestBuildCredentials_NameNeverCarriesFallbackSuffix(t *testing.T) {
	// A fallback row's ConfigName ends in " (fallback)" but names the same slot
	// as its primary. Whichever row the walk reaches first must not leave the
	// credential called "Acme (fallback)".
	seedLLMIntegrations(t, "acct-1", llmIntegration{
		Id:     "int-1",
		Name:   "Acme",
		Config: map[string]string{"llm_provider": "googleai", "llm_model_name": "gemini-pro", "llm_provider_api_key": "k"},
	})

	// Fallback row deliberately placed FIRST — the order that used to decide it.
	models := []ModelConfig{
		{Provider: "googleai", Model: "gemini-flash", ConfigSource: "db:int-1", ConfigName: "Acme (fallback)", IsFallback: true},
		{Provider: "googleai", Model: "gemini-pro", ConfigSource: "db:int-1", ConfigName: "Acme"},
	}

	creds := buildCredentials(nil, "acct-1", models)

	require.Len(t, creds, 1)
	assert.Equal(t, "Acme", creds[0].Name, "credential name must not inherit a fallback row's suffix")
}

// ─── per-tier credentials ──────────────────────────────────────────────────

func TestTierPinFor_TierPickWithCredentialWins(t *testing.T) {
	// In By-task mode each task is a complete (credential, model) choice, so
	// its credential must beat a conversation-wide pin.
	base := security.NewRequestContextForSuperAdmin()
	overrides := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "googleai", Model: "gemini-3.1-pro", ConfigSource: "db:int-1"},
	}}
	goCtx := context.WithValue(base.GetContext(), ContextKeyLlmTierModelOverrides, overrides)
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	source, model := tierPinFor(ctx, "", ModelTierReasoning)
	assert.Equal(t, "db:int-1", source, "the tier's own credential is used")
	assert.Equal(t, "gemini-3.1-pro", model, "and its own model, not the slot primary")
}

func TestTierPinFor_LegacyPickWithoutCredentialDefersToConversationPin(t *testing.T) {
	// Picks stored before per-tier credentials existed carry no ConfigSource;
	// they must fall through so those conversations keep behaving as before.
	base := security.NewRequestContextForSuperAdmin()
	overrides := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "googleai", Model: "gemini-3.1-pro"},
	}}
	goCtx := context.WithValue(base.GetContext(), ContextKeyLlmTierModelOverrides, overrides)
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	source, model := tierPinFor(ctx, "", ModelTierReasoning)
	assert.Empty(t, source)
	assert.Empty(t, model)
}

func TestTierPinFor_UntaggedCallHasNoTierPin(t *testing.T) {
	// Calls with no tier keep using the conversation-wide pin.
	source, model := tierPinFor(nil, "", "")
	assert.Empty(t, source)
	assert.Empty(t, model)
}

func TestResolveLLMConfig_TierPickCredentialBeatsConversationPin(t *testing.T) {
	// End-to-end through ResolveLLMConfig: a conversation pinned to one
	// credential, and a reasoning pick naming another. The pick must win for
	// the reasoning-tagged call.
	seedLLMIntegrations(t, "acct-1",
		llmIntegration{Id: "int-1", Name: "A", Config: map[string]string{
			"llm_provider": "googleai", "llm_model_name": "model-a", "llm_provider_api_key": "key-a"}},
		llmIntegration{Id: "int-2", Name: "B", Config: map[string]string{
			"llm_provider": "googleai", "llm_model_name": "model-b", "llm_provider_api_key": "key-b"}},
	)

	base := security.NewRequestContextForSuperAdmin()
	goCtx := base.GetContext()
	// Conversation-wide pin → credential A.
	goCtx = context.WithValue(goCtx, ContextKeyLlmConfigSourceOverride, "db:int-1")
	// Reasoning pick → credential B with its own model.
	goCtx = context.WithValue(goCtx, ContextKeyLlmTierModelOverrides, ConversationTierOverrides{
		Picks: map[string]TierModelPick{"reasoning": {Provider: "googleai", Model: "model-b", ConfigSource: "db:int-2"}},
	})
	goCtx = context.WithValue(goCtx, ContextKeyModelTier, ModelTierReasoning)
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	res, err := ResolveLLMConfig(ctx, "acct-1", "planner", "")
	require.NoError(t, err)
	assert.Equal(t, "db:int-2", res.PinnedConfigSource, "the tier's credential wins over the conversation pin")
	assert.Equal(t, "model-b", res.Model)
	assert.Equal(t, "key-b", res.PinnedApiKey, "and its credentials come with it")
}

func TestResolveFromPinnedSource_UntrimmedProviderStillMatches(t *testing.T) {
	// Integration rows in dev and prod carry provider values with trailing
	// whitespace. credentialIdentity trims when grouping, so the picker offers
	// one credential; the request-vs-slot check has to trim too or it rejects a
	// request for the credential it just offered.
	seedLLMIntegrations(t, "acct-ws",
		llmIntegration{Id: "int-ws", Name: "Whitespace", Config: map[string]string{
			"llm_provider": "googleai ", "llm_model_name": "gemini-2.5-pro",
			"llm_provider_api_key": "key-ws"}},
	)

	base := security.NewRequestContextForSuperAdmin()
	goCtx := base.GetContext()
	goCtx = context.WithValue(goCtx, ContextKeyLlmProviderOverride, "googleai")
	goCtx = context.WithValue(goCtx, ContextKeyLlmModelOverride, "gemini-2.5-pro")
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	res, err := resolveFromPinnedSource(ctx, "db:int-ws", "acct-ws", "planner", "")
	require.NoError(t, err, "trimmed request provider must match an untrimmed slot provider")
	assert.Equal(t, "gemini-2.5-pro", res.Model)
	assert.Equal(t, "key-ws", res.PinnedApiKey)
}

func TestResolveFromPinnedSource_GenuineProviderMismatchStillRejected(t *testing.T) {
	// Trimming must not weaken the check itself.
	seedLLMIntegrations(t, "acct-mm",
		llmIntegration{Id: "int-mm", Name: "Mismatch", Config: map[string]string{
			"llm_provider": "googleai", "llm_model_name": "gemini-2.5-pro",
			"llm_provider_api_key": "key-mm"}},
	)

	base := security.NewRequestContextForSuperAdmin()
	goCtx := base.GetContext()
	goCtx = context.WithValue(goCtx, ContextKeyLlmProviderOverride, "openai")
	goCtx = context.WithValue(goCtx, ContextKeyLlmModelOverride, "gpt-4o")
	ctx := security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())

	_, err := resolveFromPinnedSource(ctx, "db:int-mm", "acct-mm", "planner", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pins provider")
}

func TestParseConfigSourceId_TrimsSurroundingWhitespace(t *testing.T) {
	// Client-supplied ids: whitespace would otherwise surface as "unknown
	// layer" or "not an integration available to this account", both of which
	// point at the wrong problem.
	for _, raw := range []string{" env:global", "env:global ", "  db:int-1  "} {
		p, err := parseConfigSourceId(raw)
		require.NoError(t, err, "raw=%q", raw)
		assert.NotEmpty(t, p.Layer, "raw=%q", raw)
	}
}

func TestIntegrationConfigForPin_DoesNotAliasTheCache(t *testing.T) {
	// The returned map must not be the cached one: a caller writing to it would
	// corrupt every other request on the account and race with readers.
	seedLLMIntegrations(t, "acct-alias",
		llmIntegration{Id: "int-alias", Name: "A", Config: map[string]string{
			"llm_provider": "googleai", "llm_model_name": "model-a"}},
	)
	ctx := security.NewRequestContextForSuperAdmin()

	first, err := integrationConfigForPin(ctx, "acct-alias", "int-alias")
	require.NoError(t, err)
	first["llm_model_name"] = "mutated"

	second, err := integrationConfigForPin(ctx, "acct-alias", "int-alias")
	require.NoError(t, err)
	assert.Equal(t, "model-a", second["llm_model_name"], "cache must be unaffected by a caller's write")
}

func TestGetLLMIntegrationsForAccount_ReturnsDeepCopy(t *testing.T) {
	// The guarantee lives at the accessor now: callers can't reach the cached
	// slice or its Config maps, so no caller can corrupt the cache — and a
	// write racing another goroutine's read would be a fatal panic, not a
	// subtle bug.
	seedLLMIntegrations(t, "acct-copy",
		llmIntegration{Id: "int-copy", Name: "Orig", Config: map[string]string{"llm_provider": "googleai"}},
	)

	first, err := getLLMIntegrationsForAccount(nil, "acct-copy")
	require.NoError(t, err)
	require.Len(t, first, 1)
	first[0].Name = "mutated"
	first[0].Config["llm_provider"] = "mutated"

	second, err := getLLMIntegrationsForAccount(nil, "acct-copy")
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "Orig", second[0].Name, "cached struct must be unaffected")
	assert.Equal(t, "googleai", second[0].Config["llm_provider"], "cached Config map must be unaffected")
}

func TestResolveFromPinnedSource_ForeignProviderSlotDoesNotInheritGlobalCreds(t *testing.T) {
	// An integration whose reasoning tier is a *different provider* than the
	// global must not gap-fill credentials from the global slot: pairing an
	// azure tier with the openai endpoint/key is the leak behind #35000/#34834.
	seedLLMIntegrations(t, "acct-mixed",
		llmIntegration{Id: "int-mixed", Name: "Mixed", Config: map[string]string{
			"llm_provider":                "openai",
			"llm_model_name":              "gpt-4o",
			"llm_provider_api_key":        "sk-openai-global",
			"llm_provider_api_endpoint":   "https://api.openai.com/v1",
			"llm_tier_provider_reasoning": "azure",
			"llm_tier_model_reasoning":    "gpt-4o-azure",
			"llm_tier_api_key_reasoning":  "azure-key",
		}},
	)
	ctx := security.NewRequestContextForSuperAdmin()

	res, err := resolveFromPinnedSource(ctx, "db:int-mixed:tier:reasoning", "acct-mixed", "planner", ModelTierReasoning)
	require.NoError(t, err)
	assert.Equal(t, "azure", res.Provider)
	assert.Equal(t, "azure-key", res.PinnedApiKey, "the tier's own key must win")
	assert.Empty(t, res.PinnedEndpoint, "must NOT inherit the openai endpoint for an azure tier")
	assert.NotEqual(t, "sk-openai-global", res.PinnedApiKey)
}

func TestResolveFromPinnedSource_SameProviderSlotStillInheritsGlobalCreds(t *testing.T) {
	// The gap-fill must keep working when the providers agree — that's the
	// common case (a tier overriding only the model).
	seedLLMIntegrations(t, "acct-same",
		llmIntegration{Id: "int-same", Name: "Same", Config: map[string]string{
			"llm_provider":                "openai",
			"llm_model_name":              "gpt-4o",
			"llm_provider_api_key":        "sk-openai-global",
			"llm_provider_api_endpoint":   "https://api.openai.com/v1",
			"llm_tier_provider_reasoning": "openai",
			"llm_tier_model_reasoning":    "o3",
		}},
	)
	ctx := security.NewRequestContextForSuperAdmin()

	res, err := resolveFromPinnedSource(ctx, "db:int-same:tier:reasoning", "acct-same", "planner", ModelTierReasoning)
	require.NoError(t, err)
	assert.Equal(t, "o3", res.Model)
	assert.Equal(t, "sk-openai-global", res.PinnedApiKey, "same provider still gap-fills")
	assert.Equal(t, "https://api.openai.com/v1", res.PinnedEndpoint)
}

func TestResolveFromPinnedSource_SlotWithNoProviderInheritsGlobalAndItsCreds(t *testing.T) {
	// A tier that sets only a model inherits the global provider, so its
	// credentials are compatible by construction.
	seedLLMIntegrations(t, "acct-noprov",
		llmIntegration{Id: "int-noprov", Name: "NoProv", Config: map[string]string{
			"llm_provider":             "openai",
			"llm_model_name":           "gpt-4o",
			"llm_provider_api_key":     "sk-openai-global",
			"llm_tier_model_reasoning": "o3",
		}},
	)
	ctx := security.NewRequestContextForSuperAdmin()

	res, err := resolveFromPinnedSource(ctx, "db:int-noprov:tier:reasoning", "acct-noprov", "planner", ModelTierReasoning)
	require.NoError(t, err)
	assert.Equal(t, "openai", res.Provider)
	assert.Equal(t, "sk-openai-global", res.PinnedApiKey)
}

// ─── whole-config pins (":all") ────────────────────────────────────────────
//
// ":all" is the one scope that is not a single slot: it names a config and lets
// each call's tier choose the slot inside it. Every other scope resolves to the
// same slot no matter which tier asked.

func TestParseConfigSourceId_AllScope(t *testing.T) {
	cases := []struct {
		in   string
		want parsedConfigSource
	}{
		{"env:all", parsedConfigSource{Layer: "env", Scope: "all"}},
		{"db:int-1:all", parsedConfigSource{Layer: "db", Scope: "all", IntegrationUuid: "int-1"}},
	}
	for _, c := range cases {
		got, err := parseConfigSourceId(c.in)
		require.NoError(t, err, "parse %q", c.in)
		assert.Equal(t, c.want, *got, "parsed shape for %q", c.in)
	}

	// ":all" takes no name — the tier comes from the call, not the id.
	for _, in := range []string{"env:all:reasoning", "db:int-1:all:reasoning"} {
		_, err := parseConfigSourceId(in)
		assert.Error(t, err, "should fail on %q", in)
	}
}

func TestConfigNameFor_AllScope(t *testing.T) {
	// The whole config reads as the config itself — no slot suffix, because no
	// one slot is being named.
	assert.Equal(t, "Acme Azure", ConfigNameFor("db:int-1:all", "Acme Azure"))
	assert.Equal(t, "System", ConfigNameFor("env:all", "ignored"))
}

func TestResolveFromPinnedSource_AllScope_UsesTheCallersTier(t *testing.T) {
	seedLLMIntegrations(t, "acct-all", llmIntegration{
		Id:   "int-all",
		Name: "Tiered Config",
		Config: map[string]string{
			"llm_provider":                "azure",
			"llm_model_name":              "gpt-4o",
			"llm_provider_api_key":        "shared-key",
			"llm_provider_api_endpoint":   "https://acme.openai.azure.com",
			"llm_tier_provider_reasoning": "azure",
			"llm_tier_model_reasoning":    "o3-mini",
			"llm_tier_provider_summary":   "azure",
			"llm_tier_model_summary":      "gpt-4o-mini",
		},
	})

	reasoning, err := resolveFromPinnedSource(nil, "db:int-all:all", "acct-all", "", ModelTierReasoning)
	require.NoError(t, err)
	assert.Equal(t, "o3-mini", reasoning.Model, "a tier-tagged call must read that tier's slot")
	// The tier defines no key of its own, so it inherits the config's — without
	// this the pin would resolve with an empty key and the request would fail.
	assert.Equal(t, "shared-key", reasoning.PinnedApiKey)
	assert.Equal(t, "db:int-all:all", reasoning.PinnedConfigSource,
		"the pin must still record what was selected, not the slot it landed on")

	summary, err := resolveFromPinnedSource(nil, "db:int-all:all", "acct-all", "", ModelTierSummary)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o-mini", summary.Model)

	// Untagged calls (background work: titles, memory) have no tier to honour,
	// so they get the config's base model.
	untagged, err := resolveFromPinnedSource(nil, "db:int-all:all", "acct-all", "", "")
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", untagged.Model)
}

func TestResolveFromPinnedSource_AllScope_FallsBackWhenTierUndefined(t *testing.T) {
	// A config with no tier slots is the common case. ":all" on it must behave
	// exactly like a plain config pin — resolving an empty tier slot would send
	// a request with no model at all.
	seedLLMIntegrations(t, "acct-flat", llmIntegration{
		Id:   "int-flat",
		Name: "Flat Config",
		Config: map[string]string{
			"llm_provider":         "googleai",
			"llm_model_name":       "gemini-3-flash-preview",
			"llm_provider_api_key": "g-key",
		},
	})

	for _, tier := range []ModelTier{ModelTierReasoning, ModelTierRetrieval, ModelTierSummary, ""} {
		res, err := resolveFromPinnedSource(nil, "db:int-flat:all", "acct-flat", "", tier)
		require.NoError(t, err, "tier %q", tier)
		assert.Equal(t, "gemini-3-flash-preview", res.Model, "tier %q must fall back to the config's base model", tier)
		assert.Equal(t, "g-key", res.PinnedApiKey, "tier %q", tier)
	}
}

func TestResolveFromPinnedSource_AllScope_PartialTiersFallBackPerTier(t *testing.T) {
	// Only summary is configured. Reasoning must not silently borrow it.
	seedLLMIntegrations(t, "acct-partial", llmIntegration{
		Id:   "int-partial",
		Name: "Summary Only",
		Config: map[string]string{
			"llm_provider":              "googleai",
			"llm_model_name":            "gemini-3.1-pro-preview",
			"llm_provider_api_key":      "g-key",
			"llm_tier_provider_summary": "googleai",
			"llm_tier_model_summary":    "gemini-2.5-flash",
		},
	})

	summary, err := resolveFromPinnedSource(nil, "db:int-partial:all", "acct-partial", "", ModelTierSummary)
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-flash", summary.Model)

	reasoning, err := resolveFromPinnedSource(nil, "db:int-partial:all", "acct-partial", "", ModelTierReasoning)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro-preview", reasoning.Model, "an undefined tier falls back to the config's base model")
}

func TestResolveFromPinnedSource_AllScope_RejectsAModelSentAlongside(t *testing.T) {
	// The two intents contradict: ":all" says the config picks the model. Worse,
	// honouring both would validate against a different slot per tier, so the
	// same request would pass on one call and fail on the next.
	seedLLMIntegrations(t, "acct-rej", llmIntegration{
		Id:     "int-rej",
		Name:   "Tiered Config",
		Config: map[string]string{"llm_provider": "azure", "llm_model_name": "gpt-4o", "llm_provider_api_key": "k"},
	})

	_, err := resolveFromPinnedSource(ctxWithProviderModel("azure", "gpt-4o"), "db:int-rej:all", "acct-rej", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selects a whole config")
}
