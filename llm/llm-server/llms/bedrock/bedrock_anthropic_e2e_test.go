//go:build e2e

package bedrock

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/config"
)

func getTestBedrockLLM(t *testing.T, modelID string) *LLM {
	if os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
		t.Skip("Skipping live Bedrock e2e test: AWS_REGION / AWS_DEFAULT_REGION not set")
	}

	opts := []Option{
		WithModel(modelID),
	}

	llm, err := New(opts...)
	if err != nil {
		t.Skipf("Skipping live Bedrock e2e test (client init failed): %v", err)
	}
	return llm
}

func TestBedrockAnthropicE2E_Claude37_Thinking(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	modelID := "us.anthropic.claude-3-7-sonnet-20250219-v1:0"
	llm := getTestBedrockLLM(t, modelID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Explain how Raft consensus algorithm handles leader election split votes."},
			},
		},
	}

	callOpts := []llms.CallOption{
		llms.WithMaxTokens(4096),
		llms.WithMetadata(map[string]any{
			"ThinkingLevel": "medium",
		}),
	}

	resp, err := llm.GenerateContent(ctx, messages, callOpts...)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)

	choice := resp.Choices[0]
	assert.NotEmpty(t, choice.Content)
	t.Logf("Bedrock Claude 3.7 tokens - Input: %v, Output: %v, Thinking: %v",
		choice.GenerationInfo["input_tokens"],
		choice.GenerationInfo["output_tokens"],
		choice.GenerationInfo["ThinkingTokens"])
}

func TestBedrockAnthropicE2E_Streaming(t *testing.T) {
	modelID := "us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	llm := getTestBedrockLLM(t, modelID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var streamChunks []string
	streamingFunc := func(ctx context.Context, chunk []byte) error {
		streamChunks = append(streamChunks, string(chunk))
		return nil
	}

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Name three primary colors."},
			},
		},
	}

	resp, err := llm.GenerateContent(ctx, messages,
		llms.WithStreamingFunc(streamingFunc),
		llms.WithMaxTokens(128),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	assert.NotEmpty(t, streamChunks)
	assert.Equal(t, resp.Choices[0].Content, strings.Join(streamChunks, ""))
}
