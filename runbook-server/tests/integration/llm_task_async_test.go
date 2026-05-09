package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/config"
)

func (s *IntegrationTestSuite) TestExecuteLLMTaskAsync() {
	if config.Config.RunIntegrationTests != "true" {
		s.T().Skip("Skipping integration test as RUN_INTEGRATION_TESTS is not true.")
	}
	s.T().Log("Running TestExecuteLLMTaskAsync...")

	convID := "async-conv-123"
	expectedFinalMsg := "Async integration test response"
	callCount := 0

	// Create mock LLM server
	llmMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++

		if r.URL.Path == "/v1/completions/chat" {
			resp := map[string]any{
				"data": map[string]any{
					"conversation_id": convID,
					"status":          "IN_PROGRESS",
					"response":        []string{"Asynchronous request received."},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/v1/completions/chat_get" {
			if callCount < 3 { // 1 initial + 1 polling IN_PROGRESS
				resp := map[string]any{
					"data": map[string]any{
						"conversation_id": convID,
						"status":          "IN_PROGRESS",
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				resp := map[string]any{
					"data": map[string]any{
						"conversation_id": convID,
						"status":          "COMPLETED",
						"llm_conversation_messages": []map[string]any{
							{
								"response": expectedFinalMsg,
								"status":   "COMPLETED",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			}
			return
		}
	}))
	defer llmMock.Close()

	// Redirect LLM requests to mock server
	oldURL := config.Config.LlmServerUrl
	config.Config.LlmServerUrl = llmMock.URL
	config.Config.RunbookServerLlmRetryAttempts = 5
	config.Config.RunbookServerLlmInitialBackoffSeconds = 1
	defer func() { config.Config.LlmServerUrl = oldURL }()

	// Execute llm.nubi task
	taskType := "llm.nubi"
	params := map[string]any{
		"message": "hello async world",
	}
	body, err := json.Marshal(params)
	s.Require().NoError(err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tasks/%s/execute", apiBaseURL, taskType), bytes.NewReader(body))
	s.Require().NoError(err)
	s.addRequestHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer func() { _ = resp.Body.Close() }()

	s.Assert().Equal(http.StatusOK, resp.StatusCode)

	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	s.Require().NoError(err)

	// Verify task result
	taskData := result["result"].(map[string]any)
	s.Assert().Equal(expectedFinalMsg, taskData["data"])
	s.Assert().Equal(convID, taskData["conversation_id"])
	s.Assert().GreaterOrEqual(callCount, 3, "Should have polled at least once")
}
