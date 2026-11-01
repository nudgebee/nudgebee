package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// top_p was configured but dropped before the request left the client (#39446).
func TestGenerateContent_TopPReachesTheWire(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	llm, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("test-model"))
	require.NoError(t, err)
	msgs := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")}

	_, err = llm.GenerateContent(context.Background(), msgs, llms.WithTemperature(0.6), llms.WithTopP(0.95))
	require.NoError(t, err)
	assert.Equal(t, 0.95, body["top_p"])
	assert.Equal(t, 0.6, body["temperature"])

	_, err = llm.GenerateContent(context.Background(), msgs)
	require.NoError(t, err)
	assert.NotContains(t, body, "top_p", "unset top_p leaves the provider default")
}
