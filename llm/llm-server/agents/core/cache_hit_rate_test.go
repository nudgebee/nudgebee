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
