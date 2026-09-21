package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/anthropic/anthropicclient"
)

func TestAnthropicLLM_GenerateContent_Unary(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		respJSON := `{
			"id": "msg-001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "text", "text": "Hello from native Anthropic adapter!"}
			],
			"model": "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 15,
				"output_tokens": 8,
				"thinking_tokens": 0,
				"cache_read_input_tokens": 5
			}
		}`
		_, _ = w.Write([]byte(respJSON))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "You are a helpful assistant."},
			},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Hello!"},
			},
		},
	}

	resp, err := llm.GenerateContent(context.Background(), messages,
		llms.WithTemperature(0.7),
		llms.WithMaxTokens(1024),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Choices, 1)

	choice := resp.Choices[0]
	assert.Equal(t, "Hello from native Anthropic adapter!", choice.Content)
	assert.Equal(t, "end_turn", choice.StopReason)
	assert.Equal(t, 15, choice.GenerationInfo["input_tokens"])
	assert.Equal(t, 8, choice.GenerationInfo["output_tokens"])
	assert.Equal(t, 5, choice.GenerationInfo["CacheReadInputTokens"])

	// Check sent request body
	var sent map[string]any
	err = json.Unmarshal(capturedBody, &sent)
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-6", sent["model"])
	assert.Equal(t, "You are a helpful assistant.", sent["system"])
	assert.Equal(t, float64(0.7), sent["temperature"])
	assert.Equal(t, float64(1024), sent["max_tokens"])
}

func TestAnthropicLLM_GenerateContent_UnaryToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		respJSON := `{
			"id": "msg-tool-001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "text", "text": "I will check the weather for you."},
				{
					"type": "tool_use",
					"id": "toolu_01A",
					"name": "get_weather",
					"input": {"location": "San Francisco, CA"}
				}
			],
			"model": "claude-sonnet-4-6",
			"stop_reason": "tool_use",
			"usage": {
				"input_tokens": 50,
				"output_tokens": 30
			}
		}`
		_, _ = w.Write([]byte(respJSON))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "What is the weather in SF?"},
			},
		},
	}

	resp, err := llm.GenerateContent(context.Background(), messages,
		llms.WithTools([]llms.Tool{
			{
				Type: "function",
				Function: &llms.FunctionDefinition{
					Name:        "get_weather",
					Description: "Get current weather",
					Parameters: map[string]any{
						"type": "object",
					},
				},
			},
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Choices, 1)

	choice := resp.Choices[0]
	assert.Equal(t, "I will check the weather for you.", choice.Content)
	assert.Equal(t, "tool_use", choice.StopReason)
	require.Len(t, choice.ToolCalls, 1)
	assert.Equal(t, "toolu_01A", choice.ToolCalls[0].ID)
	assert.Equal(t, "function", choice.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", choice.ToolCalls[0].FunctionCall.Name)
	assert.JSONEq(t, `{"location": "San Francisco, CA"}`, choice.ToolCalls[0].FunctionCall.Arguments)
}

func TestAnthropicLLM_GenerateContent_StreamingToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`data: {"type":"message_start","message":{"id":"msg-stream-tool","type":"message","role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":40,"output_tokens":0}}}` + "\n\n",
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking..."}}` + "\n\n",
			`data: {"type":"content_block_stop","index":0}` + "\n\n",
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_stream_99","name":"fetch_logs","input":{}}}` + "\n\n",
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}` + "\n\n",
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":" \"error\"}"}}` + "\n\n",
			`data: {"type":"content_block_stop","index":1}` + "\n\n",
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":25}}` + "\n\n",
			`data: {"type":"message_stop"}` + "\n\n",
			`data: [DONE]` + "\n\n",
		}

		for _, ev := range events {
			_, _ = w.Write([]byte(ev))
		}
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	var streamChunks []string
	streamingFunc := func(ctx context.Context, chunk []byte) error {
		streamChunks = append(streamChunks, string(chunk))
		return nil
	}

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Fetch error logs"},
			},
		},
	}

	resp, err := llm.GenerateContent(context.Background(), messages,
		llms.WithStreamingFunc(streamingFunc),
		llms.WithMaxTokens(1024),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Choices, 1)

	choice := resp.Choices[0]
	assert.Equal(t, "Checking...", choice.Content)
	assert.Equal(t, "tool_use", choice.StopReason)
	assert.Equal(t, []string{"Checking..."}, streamChunks)
	require.Len(t, choice.ToolCalls, 1)
	assert.Equal(t, "toolu_stream_99", choice.ToolCalls[0].ID)
	assert.Equal(t, "fetch_logs", choice.ToolCalls[0].FunctionCall.Name)
	assert.JSONEq(t, `{"query": "error"}`, choice.ToolCalls[0].FunctionCall.Arguments)
}

