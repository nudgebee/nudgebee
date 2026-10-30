package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/config"
)

// One configured fallback per integration (#36339): either a reference to an
// existing slot, or a fallback-owned slot. Both run on their own credentials.

// geminiIntegration is the account's primary; the legacy list stays same-provider.
func geminiIntegration(extra map[string]string) map[string]string {
	cfg := map[string]string{
		"llm_provider":         "googleai",
		"llm_model_name":       "gemini-3.1-pro-preview",
		"llm_provider_api_key": "gemini-key",
		"llm_model_fallbacks":  "gemini-3-flash-preview",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

// openaiGateway is a separate integration a fallback can reference; its key and
// endpoint differ from the primary's.
var openaiGateway = llmIntegration{
	Id:   "int-openai-gw",
	Name: "gateway-openai",
	Config: map[string]string{
		"llm_provider":              "openai",
		"llm_model_name":            "gpt-5.6-terra",
		"llm_model_fallbacks":       "gpt-5.6-luna",
		"llm_provider_api_key":      "gateway-key",
		"llm_provider_api_endpoint": "https://gateway.example/openai",
	},
}

// seedAccount makes cfg the integration serving acct at runtime and lists the
// integrations acct can see (what a db: reference resolves against).
func seedAccount(t *testing.T, acct string, cfg map[string]string, visible ...llmIntegration) *LLMConfigResolution {
	t.Helper()
	pinGlobalModel(t, "", "")
	seedDBConfig(t, acct, cfg)
	seedLLMIntegrations(t, acct, visible...)
	res, err := ResolveLLMConfig(newCtxWith(ContextKeyModelTier, ModelTier("")), acct, "", "")
	require.NoError(t, err)
	return res
}

func TestParseFallbackModels_LegacyListStaysBareModels(t *testing.T) {
	// Colons are part of model ids (Bedrock), never a provider qualifier.
	assert.Equal(t,
		[]FallbackModel{{Model: "anthropic.claude-3-5-sonnet-20241022-v2:0"}, {Model: "Qwen/B"}},
		parseFallbackModels(" anthropic.claude-3-5-sonnet-20241022-v2:0 , ,Qwen/B"))
}

func TestConfiguredFallback_ReferenceUsesReferencedSlotCredentials(t *testing.T) {
	const acct = "acct-fb-ref"
	res := seedAccount(t, acct, geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw",
		llmFallbackRefModelKey:  "gpt-5.6-terra",
	}), openaiGateway)

	fb, err := configuredFallback(nil, acct, res)
	require.NoError(t, err)
	require.NotNil(t, fb)
	assert.Equal(t, "openai", fb.Provider)
	assert.Equal(t, "gpt-5.6-terra", fb.Model)
	assert.Equal(t, "db:int-openai-gw", fb.Resolution.PinnedConfigSource)
	assert.Equal(t, "gateway-key", getLLMApiKey(acct, "openai", "", false, fb.Resolution))
	assert.Equal(t, "https://gateway.example/openai", getLLMApiEndpoint(acct, "openai", "", false, fb.Resolution))

	t.Run("a same-provider legacy fallback of the slot is a valid model too", func(t *testing.T) {
		res := seedAccount(t, acct, geminiIntegration(map[string]string{
			llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-luna",
		}), openaiGateway)
		fb, err := configuredFallback(nil, acct, res)
		require.NoError(t, err)
		assert.Equal(t, "gpt-5.6-luna", fb.Model)
	})
}

