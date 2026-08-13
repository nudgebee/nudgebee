package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface checks — if any of the three tools stops implementing
// NBToolPromptProvider we want a build error, not a silent regression in the
// delegate brief (the guidance would just quietly vanish for that tool).
var (
	_ core.NBToolPromptProvider = HelmExecuteTool{}
	_ core.NBToolPromptProvider = RedisExecuteTool{}
	_ core.NBToolPromptProvider = RabbitExecuteTool{}
)

func TestHelmExecuteTool_ToolPromptCarriesGuidance(t *testing.T) {
	lines := HelmExecuteTool{}.ToolPrompt()
	require.NotEmpty(t, lines, "helm ToolPrompt must not be empty — delegates need the how-to")
	joined := joinLines(lines)
	assert.Contains(t, joined, "namespace",
		"helm guidance must carry the namespace-awareness rule (the agent's core safety point)")
	assert.Contains(t, joined, "helm list",
		"helm guidance must retain at least one worked example so the LLM has a concrete pattern")
}

func TestRedisExecuteTool_ToolPromptCarriesGuidance(t *testing.T) {
	lines := RedisExecuteTool{}.ToolPrompt()
	require.NotEmpty(t, lines, "redis ToolPrompt must not be empty")
	joined := joinLines(lines)
	assert.Contains(t, joined, "SCAN",
		"redis guidance must carry the SCAN-vs-KEYS-* safety rule")
	assert.Contains(t, joined, "read-only",
		"redis guidance must retain the safe-operation preference")
}

func TestRabbitExecuteTool_ToolPromptCarriesGuidance(t *testing.T) {
	lines := RabbitExecuteTool{}.ToolPrompt()
	require.NotEmpty(t, lines, "rabbit ToolPrompt must not be empty")
	joined := joinLines(lines)
	// Both invocation shapes must be represented — this is the whole point of the
	// two-manual structure the agent's prompt captures.
	assert.Contains(t, joined, "rabbitmqadmin",
		"rabbit guidance must cover the rabbitmqadmin CLI shape")
	assert.Contains(t, joined, "rabbitmq-api",
		"rabbit guidance must cover the HTTP Management API shim shape")
	assert.Contains(t, joined, "no host or port",
		"rabbit guidance must retain the credential-injection safety note")
}

func joinLines(lines []string) string {
	var b []byte
	for _, l := range lines {
		b = append(b, l...)
		b = append(b, '\n')
	}
	return string(b)
}