func TestAnthropicLLM_GenerateContent_ThinkingStripsIncompatibleSamplingParams(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-think-001","type":"message","role":"assistant","content":[{"type":"text","text":"thinking test"}],"usage":{"input_tokens":10,"output_tokens":5,"thinking_tokens":100}}`))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Explain quantum teleportation."},
			},
		},
	}

	// Pass temperature, top_p, and top_k alongside thinking level
	callOpts := []llms.CallOption{
		llms.WithTemperature(0.0),
		llms.WithTopP(0.9),
		llms.WithMetadata(map[string]any{
			"ThinkingLevel": "high",
		}),
	}

	resp, err := llm.GenerateContent(context.Background(), messages, callOpts...)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var sent map[string]any
	err = json.Unmarshal(capturedBody, &sent)
	require.NoError(t, err)

	// Temperature, TopP, and TopK must be stripped when thinking is active
	assert.NotContains(t, sent, "temperature", "temperature must be omitted when thinking is enabled")
	assert.NotContains(t, sent, "top_p", "top_p must be omitted when thinking is enabled")
	assert.NotContains(t, sent, "top_k", "top_k must be omitted when thinking is enabled")

	assert.Contains(t, sent, "thinking")
	assert.Contains(t, sent, "output_config")
}

func TestAnthropicLLM_GenerateContent_RolloutFlagGating(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-gated-001","type":"message","role":"assistant","content":[{"type":"text","text":"rollout test"}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Test gating"},
			},
		},
	}

	callOpts := []llms.CallOption{
		llms.WithMetadata(map[string]any{
			"ThinkingLevel": "high",
		}),
	}

	// 1. When flag is false, no thinking config is sent
	config.Config.LlmAnthropicThinkingEnabled = false
	_, err = llm.GenerateContent(context.Background(), messages, callOpts...)
	require.NoError(t, err)

	var sentDisabled map[string]any
	err = json.Unmarshal(capturedBody, &sentDisabled)
	require.NoError(t, err)
	assert.NotContains(t, sentDisabled, "thinking")
	assert.NotContains(t, sentDisabled, "output_config")

	// 2. When flag is true, thinking config is sent
	config.Config.LlmAnthropicThinkingEnabled = true
	_, err = llm.GenerateContent(context.Background(), messages, callOpts...)
	require.NoError(t, err)

	var sentEnabled map[string]any
	err = json.Unmarshal(capturedBody, &sentEnabled)
	require.NoError(t, err)
	assert.Contains(t, sentEnabled, "thinking")
	assert.Contains(t, sentEnabled, "output_config")
}

