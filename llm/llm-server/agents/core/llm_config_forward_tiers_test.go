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
