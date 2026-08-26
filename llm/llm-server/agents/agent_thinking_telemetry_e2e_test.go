//go:build e2e

package agents

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

func TestThinkingTelemetry_E2E(t *testing.T) {
	accountID := os.Getenv("TEST_ACCOUNT")
	if accountID == "" {
		t.Skip("Skipping e2e telemetry test: TEST_ACCOUNT not set")
	}

	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	reqCtx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountID})

	prompt := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Explain how B-tree indexing optimizes database queries."},
			},
		},
	}

	callOpts := []llms.CallOption{
		llms.WithMaxTokens(2048),
		core.WithThinkingLevel("low"),
	}

	resp, err := core.GenerateAndTrackLLMContent(reqCtx, os.Getenv("TEST_USER"), accountID, "e2e_thinking_test", "", "investigation", false, prompt, true, callOpts...)
	if err != nil {
		t.Skipf("Live LLM call skipped (provider unavailable or unconfigured): %v", err)
	}

	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	choice := resp.Choices[0]
	assert.NotEmpty(t, choice.Content)

	t.Logf("Recorded token info - Input: %v, Output: %v, Thinking: %v, CacheRead: %v",
		choice.GenerationInfo["input_tokens"],
		choice.GenerationInfo["output_tokens"],
		choice.GenerationInfo["ThinkingTokens"],
		choice.GenerationInfo["CacheReadInputTokens"])
}
