package huggingface

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/llms/huggingface/huggingfaceclient"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Hermetic unit tests for the HuggingFace provider. The message-mapping helpers
// are pure; GenerateContent is exercised against an httptest server (same pattern
// as huggingfaceclient_test.go) so no live endpoint or token is needed. The
// live-endpoint smoke test stays in huggingface_test.go (skipped without creds).

func textMsg(role llms.ChatMessageType, text string) llms.MessageContent {
	return llms.MessageContent{Role: role, Parts: []llms.ContentPart{llms.TextContent{Text: text}}}
}

func TestGenerateMessageContent(t *testing.T) {
	got := generateMessageContent([]llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "system instruction"),
		textMsg(llms.ChatMessageTypeHuman, "hello"),
		textMsg(llms.ChatMessageTypeAI, "hi there"),
	})

	// System messages are dropped in the legacy prompt format.
	assert.NotContains(t, got, "system instruction")
	assert.Contains(t, got, "<|user|>\nhello<|end|>\n")
	assert.Contains(t, got, "<|assistant|>\nhi there<|end|>\n")
	assert.True(t, strings.HasSuffix(got, "<|assistant|>"), "prompt must end with the assistant turn marker")
}

func TestParseMessageResponse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"extracts last assistant turn", "<|user|>\nq<|end|>\n<|assistant|>the answer<|end|>", "the answer"},
		{"plain text passthrough", "plain answer", "plain answer"},
		{"trims trailing end marker", "answer<|end|>", "answer"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseMessageResponse(tc.in))
		})
	}
}

func TestToChatMessages_MergesSystemToFront(t *testing.T) {
	out := toChatMessages([]llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "u1"),
		textMsg(llms.ChatMessageTypeSystem, "s1"),
		textMsg(llms.ChatMessageTypeAI, "a1"),
		textMsg(llms.ChatMessageTypeSystem, "s2"),
		textMsg(llms.ChatMessageTypeHuman, ""), // empty content is skipped
	})

	require.Len(t, out, 3)
	// All system parts merged into a single leading system message.
	assert.Equal(t, huggingfaceclient.ChatMessage{Role: "system", Content: "s1\n\ns2"}, out[0])
	// Non-system messages keep their original order.
	assert.Equal(t, huggingfaceclient.ChatMessage{Role: "user", Content: "u1"}, out[1])
	assert.Equal(t, huggingfaceclient.ChatMessage{Role: "assistant", Content: "a1"}, out[2])
}

func TestToChatMessages_NoSystem(t *testing.T) {
	out := toChatMessages([]llms.MessageContent{textMsg(llms.ChatMessageTypeHuman, "hi")})
	require.Len(t, out, 1)
	assert.Equal(t, huggingfaceclient.ChatMessage{Role: "user", Content: "hi"}, out[0])
}

func TestGenerateContent_DefaultAPI(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"generated_text":"max(memory) by (pod)"}]`))
	}))
	t.Cleanup(srv.Close)

	llm, err := New(WithToken("tok"), WithURL(srv.URL), WithModel("nb-slm-promql"))
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		textMsg(llms.ChatMessageTypeHuman, "max memory?"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Choices)
	assert.Equal(t, "max(memory) by (pod)", resp.Choices[0].Content)
	// Legacy path posts to /models/{model}/infer.
	assert.Equal(t, "/models/nb-slm-promql/infer", gotPath)
	// Token usage is not reported on the legacy path, so no GenerationInfo.
	assert.Nil(t, resp.Choices[0].GenerationInfo)
}

func TestGenerateContent_OpenAIAPI(t *testing.T) {
	var gotPath string
	var captured struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &captured)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello back"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	}))
	t.Cleanup(srv.Close)

	llm, err := New(WithToken("tok"), WithURL(srv.URL), WithModel("qwen3"), WithAPIType("openai"))
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "be brief"),
		textMsg(llms.ChatMessageTypeHuman, "hi"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Choices)

	assert.Equal(t, "/v1/chat/completions", gotPath)
	assert.Equal(t, "hello back", resp.Choices[0].Content)

	// Token usage maps to Anthropic-style keys consumed by llm_common.go.
	require.NotNil(t, resp.Choices[0].GenerationInfo)
	assert.Equal(t, 11, resp.Choices[0].GenerationInfo["InputTokens"])
	assert.Equal(t, 7, resp.Choices[0].GenerationInfo["OutputTokens"])
	assert.Equal(t, 18, resp.Choices[0].GenerationInfo["total_tokens"])

	// Request carries the merged system message at position 0.
	require.Len(t, captured.Messages, 2)
	assert.Equal(t, "system", captured.Messages[0].Role)
	assert.Equal(t, "be brief", captured.Messages[0].Content)
	assert.Equal(t, "user", captured.Messages[1].Role)
	assert.Equal(t, "qwen3", captured.Model)
}
