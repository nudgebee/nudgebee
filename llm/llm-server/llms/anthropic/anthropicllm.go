package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/llms/anthropic/anthropicclient"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

var (
	ErrEmptyResponse = errors.New("empty response from Anthropic")
	ErrMissingToken  = errors.New("missing Anthropic API token")
)

// LLM is the native Anthropic implementation of llms.Model.
type LLM struct {
	CallbacksHandler callbacks.Handler
	client           *anthropicclient.Client
}

var _ llms.Model = (*LLM)(nil)

// New creates a new Anthropic LLM instance.
func New(opts ...Option) (*LLM, error) {
	options := &options{}
	for _, opt := range opts {
		opt(options)
	}

	if options.token == "" {
		return nil, ErrMissingToken
	}

	cliOpts := make([]anthropicclient.Option, 0)
	if options.anthropicVersion != "" {
		cliOpts = append(cliOpts, anthropicclient.WithAnthropicVersion(options.anthropicVersion))
	}
	if options.anthropicBeta != "" {
		cliOpts = append(cliOpts, anthropicclient.WithAnthropicBeta(options.anthropicBeta))
	}

	cli, err := anthropicclient.New(options.token, options.model, options.baseURL, options.httpClient, cliOpts...)
	if err != nil {
		return nil, err
	}

	return &LLM{
		client:           cli,
		CallbacksHandler: options.callbackHandler,
	}, nil
}

// Call requests a completion for the given prompt.
func (o *LLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, o, prompt, options...)
}

// GenerateContent implements the llms.Model interface.
func (o *LLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	model := opts.Model
	if model == "" {
		model = o.client.Model
	}

	systemPrompt, chatMessages, err := processMessages(messages)
	if err != nil {
		return nil, err
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	req := &anthropicclient.MessageRequest{
		Model:         model,
		Messages:      chatMessages,
		System:        systemPrompt,
		MaxTokens:     maxTokens,
		StopSequences: opts.StopWords,
		StreamingFunc: opts.StreamingFunc,
		Stream:        opts.StreamingFunc != nil,
	}

	// Thinking / OutputConfig resolution
	thinking, outputConfig := resolveThinking(model, opts)
	req.Thinking = thinking
	req.OutputConfig = outputConfig

	// When extended thinking is enabled, Anthropic strictly rejects modified
	// temperature / top_p / top_k (temperature must be 1.0 or omitted).
	isThinkingActive := (thinking != nil && thinking.Type != "disabled") || (outputConfig != nil && outputConfig.Effort != "")
	if !isThinkingActive {
		// Temperature (omitted if unset 0.0 or model rejects temperature)
		if opts.Temperature > 0 {
			temp := opts.Temperature
			req.Temperature = &temp
		}
		if opts.TopP > 0 {
			topP := opts.TopP
			req.TopP = &topP
		}
		if opts.TopK > 0 {
			topK := opts.TopK
			req.TopK = &topK
		}
	}

	// Tools
	if len(opts.Tools) > 0 {
		tools := make([]anthropicclient.Tool, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			if t.Function != nil {
				tools = append(tools, anthropicclient.Tool{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					InputSchema: t.Function.Parameters,
				})
			}
		}
		req.Tools = tools
	}

	resp, err := o.client.CreateMessage(ctx, req)
	if err != nil {
		if o.CallbacksHandler != nil {
			o.CallbacksHandler.HandleLLMError(ctx, err)
		}
		return nil, err
	}

	if resp == nil || len(resp.Content) == 0 {
		if o.CallbacksHandler != nil {
			o.CallbacksHandler.HandleLLMError(ctx, ErrEmptyResponse)
		}
		return nil, ErrEmptyResponse
	}

	var combinedText strings.Builder
	var combinedThinking strings.Builder
	var toolCalls []llms.ToolCall
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			combinedText.WriteString(block.Text)
		case "thinking":
			combinedThinking.WriteString(block.Thinking)
		case "tool_use":
			args := string(block.Input)
			if strings.TrimSpace(args) == "" || args == "null" {
				args = "{}"
			}
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   block.ID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}

	genInfo := map[string]any{
		"input_tokens":  resp.Usage.InputTokens,
		"output_tokens": resp.Usage.OutputTokens,
	}
	if resp.Usage.ThinkingTokens > 0 {
		genInfo["ThinkingTokens"] = resp.Usage.ThinkingTokens
		genInfo["thinking_tokens"] = resp.Usage.ThinkingTokens
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		genInfo["CacheReadInputTokens"] = resp.Usage.CacheReadInputTokens
	}
	if resp.Usage.CacheCreationInputTokens > 0 {
		genInfo["CacheCreationInputTokens"] = resp.Usage.CacheCreationInputTokens
	}
	if combinedThinking.Len() > 0 {
		genInfo["thinking"] = combinedThinking.String()
	}

	choices := []*llms.ContentChoice{
		{
			Content:        combinedText.String(),
			StopReason:     resp.StopReason,
			GenerationInfo: genInfo,
			ToolCalls:      toolCalls,
		},
	}
	if len(toolCalls) > 0 {
		choices[0].FuncCall = toolCalls[0].FunctionCall
	}

	contentResp := &llms.ContentResponse{
		Choices: choices,
	}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, contentResp)
	}

	return contentResp, nil
}

