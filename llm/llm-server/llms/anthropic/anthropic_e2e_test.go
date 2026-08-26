//go:build e2e

package anthropic

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

func getTestAnthropicLLM(t *testing.T, model string) *LLM {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("LLM_PROVIDER_API_KEY")
	}
	if apiKey == "" {
		t.Skip("Skipping live Anthropic e2e test: ANTHROPIC_API_KEY / LLM_PROVIDER_API_KEY not set")
	}

	opts := []Option{
		WithToken(apiKey),
		WithModel(model),
	}
	if baseURL := os.Getenv("LLM_PROVIDER_API_ENDPOINT"); baseURL != "" {
		opts = append(opts, WithBaseURL(baseURL))
	}

	llm, err := New(opts...)
	require.NoError(t, err)
	return llm
}

func TestAnthropicE2E_ReasoningModelTemperatureStripping(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	llm := getTestAnthropicLLM(t, "claude-sonnet-5")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Respond with exactly the word 'PONG'."},
			},
		},
	}

	// Should not fail with 400 temperature error on claude-sonnet-5
	resp, err := llm.GenerateContent(ctx, messages,
		llms.WithTemperature(0.0),
		llms.WithMaxTokens(64),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	assert.Contains(t, strings.ToUpper(resp.Choices[0].Content), "PONG")
}

func TestAnthropicE2E_Claude37_BudgetTokens(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	llm := getTestAnthropicLLM(t, "claude-3-7-sonnet-20250219")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Explain step by step how Paxos consensus achieves agreement in a distributed network."},
			},
		},
	}

	callOpts := []llms.CallOption{
		llms.WithMaxTokens(4096),
		llms.WithTemperature(0.0), // Should be stripped automatically without 400
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
	assert.Greater(t, choice.GenerationInfo["input_tokens"], 0)
	assert.Greater(t, choice.GenerationInfo["output_tokens"], 0)
	t.Logf("Claude 3.7 tokens - Input: %v, Output: %v, Thinking: %v",
		choice.GenerationInfo["input_tokens"],
		choice.GenerationInfo["output_tokens"],
		choice.GenerationInfo["ThinkingTokens"])
}

func TestAnthropicE2E_Claude46_Effort(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	llm := getTestAnthropicLLM(t, "claude-sonnet-4-6")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Solve this riddle: The more you take, the more you leave behind. What am I?"},
			},
		},
	}

	callOpts := []llms.CallOption{
		llms.WithMaxTokens(2048),
		llms.WithTemperature(0.2), // Should be stripped automatically when thinking is active
		llms.WithMetadata(map[string]any{
			"ThinkingLevel": "low",
		}),
	}

	resp, err := llm.GenerateContent(ctx, messages, callOpts...)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)

	choice := resp.Choices[0]
	assert.NotEmpty(t, choice.Content)
	t.Logf("Claude 4.6 tokens - Input: %v, Output: %v, Thinking: %v",
		choice.GenerationInfo["input_tokens"],
		choice.GenerationInfo["output_tokens"],
		choice.GenerationInfo["ThinkingTokens"])
}

func TestAnthropicE2E_Streaming(t *testing.T) {
	llm := getTestAnthropicLLM(t, "claude-3-5-sonnet-20241022")

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
				llms.TextContent{Text: "Count from 1 to 5, one number per line."},
			},
		},
	}

	resp, err := llm.GenerateContent(ctx, messages,
		llms.WithStreamingFunc(streamingFunc),
		llms.WithMaxTokens(256),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	assert.NotEmpty(t, streamChunks)
	assert.Equal(t, resp.Choices[0].Content, strings.Join(streamChunks, ""))
}

func TestAnthropicE2E_MultiTurnWithTools(t *testing.T) {
	llm := getTestAnthropicLLM(t, "claude-3-5-sonnet-20241022")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	tools := []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "get_weather",
				Description: "Get the current weather for a city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "City and state, e.g. San Francisco, CA",
						},
					},
					"required": []string{"location"},
				},
			},
		},
	}

	// Turn 1: User asks for weather -> Claude calls get_weather tool
	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "What is the weather in Seattle?"},
			},
		},
	}

	resp1, err := llm.GenerateContent(ctx, messages,
		llms.WithTools(tools),
		llms.WithMaxTokens(1024),
	)
	require.NoError(t, err)
	require.NotNil(t, resp1)
	require.NotEmpty(t, resp1.Choices)

	// Tool call must be populated
	require.NotEmpty(t, resp1.Choices[0].ToolCalls, "Native tool call must be returned in ToolCalls")
	toolCall := resp1.Choices[0].ToolCalls[0]
	assert.Equal(t, "get_weather", toolCall.FunctionCall.Name)

	// Turn 2: Provide tool result back to Claude
	messages = append(messages, llms.MessageContent{
		Role: llms.ChatMessageTypeAI,
		Parts: []llms.ContentPart{
			toolCall,
		},
	})
	messages = append(messages, llms.MessageContent{
		Role: llms.ChatMessageTypeTool,
		Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: toolCall.ID,
				Name:       toolCall.FunctionCall.Name,
				Content:    `{"temperature": "65F", "condition": "Partly Cloudy"}`,
			},
		},
	})

	resp2, err := llm.GenerateContent(ctx, messages,
		llms.WithTools(tools),
		llms.WithMaxTokens(1024),
	)
	require.NoError(t, err)
	require.NotNil(t, resp2)
	require.NotEmpty(t, resp2.Choices)
	assert.Contains(t, strings.ToLower(resp2.Choices[0].Content), "65")
}