func TestConfiguredFallback_BrokenReferenceIsRejectedByName(t *testing.T) {
	cases := map[string]struct {
		ref, model string
		visible    []llmIntegration
		want       string
	}{
		"referenced integration disabled or deleted (not visible)": {"db:int-openai-gw", "gpt-5.6-terra", nil, "int-openai-gw"},
		"slot no longer serves the model":                          {"db:int-openai-gw", "gpt-4o", []llmIntegration{openaiGateway}, `no longer serves model "gpt-4o"`},
		"whole-config source":                                      {"db:int-openai-gw:all", "gpt-5.6-terra", []llmIntegration{openaiGateway}, "whole-config"},
		"malformed source":                                         {"nonsense", "x", nil, "nonsense"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			const acct = "acct-fb-broken"
			res := seedAccount(t, acct, geminiIntegration(map[string]string{
				llmFallbackRefSourceKey: c.ref, llmFallbackRefModelKey: c.model,
			}), c.visible...)
			fb, err := configuredFallback(nil, acct, res)
			require.Error(t, err)
			assert.Nil(t, fb, "a broken reference must never be substituted")
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestConfiguredFallback_NewSlotOwnsItsConfiguration(t *testing.T) {
	const acct = "acct-fb-new"
	// The primary is a custom gateway with OAuth, extra headers, a deployment and
	// thinking disabled — none of it may reach the fallback's endpoint.
	res := seedAccount(t, acct, map[string]string{
		"llm_provider":                 "custom",
		"llm_model_name":               "Qwen/Qwen3.6-35B-A3B-FP8",
		"llm_provider_api_endpoint":    "https://primary.example/v1",
		"llm_provider_api_key":         "primary-key",
		"llm_auth_type":                "oauth_client_credentials",
		"llm_oauth_token_url":          "https://idp.example/token",
		"llm_oauth_client_id":          "primary-client",
		"llm_oauth_client_secret":      "primary-secret",
		"llm_extra_headers":            `{"X-Tenant":"primary"}`,
		"llm_provider_deployment_name": "primary-deploy",
		"llm_disable_thinking":         "true",
		llmFallbackProviderKey:         "openai",
		llmFallbackModelKey:            "gpt-5.6-terra",
		"llm_fallback_api_key":         "fallback-key",
		"llm_fallback_api_endpoint":    "https://fallback.example/openai",
		llmFallbackContextSizeKey:      "128000",
	})

	fb, err := configuredFallback(nil, acct, res)
	require.NoError(t, err)
	require.NotNil(t, fb)
	r := fb.Resolution
	assert.Equal(t, "openai", fb.Provider)
	assert.Equal(t, "db-fallback", r.PinnedConfigSource)
	assert.Equal(t, "fallback-key", getLLMApiKey(acct, "openai", "", false, r))
	assert.Equal(t, "https://fallback.example/openai", getLLMApiEndpoint(acct, "openai", "", false, r))
	assert.Equal(t, 128000, r.MaxContext)

	auth := resolveLLMAuthSettings(acct, r)
	assert.Equal(t, llmAuthTypeAPIKey, auth.AuthType, "the primary's OAuth must not apply to the fallback")
	assert.Empty(t, auth.OAuth.ClientSecret)
	assert.Empty(t, auth.ExtraHeaders)
	assert.Empty(t, resolveLLMDeploymentName(acct, r))
	assert.False(t, getLLMDisableThinking(acct, r))
}

// A new fallback carries its own OAuth, headers, deployment and thinking flag.
func TestConfiguredFallback_NewSlotUsesItsOwnAuthAndGatewaySettings(t *testing.T) {
	const acct = "acct-fb-new-auth"
	for _, scope := range []ModelTier{"", "reasoning"} {
		k := func(base string) string { return fallbackKey(base, scope) }
		res := seedAccount(t, acct, geminiIntegration(map[string]string{
			k(llmFallbackProviderKey):             "custom",
			k(llmFallbackModelKey):                "gpt-5.6-terra",
			k("llm_fallback_api_endpoint"):        "https://gw.example/v1",
			k("llm_fallback_api_type"):            "azure_ad",
			k("llm_fallback_auth_type"):           "oauth_client_credentials",
			k("llm_fallback_oauth_token_url"):     "https://idp.example/token",
			k("llm_fallback_oauth_client_id"):     "fb-client",
			k("llm_fallback_oauth_client_secret"): "fb-secret",
			k("llm_fallback_oauth_scope"):         "api://gw/.default",
			k("llm_fallback_extra_headers"):       `{"X-Project":"fb"}`,
			k("llm_fallback_deployment_name"):     "gpt-5.6-terra_2026-07-09",
			k("llm_fallback_disable_thinking"):    "true",
		}))
		res.Tier = scope

		fb, err := configuredFallback(nil, acct, res)
		require.NoError(t, err)
		require.NotNil(t, fb)
		r := fb.Resolution

		auth := resolveLLMAuthSettings(acct, r)
		assert.Equal(t, "oauth_client_credentials", auth.AuthType, "scope %q", scope)
		assert.Equal(t, llmOAuthConfig{TokenURL: "https://idp.example/token", ClientID: "fb-client", ClientSecret: "fb-secret", Scope: "api://gw/.default"}, auth.OAuth)
		assert.Equal(t, map[string]string{"X-Project": "fb"}, auth.ExtraHeaders)
		assert.Equal(t, "gpt-5.6-terra_2026-07-09", resolveLLMDeploymentName(acct, r))
		assert.True(t, getLLMDisableThinking(acct, r))
	}
}

func TestConfiguredFallback_NoneAndIncomplete(t *testing.T) {
	const acct = "acct-fb-none"
	res := seedAccount(t, acct, geminiIntegration(nil))
	fb, err := configuredFallback(nil, acct, res)
	assert.NoError(t, err)
	assert.Nil(t, fb, "nothing configured -> legacy behaviour only")

	res = seedAccount(t, acct, geminiIntegration(map[string]string{llmFallbackProviderKey: "openai"}))
	_, err = configuredFallback(nil, acct, res)
	require.Error(t, err, "a provider without a model is rejected, not silently skipped")
	assert.Contains(t, err.Error(), "no model")
}

// builtClient records what each fallback client was built with.
type builtClient struct {
	provider, model string
	res             *LLMConfigResolution
}

func recordingSeam(t *testing.T, byModel map[string]llms.Model) *[]builtClient {
	t.Helper()
	var built []builtClient
	prev := newLLMModel
	newLLMModel = func(provider, model, _ string, _ bool, _ string, res ...*LLMConfigResolution) (llms.Model, error) {
		built = append(built, builtClient{provider, model, res[0]})
		return byModel[model], nil
	}
	t.Cleanup(func() { newLLMModel = prev })
	return &built
}

func newFallbackRC(acct string, res *LLMConfigResolution) *retryContext {
	return &retryContext{
		ctx:              newCtxWith(ContextKeyModelTier, ModelTier("")),
		llm:              &fakeLLMModel{err: errors.New("primary down")},
		promptMessages:   []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")},
		currentModel:     res.Model,
		currentProvider:  res.Provider,
		triedModels:      map[string]bool{res.Model: true},
		accountId:        acct,
		lastErr:          errors.New("primary down"),
		totalStart:       time.Now(),
		maxTotalDuration: time.Minute,
		resolution:       res,
	}
}

// Gemini primary -> legacy same-provider fallback (unchanged: primary's creds) ->
// the configured OpenAI fallback on the referenced slot. Usage attributes openai.
func TestFallbackChain_LegacyFirstThenConfiguredFallback(t *testing.T) {
	const acct = "acct-fb-chain"
	res := seedAccount(t, acct, geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw",
		llmFallbackRefModelKey:  "gpt-5.6-terra",
	}), openaiGateway)
	built := recordingSeam(t, map[string]llms.Model{
		"gemini-3-flash-preview": &fakeLLMModel{err: errors.New("provider rejected request")},
		"gpt-5.6-terra":          &fakeLLMModel{response: "ok"},
	})

	rc := newFallbackRC(acct, res)
	_, md, err := handleQuotaError(rc, fallbackChain(rc, getLLMFallbackModelName(acct, "", "", true, res)), false, fallbackCauseUnavailable)
	require.NoError(t, err)

	require.Len(t, *built, 2)
	legacy, configured := (*built)[0], (*built)[1]
	assert.Equal(t, [2]string{"googleai", "gemini-3-flash-preview"}, [2]string{legacy.provider, legacy.model})
	assert.Same(t, res, legacy.res, "a legacy entry keeps the primary's resolution, exactly as before")
	assert.Equal(t, [2]string{"openai", "gpt-5.6-terra"}, [2]string{configured.provider, configured.model})
	assert.Equal(t, "gateway-key", getLLMApiKey(acct, "openai", "", false, configured.res))

	assert.Equal(t, "openai", md.ServedProvider)
	assert.Equal(t, "gpt-5.6-terra", md.ServedModel)
	assert.Equal(t, "db:int-openai-gw", md.ConfigSource)
	require.NotNil(t, md.FallbackFromModel)
	assert.Equal(t, "gemini-3.1-pro-preview", *md.FallbackFromModel)
}

// Hugging Face primary -> a new Google AI fallback it owns.
func TestFallbackChain_HuggingFacePrimaryToNewGoogleFallback(t *testing.T) {
	const acct = "acct-fb-hf"
	res := seedAccount(t, acct, map[string]string{
		"llm_provider":              "huggingface",
		"llm_model_name":            "Qwen/Qwen3.6-35B-A3B-FP8",
		"llm_provider_api_key":      "hf-key",
		"llm_provider_api_endpoint": "https://hf.example",
		llmFallbackProviderKey:      "googleai",
		llmFallbackModelKey:         "gemini-3-flash-preview",
		"llm_fallback_api_key":      "google-key",
	})
	built := recordingSeam(t, map[string]llms.Model{"gemini-3-flash-preview": &fakeLLMModel{response: "ok"}})

	rc := newFallbackRC(acct, res)
	_, md, err := handleQuotaError(rc, fallbackChain(rc, ""), false, fallbackCauseUnavailable)
	require.NoError(t, err)
	require.Len(t, *built, 1)
	assert.Equal(t, "googleai", (*built)[0].provider)
	assert.Equal(t, "google-key", getLLMApiKey(acct, "googleai", "", false, (*built)[0].res))
	assert.Empty(t, getLLMApiEndpoint(acct, "googleai", "", false, (*built)[0].res), "must not inherit the HF endpoint")
	assert.Equal(t, "googleai", md.ServedProvider)
	assert.Equal(t, "db-fallback", md.ConfigSource)
}

// A broken configured fallback is recorded and the legacy chain still runs.
func TestFallbackChain_BrokenReferenceDoesNotSubstitute(t *testing.T) {
	const acct = "acct-fb-chain-broken"
	res := seedAccount(t, acct, geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
	})) // referenced integration not visible
	rc := newFallbackRC(acct, res)
	chain := fallbackChain(rc, "gemini-3-flash-preview")
	assert.Equal(t, []FallbackModel{{Model: "gemini-3-flash-preview"}}, chain)
	require.NotEmpty(t, rc.errorHistory)
	assert.Contains(t, rc.errorHistory[0], "db:int-openai-gw")
}