func processMessages(messages []llms.MessageContent) (string, []anthropicclient.ChatMessage, error) {
	var systemBuilder strings.Builder
	chatMessages := make([]anthropicclient.ChatMessage, 0, len(messages))

	for _, mc := range messages {
		switch mc.Role {
		case llms.ChatMessageTypeSystem:
			for _, part := range mc.Parts {
				if tc, ok := part.(llms.TextContent); ok {
					if systemBuilder.Len() > 0 {
						systemBuilder.WriteString("\n\n")
					}
					systemBuilder.WriteString(tc.Text)
				}
			}

		case llms.ChatMessageTypeHuman, llms.ChatMessageTypeGeneric:
			blocks, err := convertPartsToBlocks(mc.Parts)
			if err != nil {
				return "", nil, err
			}
			chatMessages = append(chatMessages, anthropicclient.ChatMessage{
				Role:    "user",
				Content: blocks,
			})

		case llms.ChatMessageTypeAI:
			blocks, err := convertPartsToBlocks(mc.Parts)
			if err != nil {
				return "", nil, err
			}
			chatMessages = append(chatMessages, anthropicclient.ChatMessage{
				Role:    "assistant",
				Content: blocks,
			})

		case llms.ChatMessageTypeTool:
			for _, part := range mc.Parts {
				switch tr := part.(type) {
				case llms.ToolCallResponse:
					chatMessages = append(chatMessages, anthropicclient.ChatMessage{
						Role: "user",
						Content: []anthropicclient.ToolResultContent{
							{
								Type:      "tool_result",
								ToolUseID: tr.ToolCallID,
								Content:   tr.Content,
							},
						},
					})
				case llms.TextContent:
					chatMessages = append(chatMessages, anthropicclient.ChatMessage{
						Role:    "user",
						Content: tr.Text,
					})
				}
			}

		default:
			return "", nil, fmt.Errorf("unsupported message role: %v", mc.Role)
		}
	}

	return systemBuilder.String(), coalesceChatMessages(chatMessages), nil
}

func coalesceChatMessages(messages []anthropicclient.ChatMessage) []anthropicclient.ChatMessage {
	if len(messages) <= 1 {
		return messages
	}

	coalesced := make([]anthropicclient.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if len(coalesced) > 0 && coalesced[len(coalesced)-1].Role == msg.Role {
			prev := &coalesced[len(coalesced)-1]
			prevBlocks := toContentBlocks(prev.Content)
			currBlocks := toContentBlocks(msg.Content)
			prev.Content = append(prevBlocks, currBlocks...)
		} else {
			coalesced = append(coalesced, msg)
		}
	}
	return coalesced
}

func toContentBlocks(content any) []any {
	if content == nil {
		return nil
	}
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []any{anthropicclient.TextContent{
			Type: "text",
			Text: c,
		}}
	case []any:
		res := make([]any, 0, len(c))
		for _, item := range c {
			if item != nil {
				res = append(res, item)
			}
		}
		if len(res) == 0 {
			return nil
		}
		return res
	case []anthropicclient.ToolResultContent:
		blocks := make([]any, len(c))
		for i, tr := range c {
			blocks[i] = tr
		}
		return blocks
	default:
		return []any{c}
	}
}

