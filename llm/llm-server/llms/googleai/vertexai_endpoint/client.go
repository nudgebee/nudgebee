// Package vertexai_endpoint implements a langchaingo-compatible LLM provider
// for Vertex AI Model Garden dedicated endpoints (e.g., Gemma 4).
//
// These endpoints expose an OpenAI-compatible chat API via the Vertex AI
// :predict wrapper with @requestFormat=chatCompletions. This provider handles
// the request/response envelope translation.
package vertexai_endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"golang.org/x/oauth2/google"
)

// VertexEndpoint implements the llms.Model interface for Vertex AI dedicated endpoints.
type VertexEndpoint struct {
	CallbacksHandler callbacks.Handler
	endpointURL      string // Full :predict URL
	model            string
	project          string
	location         string
	httpClient       *http.Client
	tokenSource      *google.Credentials
}

// Options configures the VertexEndpoint client.
type Options struct {
	EndpointDomain string // e.g., mg-endpoint-xxx.region-xxx.prediction.vertexai.goog
	EndpointID     string // e.g., mg-endpoint-xxx
	Project        string
	Location       string
	Model          string // Model name for responses
}

var _ llms.Model = &VertexEndpoint{}

// New creates a new VertexEndpoint client.
func New(ctx context.Context, opts Options) (*VertexEndpoint, error) {
	// Get default credentials for Vertex AI
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("failed to find default credentials: %w", err)
	}

	endpointURL := fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/endpoints/%s:predict",
		opts.EndpointDomain, opts.Project, opts.Location, opts.EndpointID,
	)

	slog.Info("VertexEndpoint: initialized",
		"endpoint", endpointURL,
		"model", opts.Model,
		"project", opts.Project,
		"location", opts.Location,
	)

	return &VertexEndpoint{
		endpointURL: endpointURL,
		model:       opts.Model,
		project:     opts.Project,
		location:    opts.Location,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
		tokenSource: creds,
	}, nil
}

// Call implements the [llms.Model] interface.
func (v *VertexEndpoint) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, v, prompt, options...)
}

// GenerateContent implements the [llms.Model] interface.
func (v *VertexEndpoint) GenerateContent(
	ctx context.Context,
	messages []llms.MessageContent,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if v.CallbacksHandler != nil {
		v.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{
		MaxTokens:   4096,
		Temperature: 0.7,
	}
	for _, opt := range options {
		opt(&opts)
	}

	// Convert langchain messages to OpenAI chat format
	chatMessages := convertMessages(messages)

	// Build the chat completion request
	chatReq := map[string]any{
		"@requestFormat": "chatCompletions",
		"messages":       chatMessages,
		"max_tokens":     opts.MaxTokens,
		"temperature":    opts.Temperature,
	}

	if opts.TopP > 0 {
		chatReq["top_p"] = opts.TopP
	}
	if len(opts.StopWords) > 0 {
		chatReq["stop"] = opts.StopWords
	}

	// Convert tools
	if len(opts.Tools) > 0 {
		tools := convertTools(opts.Tools)
		if len(tools) > 0 {
			chatReq["tools"] = tools
		}
	}

	// Wrap in Vertex AI :predict envelope
	predictReq := map[string]any{
		"instances": []any{chatReq},
	}

	// Call the endpoint
	resp, err := v.callPredict(ctx, predictReq, opts.StreamingFunc)
	if err != nil {
		return nil, err
	}

	if v.CallbacksHandler != nil {
		v.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, resp)
	}

	return resp, nil
}

// callPredict sends the request to the Vertex AI :predict endpoint.
func (v *VertexEndpoint) callPredict(
	ctx context.Context,
	body map[string]any,
	streamingFunc func(ctx context.Context, chunk []byte) error,
) (*llms.ContentResponse, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", v.endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Get fresh access token
	token, err := v.tokenSource.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	httpResp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	const maxResponseSize = 10 * 1024 * 1024 // 10MB limit
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint returned status %d: %s", httpResp.StatusCode, string(respBody))
	}

	// Parse the Vertex AI :predict response
	var predictResp struct {
		Predictions []chatCompletionResponse `json:"predictions"`
	}
	if err := json.Unmarshal(respBody, &predictResp); err != nil {
		return nil, fmt.Errorf("failed to parse predict response: %w", err)
	}

	if len(predictResp.Predictions) == 0 {
		return nil, fmt.Errorf("no predictions returned from endpoint")
	}

	// Convert to langchain response
	return convertResponse(&predictResp.Predictions[0], streamingFunc, ctx)
}

