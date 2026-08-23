package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloneWithLLMOverride(t *testing.T) {
	base := &Config{}
	base.LLM.Provider = "googleai"
	base.LLM.Model = "gemini-default"
	base.LLM.ApiKey = "env-key"
	base.LLM.Region = "us-west-2"

	t.Run("overlays non-empty fields and inherits the rest", func(t *testing.T) {
		got := base.CloneWithLLMOverride(LLMOverride{
			Model:  "tenant-model",
			ApiKey: "tenant-key",
		})
		assert.Equal(t, "googleai", got.LLM.Provider, "inherited")
		assert.Equal(t, "tenant-model", got.LLM.Model, "overridden")
		assert.Equal(t, "tenant-key", got.LLM.ApiKey, "overridden")
		assert.Equal(t, "us-west-2", got.LLM.Region, "inherited")
	})

	t.Run("does not mutate the receiver", func(t *testing.T) {
		_ = base.CloneWithLLMOverride(LLMOverride{Provider: "openai", ApiKey: "other"})
		assert.Equal(t, "googleai", base.LLM.Provider)
		assert.Equal(t, "env-key", base.LLM.ApiKey)
	})

	t.Run("empty override is a faithful copy", func(t *testing.T) {
		got := base.CloneWithLLMOverride(LLMOverride{})
		assert.Equal(t, base.LLM, got.LLM)
	})

	t.Run("switching provider drops the startup credentials", func(t *testing.T) {
		// llm-server forwards a keyless Bedrock config; none of the googleai
		// startup values may survive under it.
		got := base.CloneWithLLMOverride(LLMOverride{
			Provider: "bedrock",
			Model:    "us.meta.llama4-maverick-17b-instruct-v1:0",
		})
		assert.Equal(t, "bedrock", got.LLM.Provider)
		assert.Equal(t, "us.meta.llama4-maverick-17b-instruct-v1:0", got.LLM.Model)
		assert.Empty(t, got.LLM.ApiKey, "googleai key must not carry over to bedrock")
		assert.Empty(t, got.LLM.Region, "startup region belongs to the old provider")
	})

	t.Run("same provider still layers", func(t *testing.T) {
		got := base.CloneWithLLMOverride(LLMOverride{Provider: "GoogleAI", Model: "tenant-model"})
		assert.Equal(t, "tenant-model", got.LLM.Model)
		assert.Equal(t, "env-key", got.LLM.ApiKey, "same provider keeps the startup key")
		assert.Equal(t, "us-west-2", got.LLM.Region)
	})
}