func convertPartsToBlocks(parts []llms.ContentPart) (any, error) {
	if len(parts) == 1 {
		if tc, ok := parts[0].(llms.TextContent); ok {
			return tc.Text, nil
		}
	}

	blocks := make([]any, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case llms.TextContent:
			blocks = append(blocks, anthropicclient.TextContent{
				Type: "text",
				Text: p.Text,
			})
		case llms.BinaryContent:
			blocks = append(blocks, anthropicclient.ImageContent{
				Type: "image",
				Source: anthropicclient.ImageSource{
					Type:      "base64",
					MediaType: p.MIMEType,
					Data:      base64.StdEncoding.EncodeToString(p.Data),
				},
			})
		case llms.ImageURLContent:
			// Anthropic Messages API does not support remote image URLs; images must be provided
			// as base64 data. If a data URI (e.g. data:image/png;base64,...) is provided, parse it.
			if strings.HasPrefix(p.URL, "data:") {
				mediaType, base64Data, err := parseDataURI(p.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid data URI in ImageURLContent: %w", err)
				}
				blocks = append(blocks, anthropicclient.ImageContent{
					Type: "image",
					Source: anthropicclient.ImageSource{
						Type:      "base64",
						MediaType: mediaType,
						Data:      base64Data,
					},
				})
			} else {
				return nil, fmt.Errorf("anthropic: remote image URLs are not supported by the API; provide images as base64 BinaryContent or data: URIs")
			}
		case llms.ToolCall:
			var name string
			var args string
			if p.FunctionCall != nil {
				name = p.FunctionCall.Name
				args = p.FunctionCall.Arguments
			}
			if strings.TrimSpace(args) == "" || args == "null" {
				args = "{}"
			}
			var inputMsg json.RawMessage
			if err := json.Unmarshal([]byte(args), &inputMsg); err != nil {
				inputMsg = json.RawMessage(args)
			}
			blocks = append(blocks, anthropicclient.ToolUseContent{
				Type:  "tool_use",
				ID:    p.ID,
				Name:  name,
				Input: inputMsg,
			})
		case llms.ToolCallResponse:
			blocks = append(blocks, anthropicclient.ToolResultContent{
				Type:      "tool_result",
				ToolUseID: p.ToolCallID,
				Content:   p.Content,
			})
		default:
			return nil, fmt.Errorf("unsupported content part type: %T", part)
		}
	}

	return blocks, nil
}

var (
	claudeFamilyFirstRE  = regexp.MustCompile(`claude-[a-z]+-(\d+)(?:[-.](\d{1,3})\b)?`)
	claudeVersionFirstRE = regexp.MustCompile(`claude-(\d+)(?:[-.](\d{1,3})\b)?`)
)

func parseClaudeGeneration(m string) (major, minor int, ok bool) {
	if g := claudeVersionFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		if g[2] != "" {
			minor, _ = strconv.Atoi(g[2])
		}
		return major, minor, true
	}
	if g := claudeFamilyFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		if g[2] != "" {
			minor, _ = strconv.Atoi(g[2])
		}
		return major, minor, true
	}
	return 0, 0, false
}

func resolveThinking(modelID string, options llms.CallOptions) (*anthropicclient.ThinkingConfig, *anthropicclient.OutputConfig) {
	// Gated by rollout flag: when disabled, no thinking configuration is sent.
	if !config.Config.LlmAnthropicThinkingEnabled {
		return nil, nil
	}

	if options.Metadata == nil {
		return nil, nil
	}
	level, _ := options.Metadata["ThinkingLevel"].(string)
	budget := parseIntFromMetadata(options.Metadata["ThinkingBudget"])

	mLower := strings.ToLower(modelID)
	lvlLower := strings.ToLower(strings.TrimSpace(level))

	major, minor, ok := parseClaudeGeneration(mLower)
	if !ok {
		return nil, nil
	}

	maxTokens := options.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	switch {
	case major > 4 || (major == 4 && minor >= 6):
		if lvlLower == "" {
			return nil, nil
		}
		if lvlLower == "none" {
			if strings.Contains(mLower, "opus") {
				return nil, nil
			}
			return &anthropicclient.ThinkingConfig{Type: "disabled"}, nil
		}
		effort := lvlLower
		if effort != "low" && effort != "medium" && effort != "high" {
			effort = "medium"
		}
		return &anthropicclient.ThinkingConfig{Type: "adaptive"}, &anthropicclient.OutputConfig{Effort: effort}

	case major > 3 || (major == 3 && minor >= 7):
		if lvlLower == "none" || (lvlLower == "" && budget <= 0) {
			return nil, nil
		}
		targetBudget := budget
		if targetBudget <= 0 {
			switch lvlLower {
			case "minimal", "low":
				targetBudget = 2048
			case "high":
				targetBudget = 8192
			default:
				targetBudget = 4096
			}
		}
		if targetBudget < 1024 {
			targetBudget = 1024
		}
		if maxTokens > 0 {
			if maxTokens <= 1024 {
				return nil, nil
			}
			if targetBudget >= maxTokens {
				targetBudget = maxTokens - 1
				if targetBudget < 1024 {
					return nil, nil
				}
			}
		}
		return &anthropicclient.ThinkingConfig{Type: "enabled", BudgetTokens: targetBudget}, nil

	default:
		return nil, nil
	}
}

func parseIntFromMetadata(val any) int {
	switch v := val.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
}

func parseDataURI(uri string) (mediaType, base64Data string, err error) {
	prefix := "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", errors.New("missing data: scheme")
	}
	rest := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(rest, ",", 2)
	if len(parts) != 2 {
		return "", "", errors.New("malformed data URI")
	}
	header := parts[0]
	base64Data = parts[1]
	if !strings.HasSuffix(header, ";base64") {
		return "", "", errors.New("data URI is not base64 encoded")
	}
	mediaType = strings.TrimSuffix(header, ";base64")
	if mediaType == "" {
		mediaType = "image/png"
	}
	return mediaType, base64Data, nil
}
