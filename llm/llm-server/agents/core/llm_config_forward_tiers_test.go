package core

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ResolveLLMConfigForForwarding must include the ModelTier resolution so the
// workspace can run its internal roles (fixer/router/review) on the tier
// models the environment/tenant already configured — reusing the existing
// layered config instead of a parallel per-pod env surface.
func TestResolveLLMConfigForForwarding_IncludesTiers(t *testing.T) {
	pinGlobalModel(t, "googleai", "run-pro-model")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "test-key"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	setEnvKey(t, "llm_tier_model_retrieval", "cheap-exec-model")
	setEnvKey(t, "llm_tier_provider_retrieval", "googleai")
	setEnvKey(t, "llm_tier_model_summary", "cheap-summary-model")
	setEnvKey(t, "llm_tier_provider_summary", "googleai")
	// reasoning resolves to the run model — must be omitted from the forwarded
	// tiers (nothing to override).
	setEnvKey(t, "llm_tier_model_reasoning", "run-pro-model")
	setEnvKey(t, "llm_tier_provider_reasoning", "googleai")

	seedDBConfig(t, "acct-fwd-tiers", map[string]string{})

	fwd, err := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-fwd-tiers", "agent_code_2", "")
	assert.NoError(t, err)
	require.NotNil(t, fwd)
	assert.Equal(t, "run-pro-model", fwd.Model)
	assert.Equal(t, map[string]string{
		"retrieval": "cheap-exec-model",
		"summary":   "cheap-summary-model",
	}, fwd.Tiers)
}

// A keyless provider must still be forwarded. Bedrock authenticates through the
// AWS credential chain, so requiring an API key here dropped the provider+model
// llm-server itself runs on and left the pod on its own built-in default.
func TestResolveLLMConfigForForwarding_KeylessProviderStillForwarded(t *testing.T) {
	pinGlobalModel(t, "bedrock", "us.meta.llama4-maverick-17b-instruct-v1:0")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = ""
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	seedDBConfig(t, "acct-fwd-keyless", map[string]string{})

	fwd, err := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-fwd-keyless", "agent_code_2", "")
	assert.NoError(t, err)
	require.NotNil(t, fwd, "a resolved provider must be forwarded even with no API key")
	assert.Equal(t, "bedrock", fwd.Provider)
	assert.Equal(t, "us.meta.llama4-maverick-17b-instruct-v1:0", fwd.Model)
	assert.Empty(t, fwd.ApiKey)
}

// Nothing resolves a provider — nothing is forwarded, so the pod keeps using
// its own global LLM_* env. Callers treat a nil config and an error the same
// way (omit the block), which is what keeps this a graceful degrade.
func TestResolveLLMConfigForForwarding_NoProviderForwardsNothing(t *testing.T) {
	pinGlobalModel(t, "", "")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = ""
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	seedDBConfig(t, "acct-fwd-noprovider", map[string]string{})

	fwd, _ := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-fwd-noprovider", "agent_code_2", "")
	assert.Nil(t, fwd)
}

// A tier that resolves on a DIFFERENT provider must be skipped: the forwarded
// credentials belong to the run model's provider.
func TestResolveLLMConfigForForwarding_SkipsCrossProviderTier(t *testing.T) {
	pinGlobalModel(t, "googleai", "run-pro-model")
	prevKey := config.Config.LlmProviderApiKey
	config.Config.LlmProviderApiKey = "test-key"
	t.Cleanup(func() { config.Config.LlmProviderApiKey = prevKey })

	setEnvKey(t, "llm_tier_model_retrieval", "claude-haiku")
	setEnvKey(t, "llm_tier_provider_retrieval", "bedrock")

	seedDBConfig(t, "acct-fwd-xprov", map[string]string{})

	fwd, err := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-fwd-xprov", "agent_code_2", "")
	assert.NoError(t, err)
	require.NotNil(t, fwd)
	assert.Empty(t, fwd.Tiers, "cross-provider tier must not be forwarded")
}