// A pinned request (explicit llm_config_source) never runs fallbacks — legacy or configured.
func TestPinnedRequestRunsNoConfiguredFallback(t *testing.T) {
	const acct = "acct-fb-pinned"
	seedAccount(t, acct, geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
	}), openaiGateway)
	built := recordingSeam(t, map[string]llms.Model{})

	pinned := &LLMConfigResolution{Provider: "googleai", Model: "gemini-3.1-pro-preview", AccountId: acct,
		IsOverridden: true, PinnedConfigSource: "db:int-gemini"}
	_, _, err := generateLLMContentWithRetry(newCtxWith(ContextKeyModelTier, ModelTier("")),
		&fakeLLMModel{err: errors.New("provider rejected request")},
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")}, nil,
		"", "", acct, "", "", false, "", pinned)
	require.Error(t, err)
	assert.Empty(t, *built, "no fallback client may be built for a pinned request")
}

func TestFallbackProbeTargets(t *testing.T) {
	t.Run("reference is resolved per account, as runtime does", func(t *testing.T) {
		seedLLMIntegrations(t, "acct-probe-a", openaiGateway)
		seedLLMIntegrations(t, "acct-probe-b") // cannot see the referenced integration
		targets := fallbackProbeTargets(map[string]string{
			llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
		}, []string{"acct-probe-a", "acct-probe-b"})
		require.Len(t, targets, 2)
		assert.Equal(t, "openai", targets[0].provider)
		assert.Equal(t, "gateway-key", targets[0].cfg[cfgKeyAPIKey])
		assert.Equal(t, "https://gateway.example/openai", targets[0].cfg[cfgKeyAPIEndpoint])
		require.Error(t, targets[1].err)
		assert.Contains(t, targets[1].err.Error(), "acct-probe-b")
		assert.False(t, probeOne(t.Context(), targets[1]).OK)
	})

	t.Run("new fallback is probed with only its own fields", func(t *testing.T) {
		targets := fallbackProbeTargets(map[string]string{
			"llm_provider": "custom", "llm_model_name": "Qwen/A", "llm_provider_api_key": "primary-key",
			"llm_extra_headers":    `{"X-Tenant":"primary"}`,
			llmFallbackProviderKey: "openai", llmFallbackModelKey: "gpt-5.6-terra", "llm_fallback_api_key": "fallback-key",
		}, nil)
		require.Len(t, targets, 1)
		assert.Equal(t, map[string]string{
			cfgKeyProvider: "openai", cfgKeyModel: "gpt-5.6-terra", cfgKeyAPIKey: "fallback-key",
		}, targets[0].cfg)
	})

	t.Run("no fallback configured", func(t *testing.T) {
		assert.Empty(t, fallbackProbeTargets(map[string]string{"llm_provider": "openai"}, nil))
	})
}

