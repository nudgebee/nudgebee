package core

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

// The resolver is the whole surface area of the per-provider knob — every
// runtime decision to fire the TTFT timeout goes through it. These tests
// exercise the precedence contract in isolation, without spinning up an LLM.

func TestGetLLMTTFTTimeout_EmptyProvider_ReturnsDisabled(t *testing.T) {
	enabled, seconds := getLLMTTFTTimeout("")
	assert.False(t, enabled)
	assert.Equal(t, 0, seconds)
}

func TestGetLLMTTFTTimeout_ProviderNotEnabled_ReturnsDisabled(t *testing.T) {
	// Explicit "false" defends against an ambient shell/CI env that already sets
	// this key to "true". A large global seconds must still not activate the
	// timeout for a provider whose ENABLED key is not true — the whole point of
	// per-provider enable.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "false")
	orig := config.Config.LlmProviderTTFTTimeoutSeconds
	config.Config.LlmProviderTTFTTimeoutSeconds = 120
	t.Cleanup(func() { config.Config.LlmProviderTTFTTimeoutSeconds = orig })

	enabled, seconds := getLLMTTFTTimeout("googleai")
	assert.False(t, enabled)
	assert.Equal(t, 0, seconds)
}

func TestGetLLMTTFTTimeout_ProviderEnabled_UsesGlobalSecondsByDefault(t *testing.T) {
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "true")
	// Clear any ambient per-provider SECONDS override so the global fallback wins.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_GOOGLEAI", "")
	orig := config.Config.LlmProviderTTFTTimeoutSeconds
	config.Config.LlmProviderTTFTTimeoutSeconds = 30
	t.Cleanup(func() { config.Config.LlmProviderTTFTTimeoutSeconds = orig })

	enabled, seconds := getLLMTTFTTimeout("googleai")
	assert.True(t, enabled)
	assert.Equal(t, 30, seconds)
}

func TestGetLLMTTFTTimeout_ProviderEnabled_PerProviderSecondsOverridesGlobal(t *testing.T) {
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_HUGGINGFACE", "120")
	orig := config.Config.LlmProviderTTFTTimeoutSeconds
	config.Config.LlmProviderTTFTTimeoutSeconds = 30
	t.Cleanup(func() { config.Config.LlmProviderTTFTTimeoutSeconds = orig })

	enabled, seconds := getLLMTTFTTimeout("huggingface")
	assert.True(t, enabled)
	assert.Equal(t, 120, seconds, "per-provider seconds override must win over the global default")
}

func TestGetLLMTTFTTimeout_ProviderNameIsCaseInsensitive(t *testing.T) {
	// Env-var names are case-sensitive per POSIX; our lookup lower-cases the
	// provider so "HuggingFace"/"huggingface"/"HUGGINGFACE" all resolve to the
	// same key. Ops sees one canonical env var, not three.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_HUGGINGFACE", "90")

	for _, name := range []string{"huggingface", "HuggingFace", "HUGGINGFACE"} {
		enabled, seconds := getLLMTTFTTimeout(name)
		assert.True(t, enabled, "provider %q should resolve to enabled", name)
		assert.Equal(t, 90, seconds, "provider %q should resolve to per-provider seconds", name)
	}
}

// A per-provider ENABLED for one provider must not leak into another. Sanity
// check because AutomaticEnv keys are global — if we ever accidentally read
// the "any provider is enabled" key we'd fire the timeout on the wrong calls.
func TestGetLLMTTFTTimeout_EnableIsIsolatedPerProvider(t *testing.T) {
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_HUGGINGFACE", "true")
	// Explicit false on the other provider defends against an ambient env that
	// also has GOOGLEAI enabled — the isolation assertion would silently pass
	// through the wrong branch otherwise.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "false")

	enabledHF, _ := getLLMTTFTTimeout("huggingface")
	enabledGoogle, _ := getLLMTTFTTimeout("googleai")

	assert.True(t, enabledHF, "huggingface should be enabled")
	assert.False(t, enabledGoogle, "googleai must NOT be enabled — no cross-provider leakage")
}
