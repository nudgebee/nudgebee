package llm

import (
	"errors"
	"fmt"
	"io"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
	"time"
)

// conversationSourceAutomation is the llm-server ConversationSource value used
// to tag every runbook-server-initiated conversation. Mirrors
// core.ConversationSourceAutomation on llm-server (separate Go module — the enum
// cannot be imported, so the string is the cross-service contract).
const conversationSourceAutomation = "Automation"

type LLMRequest struct {
	Message   string `json:"message" validate:"required"`
	AccountId string `json:"account_id" validate:"required"`
	SessionId string `json:"session_id"`
	// Tools optionally restricts the investigation to a specific allow-list of tool names.
	// When empty, the LLM server uses the auto-selected agent's full tool set.
	Tools []string `json:"tools,omitempty"`
	// LlmProvider / LlmModelName override the account-default LLM for this
	// single request. Empty → llm-server resolves provider/model from its
	// usual env/DB/tier/agent chain. Forwarded into NBQueryConfig.LlmProvider
	// and NBQueryConfig.LlmModelName on llm-server, which honours the override
	// in agents/core/conversation.go.
	LlmProvider  string `json:"llm_provider,omitempty"`
	LlmModelName string `json:"llm_model_name,omitempty"`
	// WorkflowId is the originating workflow definition id. Forwarded into
	// NBQueryConfig.WorkflowId on llm-server so downstream agents (e.g. the code
	// agent raising a PR) can link the result back to the workflow. Empty for
	// non-workflow callers.
	WorkflowId string `json:"workflow_id,omitempty"`
}

type LLMEventRequest struct {
	EventId   string `json:"event_id" validate:"required"`
	AccountId string `json:"account_id" validate:"required"`
}

type LLMResponse struct {
	Message        string `json:"message" validate:"required"`
	ConversationId string `json:"conversation_id" validate:"required"`
	SessionId      string `json:"session_id" validate:"required"`
	Status         string `json:"status" validate:"required"`
}

type llmAction struct {
	Name string `json:"name"`
}

type llmChatInputRequest struct {
	Query          string         `json:"query,omitempty"`
	AccountId      string         `json:"account_id"`
	ConversationId string         `json:"conversation_id,omitempty"`
	Capabilities   map[string]any `json:"capabilities,omitempty"`
	// Source tags the conversation's origin on llm-server (persisted to
	// llm_conversations.source). Runbook-server requests are always part of a
	// workflow step, so they are tagged "Automation" to keep them out of the
	// human-driven UserInvestigation bucket. Mirrors core.ConversationSource on
	// llm-server (separate Go module — cannot import the enum directly).
	Source string `json:"source,omitempty"`
	// Config is forwarded into llm-server's NBQueryConfig. Used today for the
	// per-request `llm_provider` / `llm_model_name` overrides.
	Config map[string]any `json:"config,omitempty"`
}

type llmChatInput struct {
	Request llmChatInputRequest `json:"request"`
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
	ConversationId string       `json:"conversation_id"`
	Id             string       `json:"id"`
	SessionId      string       `json:"session_id"`
	Status         string       `json:"status"`
	Messages       []llmMessage `json:"llm_conversation_messages"`
}

type llmChatResponse struct {
	Data llmAgentResponse `json:"data"`
}