// Through the real retry entry point, with NO legacy fallback list: the
// configured fallback must still run. (A caught-in-e2e regression: the chain was
// only built when a legacy list existed, so a configured-only fallback never ran.)
func TestGenerateWithRetry_ConfiguredFallbackRunsWithoutLegacyList(t *testing.T) {
	const acct = "acct-fb-no-legacy"
	cfg := geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
	})
	delete(cfg, "llm_model_fallbacks")
	res := seedAccount(t, acct, cfg, openaiGateway)
	require.Empty(t, getLLMFallbackModelName(acct, "", "", true, res), "precondition: no legacy list anywhere")
	built := recordingSeam(t, map[string]llms.Model{"gpt-5.6-terra": &fakeLLMModel{response: "ok"}})

	_, md, err := generateLLMContentWithRetry(newCtxWith(ContextKeyModelTier, ModelTier("")),
		&fakeLLMModel{err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused")},
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")}, nil,
		"", "", acct, "", "", false, "", res)
	require.NoError(t, err)
	require.Len(t, *built, 1)
	assert.Equal(t, "openai", (*built)[0].provider)
	assert.Equal(t, "openai", md.ServedProvider)
	assert.Equal(t, "gpt-5.6-terra", md.ServedModel)
}

// optsRecordingModel is fakeLLMModel that also records the options it was called with.
type optsRecordingModel struct {
	fakeLLMModel
	opts llms.CallOptions
}

