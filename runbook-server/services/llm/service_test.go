package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProcessRequest_AsyncPolling(t *testing.T) {
	convID := "test-conv-123"
	sessionID := "test-sess-456"
	expectedMsg := "Hello from AI"
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++

		if r.URL.Path == "/v1/completions/chat" {
			// Initial call
			resp := llmChatResponse{
				Data: llmAgentResponse{
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
				resp := llmChatResponse{
					Data: llmAgentResponse{
						ConversationId: convID,
						SessionId:      sessionID,
						Status:         "IN_PROGRESS",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				// Final completion
				resp := llmChatResponse{
					Data: llmAgentResponse{
						ConversationId: convID,
						SessionId:      sessionID,
						Status:         "COMPLETED",
						Messages: []llmMessage{
							{
								Id:             "msg-1",
								ConversationId: convID,
								Response:       expectedMsg,
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
	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = server.URL
	config.Config.RunbookServerLlmRetryAttempts = 5
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	// Setup context manually to avoid DB calls in NewSecurityContextForTenantAdmin
	sc := &security.SecurityContext{}
	tenantID := "890cad87-c452-4aa7-b84a-742cee0454a1"
	userID := "30b9833e-f667-4b0b-b2c1-065169968e24"
	scData := map[string]any{
		"TenantId": tenantID,
		"UserId":   userID,
	}
	scBytes, _ := json.Marshal(scData)
	err := sc.UnmarshalJSON(scBytes)
	assert.NoError(t, err)

	ctx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)

	req := LLMRequest{
		Message:   "hi",
		AccountId: "a2a30b02-0f67-42e5-a2ab-c658230fd798",
	}

	start := time.Now()
	var resp LLMResponse
	resp, err = ProcessRequest(ctx, req)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, expectedMsg, resp.Message)
	assert.Equal(t, convID, resp.ConversationId)
	assert.Equal(t, "COMPLETED", resp.Status)
	assert.True(t, duration >= 2*time.Second, "Should have polled at least twice with 1s backoff")
	assert.Equal(t, 4, callCount, "Should have 1 initial call + 3 polling calls (2 progress, 1 complete)")
}

func TestProcessRequest_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := llmChatResponse{
			Data: llmAgentResponse{
				ConversationId: "conv-1",
				Status:         "IN_PROGRESS",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = server.URL
	config.Config.RunbookServerLlmRetryAttempts = 2
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	sc := &security.SecurityContext{}
	scData := map[string]any{"TenantId": "890cad87-c452-4aa7-b84a-742cee0454a1"}
	scBytes, _ := json.Marshal(scData)
	err := sc.UnmarshalJSON(scBytes)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	reqCtx := security.NewRequestContext(ctx, sc, nil, nil, nil)

	req := LLMRequest{
		Message:   "hi",
		AccountId: "acc-1",
	}

	_, err = ProcessRequest(reqCtx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.DeadlineExceeded.Error())
}

func TestProcessRequest_MaxRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := llmChatResponse{
			Data: llmAgentResponse{
				ConversationId: "conv-1",
				Status:         "IN_PROGRESS",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = server.URL
	config.Config.RunbookServerLlmRetryAttempts = 2
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	sc := &security.SecurityContext{}
	scData := map[string]any{"TenantId": "890cad87-c452-4aa7-b84a-742cee0454a1"}
	scBytes, _ := json.Marshal(scData)
	err := sc.UnmarshalJSON(scBytes)
	assert.NoError(t, err)

	reqCtx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)

	req := LLMRequest{
		Message:   "hi",
		AccountId: "acc-1",
	}

	_, err = ProcessRequest(reqCtx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retry attempts reached")
}

func TestProcessRequest_ForwardsModelOverride(t *testing.T) {
	var capturedInitialBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/completions/chat" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedInitialBody)
			_ = json.NewEncoder(w).Encode(llmChatResponse{
				Data: llmAgentResponse{ConversationId: "c1", SessionId: "s1", Status: "IN_PROGRESS"},
			})
			return
		}
		if r.URL.Path == "/v1/completions/chat_get" {
			_ = json.NewEncoder(w).Encode(llmChatResponse{
				Data: llmAgentResponse{
					ConversationId: "c1",
					SessionId:      "s1",
					Status:         "COMPLETED",
					Messages:       []llmMessage{{Id: "m1", ConversationId: "c1", Response: "ok", Status: "COMPLETED"}},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = server.URL
	config.Config.RunbookServerLlmRetryAttempts = 2
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	sc := &security.SecurityContext{}
	scData := map[string]any{"TenantId": "890cad87-c452-4aa7-b84a-742cee0454a1"}
	scBytes, _ := json.Marshal(scData)
	err := sc.UnmarshalJSON(scBytes)
	assert.NoError(t, err)
	reqCtx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)

	req := LLMRequest{
		Message:      "hi",
		AccountId:    "acc-1",
		LlmProvider:  "anthropic",
		LlmModelName: "claude-3-5-sonnet",
	}

	_, err = ProcessRequest(reqCtx, req)
	assert.NoError(t, err)
	assert.NotNil(t, capturedInitialBody)
	input, _ := capturedInitialBody["input"].(map[string]any)
	request, _ := input["request"].(map[string]any)
	cfg, _ := request["config"].(map[string]any)
	assert.Equal(t, "anthropic", cfg["llm_provider"])
	assert.Equal(t, "claude-3-5-sonnet", cfg["llm_model_name"])
}

func TestProcessRequest_OmitsConfigWhenNoModelOverride(t *testing.T) {
	var capturedInitialBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/completions/chat" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedInitialBody)
			_ = json.NewEncoder(w).Encode(llmChatResponse{
				Data: llmAgentResponse{ConversationId: "c1", SessionId: "s1", Status: "IN_PROGRESS"},
			})
			return
		}
		if r.URL.Path == "/v1/completions/chat_get" {
			_ = json.NewEncoder(w).Encode(llmChatResponse{
				Data: llmAgentResponse{
					ConversationId: "c1",
					SessionId:      "s1",
					Status:         "COMPLETED",
					Messages:       []llmMessage{{Id: "m1", ConversationId: "c1", Response: "ok", Status: "COMPLETED"}},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = server.URL
	config.Config.RunbookServerLlmRetryAttempts = 2
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	sc := &security.SecurityContext{}
	scData := map[string]any{"TenantId": "890cad87-c452-4aa7-b84a-742cee0454a1"}
	scBytes, _ := json.Marshal(scData)
	err := sc.UnmarshalJSON(scBytes)
	assert.NoError(t, err)
	reqCtx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)

	_, err = ProcessRequest(reqCtx, LLMRequest{Message: "hi", AccountId: "acc-1"})
	assert.NoError(t, err)
	input, _ := capturedInitialBody["input"].(map[string]any)
	request, _ := input["request"].(map[string]any)
	_, hasConfig := request["config"]
	assert.False(t, hasConfig, "config key should be omitted when no provider/model override set")
}

func TestProcessRequest_E2E(t *testing.T) {
	if os.Getenv("TEST_TENANT_ID") == "" || os.Getenv("TEST_USER_ID") == "" || os.Getenv("TEST_ACCOUNT_ID") == "" {
		t.Skip("Skipping E2E test: missing environment variables")
	}
	reqCtx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_USER_ID"), nil)

	req := LLMRequest{
		Message:   "hi",
		AccountId: os.Getenv("TEST_ACCOUNT_ID"),
	}

	var resp LLMResponse
	var err error
	resp, err = ProcessRequest(reqCtx, req)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Message)
	assert.NotEmpty(t, resp.ConversationId)
	assert.NotEmpty(t, resp.SessionId)
	assert.NotEmpty(t, resp.Status)
}
