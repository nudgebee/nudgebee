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