func (m *optsRecordingModel) GenerateContent(ctx context.Context, msgs []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	for _, o := range options {
		o(&m.opts)
	}
	return m.fakeLLMModel.GenerateContent(ctx, msgs, options...)
}

// The primary's output budget (Gemini: 65536) must not reach a configured
// fallback whose model accepts less — OpenAI rejects the whole call (caught in e2e).
func TestConfiguredFallbackGetsItsOwnMaxOutputTokens(t *testing.T) {
	const acct = "acct-fb-max-tokens"
	res := seedAccount(t, acct, geminiIntegration(map[string]string{
		llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
	}), openaiGateway)
	legacy := &optsRecordingModel{fakeLLMModel: fakeLLMModel{err: errors.New("provider rejected request")}}
	configured := &optsRecordingModel{fakeLLMModel: fakeLLMModel{response: "ok"}}
	recordingSeam(t, map[string]llms.Model{"gemini-3-flash-preview": legacy, "gpt-5.6-terra": configured})

	rc := newFallbackRC(acct, res)
	rc.options = []llms.CallOption{llms.WithMaxTokens(65536)}
	_, _, err := handleQuotaError(rc, fallbackChain(rc, "gemini-3-flash-preview"), false, fallbackCauseUnavailable)
	require.NoError(t, err)

	assert.Equal(t, 65536, legacy.opts.MaxTokens, "legacy same-provider fallbacks keep today's options")
	assert.Equal(t, DefaultMaxOutputTokensFloor, configured.opts.MaxTokens, "capped to the fallback model's own limit")

	assert.Len(t, withFallbackMaxTokens([]llms.CallOption{llms.WithMaxTokens(2048)}, acct, FallbackModel{Provider: "openai", Model: "gpt-5.6-terra"}), 1,
		"a lower cap already set by the caller is kept")
}