// convertMessages converts langchain messages to OpenAI chat format.
func convertMessages(messages []llms.MessageContent) []map[string]any {
	var chatMessages []map[string]any

	for _, msg := range messages {
		role := "user"
		switch msg.Role {
		case llms.ChatMessageTypeSystem:
			role = "system"
		case llms.ChatMessageTypeAI:
			role = "assistant"
		case llms.ChatMessageTypeHuman:
			role = "user"
		case llms.ChatMessageTypeTool:
			role = "tool"
		}

		// Extract text content
		var textParts []string
		var toolCalls []map[string]any
		var toolCallID string

		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				textParts = append(textParts, p.Text)
			case llms.ToolCall:
				toolCalls = append(toolCalls, map[string]any{
					"id":   p.ID,
					"type": "function",
					"function": map[string]any{
						"name":      p.FunctionCall.Name,
						"arguments": p.FunctionCall.Arguments,
					},
				})
			case llms.ToolCallResponse:
				role = "tool"
				toolCallID = p.ToolCallID
				textParts = append(textParts, p.Content)
			}
		}

		chatMsg := map[string]any{
			"role": role,
		}

		if len(textParts) > 0 {
			chatMsg["content"] = strings.Join(textParts, "\n")
		}
		if len(toolCalls) > 0 {
			chatMsg["tool_calls"] = toolCalls
		}
		if toolCallID != "" {
			chatMsg["tool_call_id"] = toolCallID
		}

		chatMessages = append(chatMessages, chatMsg)
	}

	return chatMessages
}

// convertTools converts langchain tools to OpenAI tool format.
func convertTools(tools []llms.Tool) []map[string]any {
	var result []map[string]any
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Function.Name,
				"description": tool.Function.Description,
				"parameters":  tool.Function.Parameters,
			},
		})
	}
	return result
}

// Response types matching the OpenAI chat completion format.
type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage,omitempty"`
}

type chatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      chatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

type chatCompletionMessage struct {
	Role      string         `json:"role"`
	Content   *string        `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// convertResponse converts the OpenAI chat response to langchain format.
func convertResponse(
	resp *chatCompletionResponse,
	streamingFunc func(ctx context.Context, chunk []byte) error,
	ctx context.Context,
) (*llms.ContentResponse, error) {
	var contentResponse llms.ContentResponse

	for _, choice := range resp.Choices {
		var toolCalls []llms.ToolCall
		for _, tc := range choice.Message.ToolCalls {
			toolCalls = append(toolCalls, llms.ToolCall{
				ID: tc.ID,
				FunctionCall: &llms.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		content := ""
		if choice.Message.Content != nil {
			content = *choice.Message.Content
		}

		// Stream the content if streaming is enabled
		if streamingFunc != nil && content != "" {
			if err := streamingFunc(ctx, []byte(content)); err != nil {
				slog.Warn("VertexEndpoint: streaming callback error", "error", err)
			}
		}

		metadata := make(map[string]any)
		if resp.Usage != nil {
			metadata["input_tokens"] = resp.Usage.PromptTokens
			metadata["output_tokens"] = resp.Usage.CompletionTokens
			metadata["total_tokens"] = resp.Usage.TotalTokens
			metadata["PromptTokens"] = resp.Usage.PromptTokens
			metadata["CompletionTokens"] = resp.Usage.CompletionTokens
			metadata["TotalTokens"] = resp.Usage.TotalTokens
		}

		contentResponse.Choices = append(contentResponse.Choices,
			&llms.ContentChoice{
				Content:        content,
				StopReason:     choice.FinishReason,
				GenerationInfo: metadata,
				ToolCalls:      toolCalls,
			})
	}

	return &contentResponse, nil
}
