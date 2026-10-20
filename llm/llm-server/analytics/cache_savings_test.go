package analytics

import (
	"testing"

	"nudgebee/llm/agents/core"
	llmruntime "nudgebee/llm/llms/runtime"

	"github.com/stretchr/testify/assert"
)

// Claude Sonnet 5 rates: $2 input, $10 output per million tokens.
var sonnet5Pricing = map[string]llmruntime.ModelPricing{
	"anthropic:claude-sonnet-5": {CostPerMillionInput: 2, CostPerMillionOutput: 10},
	"googleai:gemini-3.8-flash": {CostPerMillionInput: 0.75, CostPerMillionOutput: 3.75},
}

// Anthropic stores cache writes outside input_tokens. Without caching those
// tokens would still have been sent, so they belong in the "without cache" cost.
func TestCacheSavingsWithoutCacheIncludesAnthropicCacheWrites(t *testing.T) {
	records := []core.TokenUsageDetailedRecord{{
		LLMProvider: "anthropic", LLMModel: "claude-sonnet-5",
		InputTokens: 1_000_000, CachedInputTokens: 900_000, CacheCreationTokens: 500_000,
		OutputTokens: 10_000,
	}}
	info := calculateCacheSavings(records, 1.50, sonnet5Pricing)
	// (1,000,000 + 500,000) × $2/M + 10,000 × $10/M = $3.00 + $0.10
	assert.InDelta(t, 3.10, info.EstimatedCostWithoutCacheUsd, 1e-9)
	assert.InDelta(t, 1.60, info.CostSavingsUsd, 1e-9)
}

// Google AI already counts every prompt token in input; its cache writes must
// not be added a second time.
func TestCacheSavingsWithoutCacheUnchangedForGoogleAI(t *testing.T) {
	records := []core.TokenUsageDetailedRecord{{
		LLMProvider: "googleai", LLMModel: "gemini-3.8-flash",
		InputTokens: 1_000_000, CachedInputTokens: 800_000, CacheCreationTokens: 200_000,
	}}
	info := calculateCacheSavings(records, 0.30, sonnet5Pricing)
	assert.InDelta(t, 0.75, info.EstimatedCostWithoutCacheUsd, 1e-9)
}