// Per-category configured fallbacks: a tier-tagged call uses its category's
// fallback when that category sets one, otherwise the integration's.
func TestConfiguredFallback_PerCategory(t *testing.T) {
	const acct = "acct-fb-tier"
	cfg := geminiIntegration(map[string]string{
		// integration-level: reference to the OpenAI gateway
		llmFallbackRefSourceKey: "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
		// reasoning's own fallback: a Google AI slot it owns
		"llm_fallback_provider_reasoning": "googleai", "llm_fallback_model_reasoning": "gemini-2.5-pro",
		"llm_fallback_api_key_reasoning": "reasoning-fb-key",
	})
	pinGlobalModel(t, "", "")
	seedDBConfig(t, acct, cfg)
	seedLLMIntegrations(t, acct, openaiGateway)
	resolve := func(tier ModelTier) *LLMConfigResolution {
		res, err := ResolveLLMConfig(newCtxWith(ContextKeyModelTier, tier), acct, "", "")
		require.NoError(t, err)
		return res
	}

	fb, err := configuredFallback(nil, acct, resolve(ModelTierReasoning))
	require.NoError(t, err)
	assert.Equal(t, [2]string{"googleai", "gemini-2.5-pro"}, [2]string{fb.Provider, fb.Model}, "reasoning uses its own fallback")
	assert.Equal(t, "db-fallback:reasoning", fb.Resolution.PinnedConfigSource)
	assert.Equal(t, "reasoning-fb-key", getLLMApiKey(acct, "googleai", "", false, fb.Resolution))

	fb, err = configuredFallback(nil, acct, resolve(ModelTierSummary))
	require.NoError(t, err)
	assert.Equal(t, [2]string{"openai", "gpt-5.6-terra"}, [2]string{fb.Provider, fb.Model}, "a category without its own falls back to the integration's")

	fb, err = configuredFallback(nil, acct, resolve(""))
	require.NoError(t, err)
	assert.Equal(t, "openai", fb.Provider, "an untagged call never uses a category's fallback")
}

