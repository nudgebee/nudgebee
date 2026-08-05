package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/code-analysis-agent/config"

	"github.com/tmc/langchaingo/llms"
)

// hfClient builds a huggingface-provider client pointed at srv, plus the
// captured request body of the last call.
func hfClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLM.Provider = "huggingface"
	cfg.LLM.Model = "Qwen/Qwen3.6-35B-A3B-FP8"
	cfg.LLM.ApiKey = "hf-test-token"
	cfg.LLM.ApiEndpoint = srv.URL
	cfg.LLM.ApiType = "openai"

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

// chatCompletionsStub serves the OpenAI route HuggingFace exposes under /v1 and
// records the request body it was sent.
func chatCompletionsStub(t *testing.T, body *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		*body = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
}

// The ReAct planner batches every parallel tool result of a step into ONE Tool
// message, which is what the genai path wants. langchaingo's OpenAI driver
// rejects that outright ("expected exactly one part for role tool, got 2") —
// client-side, so the request never leaves the process. That killed every
// code-analysis run on provider=huggingface the moment Qwen emitted parallel
// tool calls; it was the most frequent of the two Qwen failure modes in dev.
func TestGenerateContent_SplitsBatchedToolResultsForOpenAIWire(t *testing.T) {
	var body []byte
	srv := chatCompletionsStub(t, &body)
	defer srv.Close()

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "query"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.TextContent{Text: "look at both files"},
			llms.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"a.go"}`}},
			llms.ToolCall{ID: "call-2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"path":"b.go"}`}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{ToolCallID: "call-1", Name: "file_view", Content: "contents of a"},
			llms.ToolCallResponse{ToolCallID: "call-2", Name: "file_view", Content: "contents of b"},
		}},
	}

	client := hfClient(t, srv)
	if _, err := client.GenerateContent(context.Background(), messages); err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	var payload struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v\n%s", err, body)
	}

	var toolIDs []string
	for _, m := range payload.Messages {
		if m.Role != "tool" {
			continue
		}
		toolIDs = append(toolIDs, m.ToolCallID)
	}
	if len(toolIDs) != 2 {
		t.Fatalf("got %d tool messages %v, want one per tool result", len(toolIDs), toolIDs)
	}
	if toolIDs[0] != "call-1" || toolIDs[1] != "call-2" {
		t.Errorf("tool_call_ids = %v, want [call-1 call-2] in order", toolIDs)
	}

	// The assistant message keeps BOTH calls together — that half is correct on
	// the OpenAI wire and must not be split alongside the results.
	assistantCalls := 0
	for _, m := range payload.Messages {
		if m.Role != "assistant" {
			continue
		}
		assistantCalls += len(m.ToolCalls)
		for _, tc := range m.ToolCalls {
			if tc.Type != "function" {
				t.Errorf("tool_call %q has type %q, want \"function\"", tc.ID, tc.Type)
			}
		}
	}
	if assistantCalls != 2 {
		t.Errorf("assistant carried %d tool_calls, want 2 in a single message", assistantCalls)
	}
}

// The split must not disturb conversations that already carry one result per
// Tool message — the common single-tool-call case.
func TestGenerateContent_LeavesSingleToolResultsAlone(t *testing.T) {
	var body []byte
	srv := chatCompletionsStub(t, &body)
	defer srv.Close()

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "query"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "grep", Arguments: "{}"}},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{ToolCallID: "call-1", Name: "grep", Content: "match"},
		}},
	}

	client := hfClient(t, srv)
	if _, err := client.GenerateContent(context.Background(), messages); err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	var payload struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v\n%s", err, body)
	}
	if len(payload.Messages) != 3 {
		t.Errorf("got %d messages, want 3 unchanged", len(payload.Messages))
	}
}

// splitToolMessages is scoped to the OpenAI wire format; the genai path builds
// its own request from the batched message and must keep seeing it batched.
func TestSplitToolMessages_NotAppliedToGoogleAI(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "googleai"
	c := &Client{config: cfg}
	if c.usesOpenAIWireFormat() {
		t.Error("googleai must not be treated as an OpenAI-wire provider")
	}

	cfg.LLM.Provider = "bedrock"
	if c.usesOpenAIWireFormat() {
		t.Error("bedrock must not be treated as an OpenAI-wire provider")
	}

	for _, p := range []string{"openai", "huggingface"} {
		cfg.LLM.Provider = p
		if !c.usesOpenAIWireFormat() {
			t.Errorf("%s must be treated as an OpenAI-wire provider", p)
		}
	}
}
