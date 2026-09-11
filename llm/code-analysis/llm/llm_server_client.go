package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// llmServerModel is the code-analysis-side adapter for llm-server's internal
// workspace generation API. Provider selection and all provider-specific
// credentials stay in llm-server; this process only transports the normalized
// message/tool contract required by the ReAct loop.
type llmServerModel struct {
	baseURL   string
	token     string
	accountID string
	client    *http.Client
}

type llmServerRequest struct {
	AccountID string             `json:"account_id"`
	Messages  []llmServerMessage `json:"messages"`
	Tools     []llms.Tool        `json:"tools,omitempty"`
}

type llmServerMessage struct {
	Role  string          `json:"role"`
	Parts []llmServerPart `json:"parts"`
}

type llmServerPart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ToolCall     *llms.ToolCall         `json:"tool_call,omitempty"`
	ToolResponse *llms.ToolCallResponse `json:"tool_response,omitempty"`
}

func newLLMServerModel() (*llmServerModel, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("NB_LLM_SERVER_URL")), "/")
	token := strings.TrimSpace(os.Getenv("NB_WORKSPACE_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("NB_ACCOUNT_ID"))
	if baseURL == "" || token == "" || accountID == "" {
		return nil, fmt.Errorf("NB_LLM_SERVER_URL, NB_WORKSPACE_TOKEN and NB_ACCOUNT_ID are required")
	}
	return &llmServerModel{baseURL: baseURL, token: token, accountID: accountID, client: &http.Client{}}, nil
}

func (m *llmServerModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	request := llmServerRequest{AccountID: m.accountID, Messages: make([]llmServerMessage, 0, len(messages))}
	for _, message := range messages {
		wire := llmServerMessage{Role: string(message.Role), Parts: make([]llmServerPart, 0, len(message.Parts))}
		for _, part := range message.Parts {
			switch value := part.(type) {
			case llms.TextContent:
				wire.Parts = append(wire.Parts, llmServerPart{Type: "text", Text: value.Text})
			case llms.ToolCall:
				call := value
				wire.Parts = append(wire.Parts, llmServerPart{Type: "tool_call", ToolCall: &call})
			case llms.ToolCallResponse:
				response := value
				wire.Parts = append(wire.Parts, llmServerPart{Type: "tool_response", ToolResponse: &response})
			default:
				return nil, fmt.Errorf("unsupported message part type %T", part)
			}
		}
		request.Messages = append(request.Messages, wire)
	}
	for _, option := range options {
		var config llms.CallOptions
		option(&config)
		if len(config.Tools) > 0 {
			request.Tools = config.Tools
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal llm-server request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/api/v1/workspace/llm/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create llm-server request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", m.token)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm-server request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read llm-server response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm-server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var response llms.ContentResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode llm-server response: %w", err)
	}
	return &response, nil
}

func (m *llmServerModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	response, err := m.GenerateContent(ctx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	if response == nil || len(response.Choices) == 0 {
		return "", fmt.Errorf("llm-server returned no choices")
	}
	return response.Choices[0].Content, nil
}
