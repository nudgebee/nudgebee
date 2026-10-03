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
