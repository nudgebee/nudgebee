package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// OpenAI's completion_tokens includes reasoning_tokens. CompletionTokens in
// GenerationInfo must be the visible share only, or cost (output + thinking)
// prices reasoning twice.
func TestGenerateContent_CompletionTokensExcludeReasoning(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-5.6-sol",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1449,"completion_tokens":994,"total_tokens":2443,
				"completion_tokens_details":{"reasoning_tokens":197}}}`))
	}))
	defer server.Close()

	llm, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("gpt-5.6-sol"))
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hi"),
	})
	require.NoError(t, err)
	info := resp.Choices[0].GenerationInfo
	assert.Equal(t, 797, info["CompletionTokens"])
	assert.Equal(t, 197, info["ThinkingTokens"])
	assert.Equal(t, 2443, info["TotalTokens"])
}

// Streaming usage arrives in the final chunk; the same split applies.
func TestGenerateContent_StreamingCompletionTokensExcludeReasoning(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
			`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":270,"completion_tokens":74,"total_tokens":344,"completion_tokens_details":{"reasoning_tokens":64}}}`,
			`data: [DONE]`,
		} {
			_, _ = w.Write([]byte(line + "\n\n"))
		}
	}))
	defer server.Close()

	llm, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("m"))
	require.NoError(t, err)

	resp, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "hi"),
	}, llms.WithStreamingFunc(func(context.Context, []byte) error { return nil }))
	require.NoError(t, err)
	info := resp.Choices[0].GenerationInfo
	assert.Equal(t, 10, info["CompletionTokens"])
	assert.Equal(t, 64, info["ThinkingTokens"])
}
