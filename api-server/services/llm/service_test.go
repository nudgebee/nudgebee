package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestChatCompletion_AsyncPolling(t *testing.T) {
	convID := "test-conv-123"
	sessionID := "test-sess-456"
	expectedMsg := []string{"Hello from AI"}
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++

		if r.URL.Path == "/v1/completions/chat" {
			// Initial call
			resp := llmApiResponse{
				Data: &llmAgentResponse{
					ConversationId: convID,
					SessionId:      sessionID,
					Status:         "IN_PROGRESS",
					Response:       []string{"Your request has been received and will be processed asynchronously."},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/v1/completions/chat_get" {
			// Polling calls
			if callCount < 4 { // 1 initial + 2 polling "IN_PROGRESS"
				resp := llmApiResponse{
					Data: &llmAgentResponse{
						ConversationId: convID,
						SessionId:      sessionID,
						Status:         "IN_PROGRESS",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				// Final completion
				resp := llmApiResponse{
					Data: &llmAgentResponse{
						ConversationId: convID,
						SessionId:      sessionID,
						Status:         "COMPLETED",
						Response:       expectedMsg,
						Query:          "hi",
						AgentName:      "test-agent",
						Messages: []llmMessage{
							{
								Id:             "msg-1",
								ConversationId: convID,
								Response:       expectedMsg[0],
								Status:         "COMPLETED",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			}
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	// Setup config
	oldURL := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = server.URL
	config.Config.ServicesServerLLMRetryAttempts = 5
	config.Config.ServicesServerLLMInitialBackoffSeconds = 1
	defer func() { config.Config.LLMServerEndpoint = oldURL }()

	sc := security.NewRequestContext(context.Background(), &security.SecurityContext{}, slog.Default(), nil, nil)

	req := ConversationApiRequest{
		Query:     "hi",
		AccountId: "acc-1",
	}

	start := time.Now()
	resp, err := ChatCompletion(sc, req)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedMsg, resp.Response)
	assert.Equal(t, "hi", resp.Query)
	assert.Equal(t, "test-agent", resp.AgentName)
	assert.Equal(t, convID, resp.ConversationId)
	assert.Equal(t, "COMPLETED", resp.Status)
	assert.True(t, duration >= 2*time.Second, "Should have polled at least twice with 1s backoff")
	assert.Equal(t, 4, callCount, "Should have 1 initial call + 3 polling calls (2 progress, 1 complete)")
}

func TestChatCompletion_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := llmApiResponse{
			Data: &llmAgentResponse{
				ConversationId: "conv-1",
				Status:         "IN_PROGRESS",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	oldURL := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = server.URL
	config.Config.ServicesServerLLMRetryAttempts = 2
	config.Config.ServicesServerLLMInitialBackoffSeconds = 1
	defer func() { config.Config.LLMServerEndpoint = oldURL }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	sc := security.NewRequestContext(ctx, &security.SecurityContext{}, slog.Default(), nil, nil)

	req := ConversationApiRequest{
		Query:     "hi",
		AccountId: "acc-1",
	}

	_, err := ChatCompletion(sc, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.DeadlineExceeded.Error())
}

func TestChatCompletion_E2E(t *testing.T) {
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping E2E test: missing environment variables")
	}
	reqCtx := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	req := ConversationApiRequest{
		Query:     "hi",
		AccountId: os.Getenv("TEST_ACCOUNT"),
	}

	resp, err := ChatCompletion(reqCtx, req)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.ConversationId)
	assert.NotEmpty(t, resp.SessionId)
	assert.NotEmpty(t, resp.Status)
}
