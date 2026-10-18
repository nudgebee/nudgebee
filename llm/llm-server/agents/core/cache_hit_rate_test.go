package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