// A category that overrides the provider (reasoning on Anthropic, global on Gemini)
// still reaches the integration's configured fallback through the real retry path.
func TestGenerateWithRetry_ProviderOverridingCategoryUsesConfiguredFallback(t *testing.T) {
	const acct = "acct-fb-tier-override"
	cfg := geminiIntegration(map[string]string{
		"llm_tier_provider_reasoning": "anthropic", "llm_tier_model_reasoning": "claude-sonnet-4-6",
		"llm_tier_api_key_reasoning": "anthropic-key",
		llmFallbackRefSourceKey:      "db:int-openai-gw", llmFallbackRefModelKey: "gpt-5.6-terra",
	})
	delete(cfg, "llm_model_fallbacks")
	pinGlobalModel(t, "", "")
	seedDBConfig(t, acct, cfg)
	seedLLMIntegrations(t, acct, openaiGateway)
	ctx := newCtxWith(ContextKeyModelTier, ModelTierReasoning)
	res, err := ResolveLLMConfig(ctx, acct, "", "")
	require.NoError(t, err)
	require.Equal(t, "anthropic", res.Provider, "precondition: the reasoning call's primary is the category override")
	built := recordingSeam(t, map[string]llms.Model{"gpt-5.6-terra": &fakeLLMModel{response: "ok"}})

	_, md, err := generateLLMContentWithRetry(ctx,
		&fakeLLMModel{err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused")},
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")}, nil,
		"", "", acct, "", "", false, "", res)
	require.NoError(t, err)
	require.Len(t, *built, 1)
	assert.Equal(t, "openai", (*built)[0].provider)
	assert.Equal(t, "gateway-key", getLLMApiKey(acct, "openai", "", false, (*built)[0].res), "the fallback never sees the Anthropic key")
	assert.Equal(t, "openai", md.ServedProvider)
}

func TestFallbackProbeTargets_PerCategory(t *testing.T) {
	targets := fallbackProbeTargets(map[string]string{
		"llm_provider": "googleai", "llm_model_name": "gemini-3.1-pro-preview",
		llmFallbackProviderKey: "openai", llmFallbackModelKey: "gpt-5.6-terra", "llm_fallback_api_key": "global-fb-key",
		"llm_fallback_provider_summary": "custom", "llm_fallback_model_summary": "Qwen/B",
		"llm_fallback_api_key_summary": "summary-fb-key", "llm_fallback_api_endpoint_summary": "https://vllm.example/v1",
	}, nil)
	require.Len(t, targets, 2)
	bySource := map[string]probeTarget{}
	for _, tg := range targets {
		bySource[tg.source] = tg
	}
	assert.Equal(t, "global-fb-key", bySource["fallback"].cfg[cfgKeyAPIKey])
	s := bySource["tier-summary-fallback"]
	assert.Equal(t, "custom", s.provider)
	assert.Equal(t, "summary-fb-key", s.cfg[cfgKeyAPIKey])
	assert.Equal(t, "https://vllm.example/v1", s.cfg[cfgKeyAPIEndpoint])
}

// Which slots can be referenced as a fallback is decided in two services: this
// picker listing, and api-server's validateFallbackRef at save
// (TestValidateConfiguredFallback, "category without its own model"). They cannot
// share code, so both tests pin the same rule: a category slot is referenceable
// only when it names its own model. Change one side, change both.
func TestGetAllConfiguredModels_CategoryWithoutOwnModelIsNotReferenceable(t *testing.T) {
	origProvider, origModel := config.Config.LlmProvider, config.Config.LlmModel
	t.Cleanup(func() { config.Config.LlmProvider, config.Config.LlmModel = origProvider, origModel })
	config.Config.LlmProvider, config.Config.LlmModel = "", ""

	seedLLMIntegrations(t, "acct-1", llmIntegration{Id: "int-1", Name: "gateway-openai", Config: map[string]string{
		"llm_provider": "openai", "llm_model_name": "gpt-5.6-terra",
		"llm_tier_model_reasoning":  "gpt-5.6-sol",
		"llm_tier_provider_summary": "openai", // provider only: inherits the primary's model
	}})

	got, err := GetAllConfiguredModels("acct-1")
	require.NoError(t, err)
	sources := map[string]bool{}
	for _, r := range got {
		sources[r.Source] = true
	}
	assert.True(t, sources["db:int-1:tier:reasoning"], "a category with its own model is referenceable")
	assert.False(t, sources["db:int-1:tier:summary"], "a category without its own model is not")
}