func ProcessRequest(ctx *security.RequestContext, request LLMRequest) (LLMResponse, error) {

	if ctx.GetSecurityContext().GetTenantId() == "" {
		return LLMResponse{}, errors.New("llm: tenant is required")
	}

	if err := common.ValidateStruct(request); err != nil {
		return LLMResponse{}, err
	}

	headersMap := map[string]string{
		"x-tenant-id":                      ctx.GetSecurityContext().GetTenantId(),
		"Content-Type":                     "application/json",
		"x-user-id":                        ctx.GetSecurityContext().GetUserId(),
		config.Config.LlmServerTokenHeader: config.Config.LlmServerToken,
	}

	var capabilities map[string]any
	if len(request.Tools) > 0 {
		capabilities = map[string]any{"allowed_tools": request.Tools}
	}

	var configOverride map[string]any
	if request.LlmProvider != "" || request.LlmModelName != "" || request.WorkflowId != "" {
		configOverride = map[string]any{}
		if request.LlmProvider != "" {
			configOverride["llm_provider"] = request.LlmProvider
		}
		if request.LlmModelName != "" {
			configOverride["llm_model_name"] = request.LlmModelName
		}
		if request.WorkflowId != "" {
			configOverride["workflow_id"] = request.WorkflowId
		}
	}

	chatReq := llmChatRequest{
		Action: llmAction{Name: "chat"},
		Input: llmChatInput{
			Request: llmChatInputRequest{
				Query:        request.Message,
				AccountId:    request.AccountId,
				Capabilities: capabilities,
				Config:       configOverride,
				Source:       conversationSourceAutomation,
			},
		},
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/completions/chat", config.Config.LlmServerUrl),
		common.HttpWithHeaders(headersMap),
		common.HttpWithJsonBody(chatReq),
		common.HttpWithContext(ctx.GetContext()))

	if err != nil {
		return LLMResponse{}, err
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		ctx.GetLogger().Error("llm: unable to read body", "error", err)
		return LLMResponse{}, err
	}

	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		return LLMResponse{}, fmt.Errorf("llm: unable to process request %s", string(body))
	}

	chatResponse := llmChatResponse{}
	if err := common.UnmarshalJson(body, &chatResponse); err != nil {
		ctx.GetLogger().Error("llm: unable to process data", "data", string(body), "error", err)
		return LLMResponse{}, err
	}

	convId := chatResponse.Data.ConversationId
	if convId == "" {
		return LLMResponse{}, errors.New("llm: conversation_id not found in initial response")
	}

	// Polling for completion
	retryAttempts := config.Config.RunbookServerLlmRetryAttempts
	if retryAttempts <= 0 {
		retryAttempts = 15
	}
	backoff := time.Duration(config.Config.RunbookServerLlmInitialBackoffSeconds) * time.Second
	if backoff <= 0 {
		backoff = 2 * time.Second
	}

	for i := 0; i < retryAttempts; i++ {
		select {
		case <-ctx.GetContext().Done():
			return LLMResponse{}, ctx.GetContext().Err()
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

		resp, err = common.HttpPost(fmt.Sprintf("%s/v1/completions/chat_get", config.Config.LlmServerUrl),
			common.HttpWithHeaders(headersMap),
			common.HttpWithJsonBody(getReq),
			common.HttpWithContext(ctx.GetContext()))

		if err != nil {
			ctx.GetLogger().Error("llm: error in chat_get", "error", err, "attempt", i+1)
			continue
		}

		body, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("llm: unable to read body in chat_get", "error", err)
			continue
		}

		if resp.StatusCode != 200 {
			ctx.GetLogger().Warn("llm: chat_get returned non-200", "status", resp.StatusCode, "body", string(body))
			continue
		}

		getResponse := llmChatResponse{}
		if err := common.UnmarshalJson(body, &getResponse); err != nil {
			ctx.GetLogger().Error("llm: unable to unmarshal chat_get response", "error", err)
			continue
		}

		status := getResponse.Data.Status
		if status == "COMPLETED" {
			if len(getResponse.Data.Messages) > 0 {
				msg := getResponse.Data.Messages[len(getResponse.Data.Messages)-1]
				return LLMResponse{
					Message:        msg.Response,
					ConversationId: convId,
					SessionId:      getResponse.Data.SessionId,
					Status:         getResponse.Data.Status,
				}, nil
			}
			return LLMResponse{}, errors.New("llm: completed but no messages found")
		}

		if status == "FAILED" || status == "ERROR" {
			return LLMResponse{}, fmt.Errorf("llm: request failed with status: %s", status)
		}

		ctx.GetLogger().Info("llm: request in progress", "conversation_id", convId, "attempt", i+1)
	}
	return LLMResponse{}, errors.New("llm: max retry attempts reached")
}

func ProcessEventRequest(ctx *security.RequestContext, request LLMEventRequest) (LLMResponse, error) {
	return LLMResponse{}, errors.New("not supported")
}
