package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptTokens(t *testing.T) {
	// Anthropic and Bedrock keep cache writes outside input: the prompt is both.
	assert.Equal(t, 2_279_723, promptTokens("anthropic", 1_881_294, 398_429))
	assert.Equal(t, 1_500, promptTokens("bedrock", 1_000, 500))
	assert.Equal(t, 1_500, promptTokens(" Anthropic ", 1_000, 500))
	// Google AI counts explicit-cache writes inside input already; OpenAI has no writes.
	assert.Equal(t, 1_000, promptTokens("googleai", 1_000, 500))
	assert.Equal(t, 1_000, promptTokens("openai", 1_000, 0))
	assert.Equal(t, 1_000, promptTokens("custom", 1_000, 500))
}

// The test-env Claude run (conversation 7b2e7d4c, claude-sonnet-5) showed
// 99.96% cached because 398K cache-write tokens were left out of the prompt.
func TestCacheSavingsHitRateCountsAnthropicCacheWrites(t *testing.T) {
	records := []TokenUsageDetailedRecord{{
		LLMProvider: "anthropic", LLMModel: "claude-sonnet-5",
		InputTokens: 1_881_294, CachedInputTokens: 1_880_596, CacheCreationTokens: 398_429,
	}}
	info := calculateCacheSavings(records, 0, nil)
	require.NotNil(t, info.CacheHitRatePercentage)
	assert.InDelta(t, 82.49, *info.CacheHitRatePercentage, 0.01)
}

func TestCacheSavingsHitRateUnchangedForGoogleAI(t *testing.T) {
	records := []TokenUsageDetailedRecord{{
		LLMProvider: "googleai", LLMModel: "gemini-3.8-flash",
		InputTokens: 1_000, CachedInputTokens: 800, CacheCreationTokens: 200,
	}}
	info := calculateCacheSavings(records, 0, nil)
	require.NotNil(t, info.CacheHitRatePercentage)
	assert.InDelta(t, 80.0, *info.CacheHitRatePercentage, 0.001)
}
