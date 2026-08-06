package agents

import (
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

func TestForwardedLLMConfigToMap(t *testing.T) {
	full := &core.ForwardedLLMConfig{
		Provider:    "googleai",
		Model:       "gemini-3-flash-preview",
		ApiKey:      "secret-key",
		ApiEndpoint: "https://example.com",
		ApiVersion:  "2024-01",
		ApiType:     "azure",
		Region:      "us-west-2",
	}
	m := forwardedLLMConfigToMap(full)
	assert.Equal(t, "googleai", m["provider"])
	assert.Equal(t, "gemini-3-flash-preview", m["model"])
	assert.Equal(t, "secret-key", m["api_key"])
	assert.Equal(t, "https://example.com", m["endpoint"])
	assert.Equal(t, "2024-01", m["api_version"])
	assert.Equal(t, "azure", m["api_type"])
	assert.Equal(t, "us-west-2", m["region"])

	// Empty optional fields are omitted; provider is always present.
	sparse := forwardedLLMConfigToMap(&core.ForwardedLLMConfig{Provider: "openai", ApiKey: "k"})
	assert.Equal(t, "openai", sparse["provider"])
	assert.Equal(t, "k", sparse["api_key"])
	_, hasEndpoint := sparse["endpoint"]
	assert.False(t, hasEndpoint, "empty endpoint must be omitted")
	_, hasModel := sparse["model"]
	assert.False(t, hasModel, "empty model must be omitted")
}

func TestForwardedLLMConfigToEnv(t *testing.T) {
	env := forwardedLLMConfigToEnv(&core.ForwardedLLMConfig{
		Provider: "googleai",
		Model:    "gemini-3-flash-preview",
		ApiKey:   "secret-key",
		Region:   "us-west-2",
	})
	got := map[string]string{}
	for _, e := range env {
		// Forwarded config is plaintext per-request env, never SecretKeyRef.
		assert.Nil(t, e.ValueFrom, "%s must be an inline value, not ValueFrom", e.Name)
		got[e.Name] = e.Value
	}
	assert.Equal(t, "googleai", got["LLM_PROVIDER"])
	assert.Equal(t, "gemini-3-flash-preview", got["LLM_MODEL_NAME"])
	assert.Equal(t, "secret-key", got["LLM_PROVIDER_API_KEY"])
	assert.Equal(t, "us-west-2", got["LLM_PROVIDER_REGION"])
	// Credential vars are emitted even when empty — see below.
	endpoint, hasEndpoint := got["LLM_PROVIDER_API_ENDPOINT"]
	assert.True(t, hasEndpoint, "credential vars must always be emitted")
	assert.Empty(t, endpoint)
}

// This override always sets LLM_PROVIDER, so every credential var must be
// emitted with it — otherwise the secret's credential for the *previous*
// provider survives underneath and gets spliced onto the forwarded one.
func TestForwardedLLMConfigToEnv_KeylessProviderBlanksCredentials(t *testing.T) {
	env := forwardedLLMConfigToEnv(&core.ForwardedLLMConfig{
		Provider: "bedrock",
		Model:    "us.meta.llama4-maverick-17b-instruct-v1:0",
	})
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	assert.Equal(t, "bedrock", got["LLM_PROVIDER"])
	for _, name := range []string{
		"LLM_PROVIDER_API_KEY",
		"LLM_PROVIDER_API_ENDPOINT",
		"LLM_PROVIDER_API_VERSION",
		"LLM_PROVIDER_API_TYPE",
		"LLM_PROVIDER_REGION",
	} {
		val, ok := got[name]
		assert.True(t, ok, "%s must be emitted to blank the secret's value", name)
		assert.Empty(t, val)
	}
}

// An unresolved model is not a credential: falling through to the pod's default
// beats forcing it blank.
func TestForwardedLLMConfigToEnv_EmptyModelFallsThrough(t *testing.T) {
	env := forwardedLLMConfigToEnv(&core.ForwardedLLMConfig{Provider: "bedrock"})
	for _, e := range env {
		assert.NotEqual(t, "LLM_MODEL_NAME", e.Name, "empty model must not override the pod default")
	}
}
