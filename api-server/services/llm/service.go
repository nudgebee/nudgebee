package llm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"time"
)

type llmAction struct {
	Name string `json:"name"`
}

type llmChatInputRequest struct {
	Query          string `json:"query,omitempty"`
	AccountId      string `json:"account_id"`
	ConversationId string `json:"conversation_id,omitempty"`
}

type llmChatInput struct {
	Request any `json:"request"`
}

type llmChatRequest struct {
	Action llmAction    `json:"action"`
	Input  llmChatInput `json:"input"`
}

type llmMessage struct {
	Id             string `json:"id"`
	ConversationId string `json:"conversation_id"`
	Response       string `json:"response"`
	Status         string `json:"status"`
}

type llmAgentResponse struct {
	Response       []string     `json:"response"`
	Query          string       `json:"query"`
	AgentName      string       `json:"agent_name"`
	ConversationId string       `json:"conversation_id"`
	Id             string       `json:"id"`
	SessionId      string       `json:"session_id"`
	Status         string       `json:"status"`
	Messages       []llmMessage `json:"llm_conversation_messages"`
}

type llmApiResponse struct {
	Data   *llmAgentResponse `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func ChatCompletion(sc *security.RequestContext, request ConversationApiRequest) (*ChatCompletionResponse, error) {
	// Make the HTTP POST request to the llm-server
	llmServerURL := fmt.Sprintf("%s/v1/completions/chat", config.Config.LLMServerEndpoint)

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if sc.GetSecurityContext().GetTenantId() != "" {
		headers["x-tenant-id"] = sc.GetSecurityContext().GetTenantId()
	}
	if request.UserId != "" {
		headers["x-user-id"] = request.UserId
	}
	headers[config.Config.LLMServerTokenHeader] = config.Config.LLMServerToken

	chatReq := llmChatRequest{
		Action: llmAction{Name: "chat"},
		Input:  llmChatInput{Request: request},
	}

	var resp *http.Response
	var err error
	maxRetries := 3
	retryInterval := 5 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = common.HttpPost(llmServerURL,
			common.HttpWithJsonBody(chatReq),
			common.HttpWithHeaders(headers),
			common.HttpWithTimeout(300*time.Second),
			common.HttpWithContext(sc.GetContext()),
		)

		if err != nil {
			sc.GetLogger().Error("llm.ChatCompletion: failed to send request", "error", err, "attempt", attempt+1)
			time.Sleep(retryInterval)
			continue
		}

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
			defer func() {
				err := resp.Body.Close()
				if err != nil {
					sc.GetLogger().Error("llm.ChatCompletion: failed to close body", "error", err)
				}
			}()
			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read response body: %w", err)
			}

			var apiResponse llmApiResponse
			if err := common.UnmarshalJson(bodyBytes, &apiResponse); err != nil {
				return nil, fmt.Errorf("failed to unmarshal response: %w", err)
			}

			if len(apiResponse.Errors) > 0 {
				return nil, errors.New(apiResponse.Errors[0].Message)
			}

			if apiResponse.Data == nil {
				return nil, errors.New("llm: no data in response")
			}

			convId := apiResponse.Data.ConversationId
			if convId == "" {
				// if we got data immediately, return it
				status := apiResponse.Data.Status
				if status == "COMPLETED" {
					finalResponse := apiResponse.Data.Response
					if len(finalResponse) == 0 && len(apiResponse.Data.Messages) > 0 {
						finalResponse = []string{apiResponse.Data.Messages[len(apiResponse.Data.Messages)-1].Response}
					}
					return &ChatCompletionResponse{
						Response:       finalResponse,
						Query:          apiResponse.Data.Query,
						AgentName:      apiResponse.Data.AgentName,
						ConversationId: apiResponse.Data.ConversationId,
						SessionId:      apiResponse.Data.SessionId,
						Status:         status,
					}, nil
				}
				if status == "FAILED" || status == "ERROR" {
					return nil, fmt.Errorf("llm: initial request failed with status: %s", status)
				}
				return nil, errors.New("llm: conversation_id not found in initial response")
			}

			// Polling for completion
			retryAttempts := config.Config.ServicesServerLLMRetryAttempts
			backoffSec := config.Config.ServicesServerLLMInitialBackoffSeconds
			if backoffSec <= 0 {
				backoffSec = 2
			}
			backoff := time.Duration(backoffSec) * time.Second

			for i := 0; i < retryAttempts; i++ {
				select {
				case <-sc.GetContext().Done():
					return nil, sc.GetContext().Err()
				case <-time.After(backoff):
				}

				getReq := llmChatRequest{
					Action: llmAction{Name: "chat"},
					Input: llmChatInput{
						Request: llmChatInputRequest{
							ConversationId: convId,
							AccountId:      request.AccountId,
						},
					},
				}

				pollResp, err := common.HttpPost(fmt.Sprintf("%s/v1/completions/chat_get", config.Config.LLMServerEndpoint),
					common.HttpWithHeaders(headers),
					common.HttpWithJsonBody(getReq),
					common.HttpWithTimeout(30*time.Second),
					common.HttpWithContext(sc.GetContext()))

				if err != nil {
					sc.GetLogger().Error("llm.ChatCompletion: error in chat_get", "error", err, "attempt", i+1)
					continue
				}

				pollBody, err := io.ReadAll(pollResp.Body)
				if closeErr := pollResp.Body.Close(); closeErr != nil {
					sc.GetLogger().Warn("llm.ChatCompletion: error closing poll response body", "error", closeErr)
				}
				if err != nil {
					sc.GetLogger().Error("llm.ChatCompletion: unable to read body in chat_get", "error", err)
					continue
				}

				if pollResp.StatusCode != http.StatusOK {
					sc.GetLogger().Warn("llm.ChatCompletion: chat_get returned non-200", "status", pollResp.StatusCode, "body", string(pollBody))
					continue
				}

				var pollApiResponse llmApiResponse
				if err := common.UnmarshalJson(pollBody, &pollApiResponse); err != nil {
					sc.GetLogger().Error("llm.ChatCompletion: unable to unmarshal chat_get response", "error", err)
					continue
				}

				if pollApiResponse.Data == nil {
					sc.GetLogger().Error("llm.ChatCompletion: no data in chat_get response")
					continue
				}

				status := pollApiResponse.Data.Status
				if status == "COMPLETED" {
					finalResponse := pollApiResponse.Data.Response
					if len(finalResponse) == 0 && len(pollApiResponse.Data.Messages) > 0 {
						finalResponse = []string{pollApiResponse.Data.Messages[len(pollApiResponse.Data.Messages)-1].Response}
					}
					return &ChatCompletionResponse{
						Response:       finalResponse,
						Query:          pollApiResponse.Data.Query,
						AgentName:      pollApiResponse.Data.AgentName,
						ConversationId: convId,
						SessionId:      pollApiResponse.Data.SessionId,
						Status:         pollApiResponse.Data.Status,
					}, nil
				}

				if status == "FAILED" || status == "ERROR" {
					return nil, fmt.Errorf("llm: request failed with status: %s", status)
				}

				sc.GetLogger().Info("llm.ChatCompletion: request in progress", "conversation_id", convId, "attempt", i+1)
			}
			return nil, errors.New("llm: max retry attempts reached")
		}

		sc.GetLogger().Warn("llm.ChatCompletion: received non-OK status", "status_code", resp.StatusCode, "attempt", attempt+1)
		if closeErr := resp.Body.Close(); closeErr != nil {
			sc.GetLogger().Warn("llm.ChatCompletion: error closing response body", "error", closeErr)
		}
		time.Sleep(retryInterval)
	}

	return nil, fmt.Errorf("failed to get a successful response after %d attempts", maxRetries)
}
