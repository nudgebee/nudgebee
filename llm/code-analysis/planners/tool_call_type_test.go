package planners

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// OpenAI-compatible servers (vLLM behind the HuggingFace Qwen endpoints)
// discriminate the assistant tool-call union on "type", and langchaingo
// serializes that field without omitempty. A ToolCall built without a Type goes
// out as "type": "" and the server rejects the whole request with a 400 —
// which killed every code-analysis run on provider=huggingface as soon as the
// planner replayed its first tool call.
//
// This test asserts on the actual wire payload, not on the struct, because the
// struct field being empty is only a bug by virtue of how it serializes.
func TestConversationToolCallsCarryFunctionTypeOnTheWire(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		body = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p := &ReActPlanner{}
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "query"}}},
	}
	msgs = p.updateConversationMessagesMulti(msgs, []Step{
		{
			Number:      1,
			Thought:     "clone the repo first",
			Action:      "repo_clone",
			ActionInput: map[string]any{"repo_url": "https://example.com/org/repo.git"},
			Observation: "cloned",
			Status:      "completed",
			ToolCallID:  "call-1",
		},
	})

	client, err := openai.New(
		openai.WithToken("test"),
		openai.WithModel("test-model"),
		openai.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	if _, err := client.GenerateContent(context.Background(), msgs); err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	var payload struct {
		Messages []struct {
			Role      string `json:"role"`
			ToolCalls []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v\n%s", err, body)
	}

	seen := 0
	for _, m := range payload.Messages {
		for _, tc := range m.ToolCalls {
			seen++
			if tc.Type != "function" {
				t.Errorf("tool_call %q on role %q has type %q, want \"function\"", tc.ID, m.Role, tc.Type)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no tool_calls in request payload: %s", body)
	}
}
