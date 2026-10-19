package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPromptTokens(t *testing.T) {
	// Anthropic and Bedrock keep cache writes outside input: the prompt is both.
	assert.Equal(t, 2_279_723, PromptTokens("anthropic", 1_881_294, 398_429))
	assert.Equal(t, 1_500, PromptTokens("bedrock", 1_000, 500))
	assert.Equal(t, 1_500, PromptTokens(" Anthropic ", 1_000, 500))
	// Google AI counts explicit-cache writes inside input already; OpenAI has no writes.
	assert.Equal(t, 1_000, PromptTokens("googleai", 1_000, 500))
	assert.Equal(t, 1_000, PromptTokens("openai", 1_000, 0))
	assert.Equal(t, 1_000, PromptTokens("custom", 1_000, 500))
}

// A run's budget counts what the provider processed afresh: cache reads do
// not count, cache writes do (outside input for anthropic/bedrock, already
// inside it for the rest), and so do output and thinking.
func TestRunBudgetTokens(t *testing.T) {
	call := TokenInfo{InputTokens: 20000, CacheReadTokens: 15000, CacheCreationTokens: 1000, OutputTokens: 300, ThinkingTokens: 200}
	assert.Equal(t, 5000+1000+300+200, RunBudgetTokens("anthropic", call))
	assert.Equal(t, 5000+1000+300+200, RunBudgetTokens(" Bedrock ", call))
	assert.Equal(t, 5000+300+200, RunBudgetTokens("google", call))
	assert.Equal(t, 5000+300+200, RunBudgetTokens("openai", call))
	assert.Equal(t, 300, RunBudgetTokens("google", TokenInfo{InputTokens: 10, CacheReadTokens: 20, OutputTokens: 300}), "never below zero")

	// 20 calls of 20k input, 75% cached: 110k, inside a 300k budget
	// that input + output (~410k) used to exceed.
	run := 0
	for range 20 {
		run += RunBudgetTokens("google", TokenInfo{InputTokens: 20000, CacheReadTokens: 15000, OutputTokens: 500})
	}
	assert.Equal(t, 110000, run)
}
