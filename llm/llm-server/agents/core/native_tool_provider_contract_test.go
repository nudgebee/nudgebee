package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"nudgebee/llm/llms/openai"
)

func TestOpenAINativeToolRoundTrip(t *testing.T) {
	requests := &requestRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		request := decodeJSONRequest(t, r)
		requestNumber := requests.add(request)

		w.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			_, err := fmt.Fprint(w, `{
				"id":"chatcmpl-tool","object":"chat.completion","created":1,"model":"gpt-4o",
				"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"inspect_resources","arguments":"{\"command\":\"get pods\",\"resources\":[\"pods\",\"services\"]}"}}]},"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
			}`)
			require.NoError(t, err)
			return
		case 2:
			_, err := fmt.Fprint(w, `{
				"id":"chatcmpl-final","object":"chat.completion","created":2,"model":"gpt-4o",
				"choices":[{"index":0,"message":{"role":"assistant","content":"inspection complete"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}
			}`)
			require.NoError(t, err)
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	model, err := openai.New(
		openai.WithToken("test-token"),
		openai.WithModel("gpt-4o"),
		openai.WithBaseURL(server.URL),
		openai.WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	assertNativeToolRoundTrip(t, model)

	recordedRequests := requests.snapshot()
	require.Len(t, recordedRequests, 2)
	first := recordedRequests[0]
	second := recordedRequests[1]
	assertProviderToolSchema(t, first, "tools", "function", "parameters")
	assertOpenAIToolReplay(t, second)
}

func TestAnthropicNativeToolRoundTrip(t *testing.T) {
	requests := &requestRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/messages", r.URL.Path)
		request := decodeJSONRequest(t, r)
		requestNumber := requests.add(request)

		w.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			_, err := fmt.Fprint(w, `{
				"id":"msg_tool","type":"message","role":"assistant","model":"claude-sonnet-4-6",
				"content":[{"type":"tool_use","id":"toolu_1","name":"inspect_resources","input":{"command":"get pods","resources":["pods","services"]}}],
				"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}
			}`)
			require.NoError(t, err)
			return
		case 2:
			_, err := fmt.Fprint(w, `{
				"id":"msg_final","type":"message","role":"assistant","model":"claude-sonnet-4-6",
				"content":[{"type":"text","text":"inspection complete"}],
				"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":3}
			}`)
			require.NoError(t, err)
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	model, err := anthropic.New(
		anthropic.WithToken("test-token"),
		anthropic.WithModel("claude-sonnet-4-6"),
		anthropic.WithBaseURL(server.URL),
		anthropic.WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	assertNativeToolRoundTrip(t, wrapAnthropicChoiceNormalizer(model))

	recordedRequests := requests.snapshot()
	require.Len(t, recordedRequests, 2)
	first := recordedRequests[0]
	second := recordedRequests[1]
	assertProviderToolSchema(t, first, "tools", "input_schema", "input_schema")
	assertAnthropicToolReplay(t, second)
}

type requestRecorder struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (r *requestRecorder) add(request map[string]any) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	return len(r.requests)
}

func (r *requestRecorder) snapshot() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.requests...)
}

func assertNativeToolRoundTrip(t *testing.T, model llms.Model) {
	t.Helper()

	messages := []llms.MessageContent{{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{llms.TextPart("inspect the namespace")},
	}}
	response, err := model.GenerateContent(context.Background(), messages, llms.WithTools(nativeToolContractFixture()))
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Len(t, response.Choices[0].ToolCalls, 1)

	toolCall := response.Choices[0].ToolCalls[0]
	require.NotNil(t, toolCall.FunctionCall)
	assert.Equal(t, "inspect_resources", toolCall.FunctionCall.Name)
	assert.JSONEq(t, `{"command":"get pods","resources":["pods","services"]}`, toolCall.FunctionCall.Arguments)
	assert.NotEmpty(t, toolCall.ID)

	messages = append(messages,
		llms.MessageContent{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{toolCall}},
		llms.MessageContent{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    `{"pods":2,"services":1}`,
		}}},
	)
	response, err = model.GenerateContent(context.Background(), messages, llms.WithTools(nativeToolContractFixture()))
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	assert.Equal(t, "inspection complete", response.Choices[0].Content)
}

func nativeToolContractFixture() []llms.Tool {
	return []llms.Tool{{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "inspect_resources",
			Description: "Inspect Kubernetes resources",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
					"resources": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []string{"command"},
			},
		},
	}}
}

func decodeJSONRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var request map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
	require.NoError(t, r.Body.Close())
	return request
}

func assertProviderToolSchema(t *testing.T, request map[string]any, toolsKey, schemaKey, nestedSchemaKey string) {
	t.Helper()
	tools := requireJSONArray(t, request[toolsKey])
	require.Len(t, tools, 1)
	tool := requireJSONObject(t, tools[0])

	var schema map[string]any
	if schemaKey == "function" {
		function := requireJSONObject(t, tool[schemaKey])
		assert.Equal(t, "inspect_resources", function["name"])
		schema = requireJSONObject(t, function[nestedSchemaKey])
	} else {
		assert.Equal(t, "inspect_resources", tool["name"])
		schema = requireJSONObject(t, tool[schemaKey])
	}
	properties := requireJSONObject(t, schema["properties"])
	resources := requireJSONObject(t, properties["resources"])
	items := requireJSONObject(t, resources["items"])
	assert.Equal(t, "string", items["type"])
}

func assertOpenAIToolReplay(t *testing.T, request map[string]any) {
	t.Helper()
	messages := requireJSONArray(t, request["messages"])
	require.Len(t, messages, 3)
	assistant := requireJSONObject(t, messages[1])
	toolCalls := requireJSONArray(t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", requireJSONObject(t, toolCalls[0])["id"])
	toolResult := requireJSONObject(t, messages[2])
	assert.Equal(t, "tool", toolResult["role"])
	assert.Equal(t, "call_1", toolResult["tool_call_id"])
}

func assertAnthropicToolReplay(t *testing.T, request map[string]any) {
	t.Helper()
	messages := requireJSONArray(t, request["messages"])
	require.Len(t, messages, 3)
	assistantContent := requireJSONArray(t, requireJSONObject(t, messages[1])["content"])
	assert.Equal(t, "toolu_1", requireJSONObject(t, assistantContent[0])["id"])
	toolContent := requireJSONArray(t, requireJSONObject(t, messages[2])["content"])
	toolResult := requireJSONObject(t, toolContent[0])
	assert.Equal(t, "tool_result", toolResult["type"])
	assert.Equal(t, "toolu_1", toolResult["tool_use_id"])
}

func requireJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected JSON object, got %T", value)
	return object
}

func requireJSONArray(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	require.True(t, ok, "expected JSON array, got %T", value)
	return array
}