func TestAnthropicLLM_LargeSSEStreamLine(t *testing.T) {
	// Generate a 128-KB text chunk (exceeding standard 64-KB bufio.Scanner buffer)
	largeText := strings.Repeat("A", 128*1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		ev := fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`+"\n\n", largeText)
		_, _ = w.Write([]byte(ev))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Test large stream"},
			},
		},
	}

	resp, err := llm.GenerateContent(context.Background(), messages,
		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			return nil
		}),
	)
	require.NoError(t, err, "SSE line > 64KB must not fail with token too long")
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	assert.Equal(t, largeText, resp.Choices[0].Content)
}

func TestParseClaudeGeneration(t *testing.T) {
	tests := []struct {
		model string
		major int
		minor int
		ok    bool
	}{
		{"claude-sonnet-4-6", 4, 6, true},
		{"claude-sonnet-4-6-20260501", 4, 6, true},
		{"claude-3-7-sonnet", 3, 7, true},
		{"claude-3-7-sonnet-20250219", 3, 7, true},
		{"claude-3-5-sonnet", 3, 5, true},
		{"claude-3-100-sonnet", 3, 100, true},
		{"claude-sonnet-4-100", 4, 100, true},
		{"claude-3-haiku", 3, 0, true},
		{"claude-opus-5", 5, 0, true},
		{"gpt-4o", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			major, minor, ok := parseClaudeGeneration(tt.model)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.major, major)
			assert.Equal(t, tt.minor, minor)
		})
	}
}

func TestResolveThinking(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	// 1. When flag is false, thinking is nil
	config.Config.LlmAnthropicThinkingEnabled = false
	opts46 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "high",
		},
	}
	thDisabled, outDisabled := resolveThinking("claude-sonnet-4-6", opts46)
	assert.Nil(t, thDisabled)
	assert.Nil(t, outDisabled)

	// 2. When flag is true, thinking resolves for Claude 4.6
	config.Config.LlmAnthropicThinkingEnabled = true
	th46, out46 := resolveThinking("claude-sonnet-4-6", opts46)
	require.NotNil(t, th46)
	require.NotNil(t, out46)
	assert.Equal(t, "adaptive", th46.Type)
	assert.Equal(t, "high", out46.Effort)

	// 3. Claude 3.7 with ThinkingLevel medium -> budget 4096
	opts37 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "medium",
		},
		MaxTokens: 8192,
	}
	th37, out37 := resolveThinking("claude-3-7-sonnet", opts37)
	require.NotNil(t, th37)
	assert.Nil(t, out37)
	assert.Equal(t, "enabled", th37.Type)
	assert.Equal(t, 4096, th37.BudgetTokens)

	// 4. Claude 3.5 -> nil thinking
	opts35 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "medium",
		},
	}
	th35, out35 := resolveThinking("claude-3-5-sonnet", opts35)
	assert.Nil(t, th35)
	assert.Nil(t, out35)
}

func TestAnthropicLLM_GenerateContent_ThinkingExposed(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		respJSON := `{
			"id": "msg-think-001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "thinking", "thinking": "Let me reason about the user's question step by step..."},
				{"type": "text", "text": "The answer is 42."}
			],
			"model": "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 20,
				"output_tokens": 15,
				"thinking_tokens": 8
			}
		}`
		_, _ = w.Write([]byte(respJSON))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeGeneric, "What is the answer?"),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Choices, 1)

	choice := resp.Choices[0]
	assert.Equal(t, "The answer is 42.", choice.Content)
	assert.Equal(t, "Let me reason about the user's question step by step...", choice.GenerationInfo["thinking"])
	assert.Equal(t, 8, choice.GenerationInfo["ThinkingTokens"])
}

func TestProcessMessages_CoalesceConsecutiveUserMessages(t *testing.T) {
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "Hello"),
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_1",
					Name:       "tool1",
					Content:    "result1",
				},
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_2",
					Name:       "tool2",
					Content:    "result2",
				},
			},
		},
		llms.TextParts(llms.ChatMessageTypeAI, "Assistant response"),
	}

	system, chatMsgs, err := processMessages(messages)
	require.NoError(t, err)
	assert.Empty(t, system)
	require.Len(t, chatMsgs, 2)
	assert.Equal(t, "user", chatMsgs[0].Role)
	assert.Equal(t, "assistant", chatMsgs[1].Role)

	// First user message should have 3 content blocks coalesced
	blocks, ok := chatMsgs[0].Content.([]any)
	require.True(t, ok)
	require.Len(t, blocks, 3)
}

func TestResolveThinking_Float64Budget(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	opts := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel":  "custom",
			"ThinkingBudget": float64(3000),
		},
		MaxTokens: 8192,
	}
	th, out := resolveThinking("claude-3-7-sonnet", opts)
	require.NotNil(t, th)
	assert.Nil(t, out)
	assert.Equal(t, "enabled", th.Type)
	assert.Equal(t, 3000, th.BudgetTokens)
}

func TestAnthropicLLM_GenerateContent_UnsetTemperatureOmitted(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		respJSON := `{
			"id": "msg-001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Hello!"}],
			"model": "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`
		_, _ = w.Write([]byte(respJSON))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	// Call without specifying temperature (temperature is default 0.0)
	_, err = llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeGeneric, "Hi"),
	})
	require.NoError(t, err)

	var sent map[string]any
	err = json.Unmarshal(capturedBody, &sent)
	require.NoError(t, err)
	_, hasTemp := sent["temperature"]
	assert.False(t, hasTemp, "temperature should be omitted when unset/0.0")
}

func TestAnthropicLLM_Streaming_MalformedJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {malformed json\n\n"))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	_, err = llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeGeneric, "Hi"),
	}, llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		return nil
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal SSE event data")
}

