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

// TestBedrockAnthropicE2E_PromptCaching sends two steps of a tool loop with the
// markers AnthropicCacheProvider places, and checks that step 1 writes the
// cache and step 2 reads it. Defaults to Claude Haiku 4.5, the cheapest Claude
// on Bedrock with prompt caching (4,096-token minimum prefix, so the system
// prompt is padded past it); override with BEDROCK_CACHE_TEST_MODEL.
// Step 2 streams, so both response parsers report cache usage.
func TestBedrockAnthropicE2E_PromptCaching(t *testing.T) {
	modelID := os.Getenv("BEDROCK_CACHE_TEST_MODEL")
	if modelID == "" {
		modelID = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
	}
	llm := getTestBedrockLLM(t, modelID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var sys strings.Builder
	sys.WriteString("You are a Kubernetes troubleshooting assistant. Answer in one short sentence.\n")
	// A unique run id keeps an earlier run's cache from turning step 1 into a read.
	sys.WriteString("Run " + time.Now().Format(time.RFC3339Nano) + "\n")
	for i := 0; i < 700; i++ {
		sys.WriteString("Reference rule: check pod events, container restarts, and resource limits before concluding.\n")
	}
	mark := &llms.CacheControl{Type: "ephemeral"}

	step1 := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.WithCacheControl(llms.TextContent{Text: sys.String()}, mark)}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.WithCacheControl(llms.TextContent{Text: "Why is pod api-0 restarting?"}, mark)}},
	}
	resp1, err := llm.GenerateContent(ctx, step1, llms.WithMaxTokens(64))
	require.NoError(t, err)
	info1 := resp1.Choices[0].GenerationInfo
	t.Logf("step 1: input=%v cache_write=%v cache_read=%v", info1["input_tokens"], info1["CacheCreationInputTokens"], info1["CacheReadInputTokens"])
	assert.Greater(t, info1["CacheCreationInputTokens"], 0, "step 1 writes the cache")

	step2 := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.WithCacheControl(llms.TextContent{Text: sys.String()}, mark)}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.WithCacheControl(llms.TextContent{Text: "Why is pod api-0 restarting?"}, mark)}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: resp1.Choices[0].Content}}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.WithCacheControl(
			llms.ToolCallResponse{ToolCallID: "t1", Name: "kubectl", Content: "Last State: Terminated, Reason: OOMKilled"}, mark)}},
	}
	resp2, err := llm.GenerateContent(ctx, step2, llms.WithMaxTokens(64),
		llms.WithStreamingFunc(func(context.Context, []byte) error { return nil }))
	require.NoError(t, err)
	info2 := resp2.Choices[0].GenerationInfo
	t.Logf("step 2: input=%v cache_write=%v cache_read=%v", info2["input_tokens"], info2["CacheCreationInputTokens"], info2["CacheReadInputTokens"])
	assert.Greater(t, info2["CacheReadInputTokens"], 0, "step 2 reads step 1's prefix")
}
