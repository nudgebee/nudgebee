package anthropicclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ChatMessage represents a single message in the Anthropic conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// MessageRequest represents the payload sent to /v1/messages.
type MessageRequest struct {
	Model         string          `json:"model"`
	Messages      []ChatMessage   `json:"messages"`
	System        string          `json:"system,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig   `json:"output_config,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    any             `json:"tool_choice,omitempty"`
	Stream        bool            `json:"stream,omitempty"`

	StreamingFunc func(ctx context.Context, chunk []byte) error `json:"-"`
}

// ThinkingConfig represents extended thinking parameters.
type ThinkingConfig struct {
	Type         string `json:"type"`                    // "enabled" | "disabled" | "adaptive"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // For Claude 3.7 - 4.5
}

// OutputConfig represents reasoning effort parameters.
type OutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low" | "medium" | "high" for Claude 4.6+
}

// Tool represents an Anthropic tool definition.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

// CacheControl represents prompt caching configuration.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// TextContent represents a text block.
type TextContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageContent represents an image block.
type ImageContent struct {
	Type         string        `json:"type"`
	Source       ImageSource   `json:"source"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageSource represents the image data source.
type ImageSource struct {
	Type      string `json:"type"` // "base64" or "url"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// ToolUseContent represents a tool use request from the model.
type ToolUseContent struct {
	Type  string          `json:"type"` // "tool_use"
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResultContent represents a tool execution result returned to the model.
type ToolResultContent struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Usage represents token usage statistics returned by Anthropic.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	ThinkingTokens           int `json:"thinking_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// MessageResponse represents the response returned by /v1/messages.
type MessageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence string         `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// ContentBlock is a single block in the response content.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// APIError represents an error returned by the Anthropic API.
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type errorResponse struct {
	Type  string   `json:"type"`
	Error APIError `json:"error"`
}

// CreateMessage creates a completion using the Anthropic Messages API.
func (c *Client) CreateMessage(ctx context.Context, req *MessageRequest) (*MessageResponse, error) {
	if req.Stream && req.StreamingFunc != nil {
		return c.createMessageStream(ctx, req)
	}

	req.Stream = false
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("anthropic: received nil response or body from http client")
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp errorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("anthropic: API error (%d %s): %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return nil, fmt.Errorf("anthropic: API returned status code %d: %s", resp.StatusCode, string(respBody))
	}

	var msgResp MessageResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &msgResp, nil
}

type sseEvent struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	Message      *MessageResponse `json:"message,omitempty"`
	ContentBlock *ContentBlock    `json:"content_block,omitempty"`
	Delta        struct {
		Type         string `json:"type"`
		Text         string `json:"text"`
		PartialJSON  string `json:"partial_json"`
		StopReason   string `json:"stop_reason"`
		StopSequence string `json:"stop_sequence"`
		Thinking     string `json:"thinking"`
	} `json:"delta"`
	Usage *Usage    `json:"usage,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

type blockAccumulator struct {
	index           int
	blockType       string
	textBuilder     strings.Builder
	thinkingBuilder strings.Builder
	id              string
	name            string
	jsonBuilder     strings.Builder
}

func (c *Client) createMessageStream(ctx context.Context, req *MessageRequest) (*MessageResponse, error) {
	req.Stream = true
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create streaming request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute streaming request: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("anthropic: received nil response or body from http client")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		var errResp errorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("anthropic: API error (%d %s): %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return nil, fmt.Errorf("anthropic: API returned status code %d: %s", resp.StatusCode, string(respBody))
	}

	reader := bufio.NewReader(resp.Body)
	var finalResp MessageResponse
	blockMap := make(map[int]*blockAccumulator)

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("error reading event stream: %w", readErr)
		}

		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimPrefix(line, "data:")
			dataStr = strings.TrimLeft(dataStr, " \t")
			if dataStr == "" {
				continue
			}
			if strings.TrimSpace(dataStr) == "[DONE]" {
				break
			}

			var ev sseEvent
			if err := json.Unmarshal([]byte(dataStr), &ev); err != nil {
				return nil, fmt.Errorf("failed to unmarshal SSE event data: %w", err)
			}

			switch ev.Type {
			case "error":
				if ev.Error != nil {
					return nil, fmt.Errorf("anthropic: streaming error (%s): %s", ev.Error.Type, ev.Error.Message)
				}
				return nil, fmt.Errorf("anthropic: error event received in event stream")
			case "message_start":
				if ev.Message != nil {
					finalResp.ID = ev.Message.ID
					finalResp.Model = ev.Message.Model
					finalResp.Role = ev.Message.Role
					finalResp.Usage.InputTokens = ev.Message.Usage.InputTokens
					finalResp.Usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
					finalResp.Usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
				}

			case "content_block_start":
				acc := &blockAccumulator{index: ev.Index}
				if ev.ContentBlock != nil {
					acc.blockType = ev.ContentBlock.Type
					acc.id = ev.ContentBlock.ID
					acc.name = ev.ContentBlock.Name
					if ev.ContentBlock.Text != "" {
						acc.textBuilder.WriteString(ev.ContentBlock.Text)
					}
					if ev.ContentBlock.Thinking != "" {
						acc.thinkingBuilder.WriteString(ev.ContentBlock.Thinking)
					}
				}
				blockMap[ev.Index] = acc

			case "content_block_delta":
				acc, exists := blockMap[ev.Index]
				if !exists {
					acc = &blockAccumulator{index: ev.Index}
					blockMap[ev.Index] = acc
				}

				if ev.Delta.Type == "text_delta" || (ev.Delta.Text != "" && ev.Delta.Type != "thinking_delta") {
					if acc.blockType == "" {
						acc.blockType = "text"
					}
					acc.textBuilder.WriteString(ev.Delta.Text)
					if req.StreamingFunc != nil {
						if err := req.StreamingFunc(ctx, []byte(ev.Delta.Text)); err != nil {
							return nil, err
						}
					}
				} else if ev.Delta.Type == "thinking_delta" || ev.Delta.Thinking != "" {
					if acc.blockType == "" {
						acc.blockType = "thinking"
					}
					acc.thinkingBuilder.WriteString(ev.Delta.Thinking)
				} else if ev.Delta.Type == "input_json_delta" || ev.Delta.PartialJSON != "" {
					if acc.blockType == "" {
						acc.blockType = "tool_use"
					}
					acc.jsonBuilder.WriteString(ev.Delta.PartialJSON)
				}

			case "message_delta":
				if ev.Delta.StopReason != "" {
					finalResp.StopReason = ev.Delta.StopReason
				}
				if ev.Delta.StopSequence != "" {
					finalResp.StopSequence = ev.Delta.StopSequence
				}
				if ev.Usage != nil {
					finalResp.Usage.OutputTokens = ev.Usage.OutputTokens
					if ev.Usage.ThinkingTokens > 0 {
						finalResp.Usage.ThinkingTokens = ev.Usage.ThinkingTokens
					}
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	// Sort and reconstruct content blocks in index order
	indices := make([]int, 0, len(blockMap))
	for idx := range blockMap {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	for _, idx := range indices {
		acc := blockMap[idx]
		switch acc.blockType {
		case "thinking":
			thinking := acc.thinkingBuilder.String()
			if thinking != "" {
				finalResp.Content = append(finalResp.Content, ContentBlock{
					Type:     "thinking",
					Thinking: thinking,
				})
			}
		case "tool_use":
			args := acc.jsonBuilder.String()
			if strings.TrimSpace(args) == "" || args == "null" {
				args = "{}"
			}
			finalResp.Content = append(finalResp.Content, ContentBlock{
				Type:  "tool_use",
				ID:    acc.id,
				Name:  acc.name,
				Input: json.RawMessage(args),
			})
		default:
			text := acc.textBuilder.String()
			if text != "" {
				finalResp.Content = append(finalResp.Content, ContentBlock{
					Type: "text",
					Text: text,
				})
			}
		}
	}

	return &finalResp, nil
}