func TestAnthropicLLM_Streaming_OptionalSpaceAfterData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		events := []string{
			`data:{"type":"message_start","message":{"id":"msg-001","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":0}}}`,
			`data:{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data:{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello World"}}`,
			`data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`data:[DONE]`,
		}
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "%s\n\n", ev)
		}
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	var streamOutput strings.Builder
	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeGeneric, "Hi"),
	}, llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		streamOutput.Write(chunk)
		return nil
	}))
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "Hello World", streamOutput.String())
	assert.Equal(t, "Hello World", resp.Choices[0].Content)
}

func TestConvertPartsToBlocks_DataURIAndRemoteURL(t *testing.T) {
	// 1. Data URI with base64
	dataURI := "data:image/jpeg;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	parts := []llms.ContentPart{
		llms.ImageURLContent{URL: dataURI},
	}
	blocks, err := convertPartsToBlocks(parts)
	require.NoError(t, err)
	blockList, ok := blocks.([]any)
	require.True(t, ok)
	require.Len(t, blockList, 1)
	imgBlock, ok := blockList[0].(anthropicclient.ImageContent)
	require.True(t, ok)
	assert.Equal(t, "image", imgBlock.Type)
	assert.Equal(t, "base64", imgBlock.Source.Type)
	assert.Equal(t, "image/jpeg", imgBlock.Source.MediaType)
	assert.Contains(t, imgBlock.Source.Data, "iVBORw0KGgo")

	// 2. Remote URL should return error
	remoteParts := []llms.ContentPart{
		llms.ImageURLContent{URL: "https://example.com/image.png"},
	}
	_, err = convertPartsToBlocks(remoteParts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote image URLs are not supported by the API")
}

func TestResolveThinking_OpusHandling(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	config.Config.LlmAnthropicThinkingEnabled = true
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	opts := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "none",
		},
	}
	// Opus models with "none" level return nil thinking config
	thOpus5, outOpus5 := resolveThinking("claude-opus-5", opts)
	assert.Nil(t, thOpus5)
	assert.Nil(t, outOpus5)

	thOpus46, outOpus46 := resolveThinking("claude-opus-4-6", opts)
	assert.Nil(t, thOpus46)
	assert.Nil(t, outOpus46)

	// Non-opus Claude 4.6 with "none" level returns disabled thinking config
	thSonnet, outSonnet := resolveThinking("claude-sonnet-4-6", opts)
	require.NotNil(t, thSonnet)
	assert.Equal(t, "disabled", thSonnet.Type)
	assert.Nil(t, outSonnet)

	// Missing ThinkingLevel metadata should return nil, nil (opt-in thinking)
	optsMissing := llms.CallOptions{
		Metadata: map[string]any{
			"SomeOtherKey": "foo",
		},
	}
	thMissing, outMissing := resolveThinking("claude-sonnet-4-6", optsMissing)
	assert.Nil(t, thMissing)
	assert.Nil(t, outMissing)

	optsEmpty := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "",
		},
	}
	thEmpty, outEmpty := resolveThinking("claude-sonnet-4-6", optsEmpty)
	assert.Nil(t, thEmpty)
	assert.Nil(t, outEmpty)
}

func TestAnthropicLLM_EmptyToolCallArguments_DefaultsToEmptyJSON(t *testing.T) {
	// Unary test with empty input in tool_use block
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg-001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "tool_use", "id": "tool-1", "name": "doSomething", "input": {}}
			],
			"model": "claude-sonnet-4-6",
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	defer server.Close()

	llm, err := New(
		WithToken("test-token"),
		WithModel("claude-sonnet-4-6"),
		WithBaseURL(server.URL),
	)
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeGeneric, "Run tool"),
	})
	require.NoError(t, err)
	require.Len(t, resp.Choices[0].ToolCalls, 1)
	assert.Equal(t, "{}", resp.Choices[0].ToolCalls[0].FunctionCall.Arguments)
	require.NotNil(t, resp.Choices[0].FuncCall)
	assert.Equal(t, "doSomething", resp.Choices[0].FuncCall.Name)
}

func TestAnthropicClient_MissingToken(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := anthropicclient.New("", "claude-sonnet-4-6", "", nil)
	assert.ErrorIs(t, err, anthropicclient.ErrMissingToken)
}

func TestAnthropicLLM_ProcessMessages_ChatMessageTypeTool(t *testing.T) {
	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "Calculate 2+2"},
			},
		},
		{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{
				llms.ToolCall{
					ID:   "call_123",
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      "calculator",
						Arguments: `{"expr":"2+2"}`,
					},
				},
			},
		},
		{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{
				llms.ToolCallResponse{
					ToolCallID: "call_123",
					Content:    "4",
				},
				llms.TextContent{
					Text: "Additional tool log context",
				},
			},
		},
	}

	sys, chatMsgs, err := processMessages(messages)
	require.NoError(t, err)
	assert.Empty(t, sys)
	require.Len(t, chatMsgs, 3)
	assert.Equal(t, "user", chatMsgs[0].Role)
	assert.Equal(t, "assistant", chatMsgs[1].Role)
	assert.Equal(t, "user", chatMsgs[2].Role)
}

func TestToContentBlocks_NilAndEmptyHandling(t *testing.T) {
	assert.Nil(t, toContentBlocks(nil))
	assert.Nil(t, toContentBlocks(""))
	assert.Nil(t, toContentBlocks([]any{nil, nil}))

	blocks := toContentBlocks([]any{nil, anthropicclient.TextContent{Type: "text", Text: "hello"}})
	require.Len(t, blocks, 1)
	assert.Equal(t, "hello", blocks[0].(anthropicclient.TextContent).Text)
}
