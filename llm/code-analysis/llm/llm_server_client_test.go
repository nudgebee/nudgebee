package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestLLMServerModelTransportsMessagesAndTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "workspace-token", r.Header.Get("X-Workspace-Token"))
		var request llmServerRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "account-1", request.AccountID)
		require.Len(t, request.Messages, 1)
		require.Equal(t, "hello", request.Messages[0].Parts[0].Text)
		require.Len(t, request.Tools, 1)
		require.Equal(t, "read_file", request.Tools[0].Function.Name)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Choices":[{"Content":"ok","StopReason":"stop"}]}`))
	}))
	defer server.Close()

	model := &llmServerModel{baseURL: server.URL, token: "workspace-token", accountID: "account-1", client: server.Client()}
	response, err := model.GenerateContent(context.Background(), []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("hello")}},
	}, llms.WithTools([]llms.Tool{{Type: "function", Function: &llms.FunctionDefinition{Name: "read_file", Parameters: map[string]any{"type": "object"}}}}))

	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Equal(t, "ok", response.Choices[0].Content)
}

// A reply with tool calls arrives exactly as llm-server writes it, the
// marshalled langchaingo response. Every call keeps its id, name and arguments;
// a call with no function stays without one, so the planner skips it.
func TestLLMServerModelKeepsToolCalls(t *testing.T) {
	reply, err := json.Marshal(llms.ContentResponse{Choices: []*llms.ContentChoice{{
		Content:    "reading both files",
		StopReason: "tool_calls",
		ToolCalls: []llms.ToolCall{
			{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"main.go"}`}},
			{ID: "call-2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: `{"pattern":"listen"}`}},
			{ID: "call-3", Type: "function"},
		},
	}}})
	require.NoError(t, err)

	// The decoder llm-server reads a request's tool_call part with.
	var sent struct {
		Messages []struct {
			Role  string `json:"role"`
			Parts []struct {
				Type     string `json:"type"`
				ToolCall struct {
					ToolCall struct {
						ID       string            `json:"id"`
						Function llms.FunctionCall `json:"function"`
					} `json:"tool_call"`
				} `json:"tool_call"`
			} `json:"parts"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}))
	defer server.Close()

	model := &llmServerModel{baseURL: server.URL, token: "workspace-token", accountID: "account-1", client: server.Client()}
	response, err := model.GenerateContent(context.Background(), []llms.MessageContent{
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "call-0", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_find", Arguments: `{"name":"*.go"}`}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{ToolCallID: "call-0", Name: "file_find", Content: "main.go"},
		}},
	})

	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	choice := response.Choices[0]
	require.Equal(t, "reading both files", choice.Content)
	require.Equal(t, "tool_calls", choice.StopReason)
	require.Len(t, choice.ToolCalls, 3)
	require.Equal(t, llms.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"main.go"}`}}, choice.ToolCalls[0])
	require.Equal(t, llms.ToolCall{ID: "call-2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "rg", Arguments: `{"pattern":"listen"}`}}, choice.ToolCalls[1])
	require.Nil(t, choice.ToolCalls[2].FunctionCall)

	require.Len(t, sent.Messages, 2)
	replayed := sent.Messages[0].Parts[0]
	require.Equal(t, "tool_call", replayed.Type)
	require.Equal(t, "call-0", replayed.ToolCall.ToolCall.ID)
	require.Equal(t, llms.FunctionCall{Name: "file_find", Arguments: `{"name":"*.go"}`}, replayed.ToolCall.ToolCall.Function)
}

// A call llm-server recorded against the run's conversation is not recorded
// again here. Without that receipt (an llm-server that does not send it, or a
// run it could not resolve) the estimate is recorded exactly as before.
func TestBridgeCallUsageIsRecordedOnce(t *testing.T) {
	generate := map[string]func(*Client, context.Context, []llms.MessageContent) (*llms.ContentResponse, error){
		"retry": func(c *Client, ctx context.Context, messages []llms.MessageContent) (*llms.ContentResponse, error) {
			return c.GenerateContent(ctx, messages)
		},
		"no retry": func(c *Client, ctx context.Context, messages []llms.MessageContent) (*llms.ContentResponse, error) {
			return c.GenerateContentNoRetry(ctx, messages)
		},
	}
	replies := map[string]struct {
		body      string
		wantCalls int
	}{
		"usage linked":   {body: `{"Choices":[{"Content":"ok","StopReason":"stop"}],"usage_linked":true}`, wantCalls: 0},
		"usage unlinked": {body: `{"Choices":[{"Content":"ok","StopReason":"stop"}]}`, wantCalls: 1},
	}

	for path, call := range generate {
		for name, reply := range replies {
			t.Run(path+"/"+name, func(t *testing.T) {
				var request llmServerRequest
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(reply.body))
				}))
				defer server.Close()

				client := NewUsageRecorder("model-1", "custom")
				client.llm = &llmServerModel{baseURL: server.URL, token: "workspace-token", accountID: "account-1", client: server.Client()}
				client.SetModelTier(ModelTierRetrieval)

				ctx := WithCallPhase(WithAgentRunID(context.Background(), "run-1"), CallPhaseReflection)
				response, err := call(client, ctx, []llms.MessageContent{
					{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("hello there")}},
				})

				require.NoError(t, err)
				require.Equal(t, "ok", response.Choices[0].Content)
				require.Equal(t, "run-1", request.AgentRunID)
				require.Equal(t, ModelTierRetrieval, request.ModelTier)
				require.Equal(t, CallPhaseReflection, request.TaskType)

				calls, _ := client.SnapshotCalls()
				require.Len(t, calls, reply.wantCalls)
				if reply.wantCalls == 0 {
					require.Zero(t, client.SnapshotTokenUsage().TotalTokens)
					return
				}
				require.True(t, calls[0].Estimated)
				require.Equal(t, CallPhaseReflection, calls[0].TaskType)
			})
		}
	}
}

func TestBridgeTaggingToleratesNilContext(t *testing.T) {
	var none context.Context

	tagged := WithAgentRunID(none, "run-1")
	require.NotNil(t, tagged)
	require.Equal(t, "run-1", tagged.Value(agentRunIDKey{}))
	require.NotNil(t, WithAgentRunID(none, ""))

	ctx, call := NewUsageRecorder("model-1", "custom").withBridgeCall(none)
	require.NotNil(t, ctx)
	require.NotNil(t, call)
}

// An analysis dispatched without an agent run sends none, so llm-server leaves
// the call unlinked.
func TestBridgeCallWithoutAgentRun(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"Choices":[{"Content":"ok"}]}`))
	}))
	defer server.Close()

	model := &llmServerModel{baseURL: server.URL, token: "workspace-token", accountID: "account-1", client: server.Client()}
	_, err := model.GenerateContent(WithAgentRunID(context.Background(), ""), []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("hello")}},
	})

	require.NoError(t, err)
	require.NotContains(t, body, "agent_run_id")
	require.NotContains(t, body, "model_tier")
	require.NotContains(t, body, "task_type")
}
