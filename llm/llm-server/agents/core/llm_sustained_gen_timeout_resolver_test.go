package core

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

// The resolver is the whole surface area of the per-provider knob — every
// runtime decision to fire the sustained-generation timeout goes through it.
// These tests exercise the precedence contract in isolation, without spinning
// up an LLM. Mirrors llm_ttft_timeout_resolver_test.go — same enable/override
// shape, different key prefix.

func TestGetLLMSustainedGenTimeout_EmptyProvider_ReturnsDisabled(t *testing.T) {
	enabled, seconds := getLLMSustainedGenTimeout("")
	assert.False(t, enabled)
	assert.Equal(t, 0, seconds)
}

func TestGetLLMSustainedGenTimeout_ProviderNotEnabled_ReturnsDisabled(t *testing.T) {
	// Explicit "false" defends against an ambient shell/CI env that already sets
	// this key to "true". A large global seconds must still not activate the
	// timeout for a provider whose ENABLED key is not true — the whole point of
	// per-provider enable.
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI", "false")
	orig := config.Config.LlmProviderSustainedGenTimeoutSeconds
	config.Config.LlmProviderSustainedGenTimeoutSeconds = 120
	t.Cleanup(func() { config.Config.LlmProviderSustainedGenTimeoutSeconds = orig })

	enabled, seconds := getLLMSustainedGenTimeout("googleai")
	assert.False(t, enabled)
	assert.Equal(t, 0, seconds)
}

func TestGetLLMSustainedGenTimeout_ProviderEnabled_UsesGlobalSecondsByDefault(t *testing.T) {
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI", "true")
	// Clear any ambient per-provider SECONDS override so the global fallback wins.
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_SECONDS_GOOGLEAI", "")
	orig := config.Config.LlmProviderSustainedGenTimeoutSeconds
	config.Config.LlmProviderSustainedGenTimeoutSeconds = 60
	t.Cleanup(func() { config.Config.LlmProviderSustainedGenTimeoutSeconds = orig })

	enabled, seconds := getLLMSustainedGenTimeout("googleai")
	assert.True(t, enabled)
	assert.Equal(t, 60, seconds)
}

func TestGetLLMSustainedGenTimeout_ProviderEnabled_PerProviderSecondsOverridesGlobal(t *testing.T) {
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_SECONDS_HUGGINGFACE", "45")
	orig := config.Config.LlmProviderSustainedGenTimeoutSeconds
	config.Config.LlmProviderSustainedGenTimeoutSeconds = 60
	t.Cleanup(func() { config.Config.LlmProviderSustainedGenTimeoutSeconds = orig })

	enabled, seconds := getLLMSustainedGenTimeout("huggingface")
	assert.True(t, enabled)
	assert.Equal(t, 45, seconds, "per-provider seconds override must win over the global default")
}

func TestGetLLMSustainedGenTimeout_ProviderNameIsCaseInsensitive(t *testing.T) {
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_SECONDS_HUGGINGFACE", "90")

	for _, name := range []string{"huggingface", "HuggingFace", "HUGGINGFACE"} {
		enabled, seconds := getLLMSustainedGenTimeout(name)
		assert.True(t, enabled, "provider %q should resolve to enabled", name)
		assert.Equal(t, 90, seconds, "provider %q should resolve to per-provider seconds", name)
	}
}

// A per-provider ENABLED for one provider must not leak into another, and must
// not be confused with the separate TTFT enable key on the same provider.
func TestGetLLMSustainedGenTimeout_EnableIsIsolatedPerProviderAndFromTTFT(t *testing.T) {
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI", "false")
	// TTFT enabled for googleai must not cause sustained-gen to read as enabled.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "true")

	enabledHF, _ := getLLMSustainedGenTimeout("huggingface")
	enabledGoogle, _ := getLLMSustainedGenTimeout("googleai")

	assert.True(t, enabledHF, "huggingface should be enabled")
	assert.False(t, enabledGoogle, "googleai must NOT be enabled via sustained-gen key — no cross-provider or cross-mechanism leakage")
}
