package bedrockclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/tmc/langchaingo/llms"
)

// Ref: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
// Also: https://docs.anthropic.com/claude/reference/messages_post

type anthropicThinking struct {
	Type         string `json:"type"`                    // "enabled" | "disabled" | "adaptive"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // For Claude 3.7 - 4.5
}

type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low" | "medium" | "high" for Claude 4.6+
}

// anthropicBinGenerationInputSource is the source of the content.
type anthropicBinGenerationInputSource struct {
	// The type of the source. Required
	// One of: "base64"
	Type string `json:"type"`
	// The MIME type of the source. Required
	// One of: []"image/jpeg", "image/png", "image/gif", "image/bmp", "image/webp"]
	MediaType string `json:"media_type"`
	// The data of the source. Required
	// For example if type is "base64" then data is a base64 encoded string
	Data string `json:"data"`
}

// anthropicTextGenerationInputContent is the content of the message.
type anthropicTextGenerationInputContent struct {
	// The type of the content. Required
	// One of: "text", "image"
	Type string `json:"type"`
	// The text content of the message. Required if type is "text"
	Text string `json:"text,omitempty"`
	// The source of the content. Required if type is "image"
	Source *anthropicBinGenerationInputSource `json:"source,omitempty"`
}

// anthropicTextGenerationInputMessage is the message to the model.
type anthropicTextGenerationInputMessage struct {
	// Conversational role of the message. Required
	// One of: "user", "assistant"
	Role string `json:"role"`
	// The content of the message. Required
	Content []anthropicTextGenerationInputContent `json:"content"`
}

// anthropicTextGenerationInput is the input to the model.
type anthropicTextGenerationInput struct {
	// The version of the model to use. Required
	AnthropicVersion string `json:"anthropic_version"`
	// The maximum number of tokens to generate per result. Required
	MaxTokens int `json:"max_tokens"`
	// The system prompt to use. Optional
	System string `json:"system,omitempty"`
	// The messages to use. Required
	Messages []*anthropicTextGenerationInputMessage `json:"messages"`
	// The amount of randomness injected into the response. Optional, default = 1
	Temperature float64 `json:"temperature,omitempty"`
	// The probability mass from which tokens are sampled. Optional, default = 1
	TopP float64 `json:"top_p,omitempty"`
	// Only sample from the top K options for each subsequent token.
	// Use top_k to remove long tail low probability responses.
	// Optional, default = 250
	TopK int `json:"top_k,omitempty"`
	// Sequences that will cause the model to stop generating tokens. Optional
	StopSequences []string `json:"stop_sequences,omitempty"`
	// Extended thinking configuration
	Thinking *anthropicThinking `json:"thinking,omitempty"`
	// Output reasoning effort configuration
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	// Tool definitions
	Tools []any `json:"tools,omitempty"`
}

// anthropicTextGenerationOutput is the generated output.
type anthropicTextGenerationOutput struct {
	// Type of the content.
	// For messages, it is "message"
	Type string `json:"type"`
	// Conversational role of the generated message.
	// This will always be "assistant".
	Role string `json:"role"`
	// This is an array of content blocks, each of which has a type that determines its shape.
	Content []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		Thinking string          `json:"thinking,omitempty"`
		ID       string          `json:"id,omitempty"`
		Name     string          `json:"name,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	// The reason for the completion of the generation.
	// One of: ["end_turn", "max_tokens", "stop_sequence", "tool_use"]
	StopReason string `json:"stop_reason"`
	// Which custom stop sequence was matched, if any.
	StopSequence string `json:"stop_sequence"`
	Usage        struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		ThinkingTokens           int `json:"thinking_tokens,omitempty"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage"`
}

// Finish reason for the completion of the generation.
const (
	AnthropicCompletionReasonEndTurn      = "end_turn"
	AnthropicCompletionReasonMaxTokens    = "max_tokens"
	AnthropicCompletionReasonStopSequence = "stop_sequence"
	AnthropicCompletionReasonToolUse      = "tool_use"
)

// The latest version of the model.
const (
	AnthropicLatestVersion = "bedrock-2023-05-31"
)

// Role attribute for the anthropic message.
const (
	AnthropicSystem        = "system"
	AnthropicRoleUser      = "user"
	AnthropicRoleAssistant = "assistant"
)

// Type attribute for the anthropic message.
const (
	AnthropicMessageTypeText  = "text"
	AnthropicMessageTypeImage = "image"
)

func createAnthropicCompletion(ctx context.Context,
	client *bedrockruntime.Client,
	modelID string,
	messages []Message,
	options llms.CallOptions,
) (*llms.ContentResponse, error) {
	inputContents, systemPrompt, err := processInputMessagesAnthropic(messages)
	if err != nil {
		return nil, err
	}

	maxTokens := getMaxTokens(options.MaxTokens, 2048)
	thinking, outputConfig := resolveAnthropicThinking(modelID, options, maxTokens)

	input := anthropicTextGenerationInput{
		AnthropicVersion: AnthropicLatestVersion,
		MaxTokens:        maxTokens,
		System:           systemPrompt,
		Messages:         inputContents,
		StopSequences:    options.StopWords,
		Thinking:         thinking,
		OutputConfig:     outputConfig,
	}

	// Extended thinking is incompatible with modified temperature / top_p / top_k.
	isThinkingActive := (thinking != nil && thinking.Type != "disabled") || (outputConfig != nil && outputConfig.Effort != "")
	if !isThinkingActive {
		input.Temperature = options.Temperature
		input.TopP = options.TopP
		input.TopK = options.TopK
	}

	if len(options.Tools) > 0 {
		tools := make([]any, 0, len(options.Tools))
		for _, t := range options.Tools {
			if t.Function != nil {
				tools = append(tools, map[string]any{
					"name":         t.Function.Name,
					"description":  t.Function.Description,
					"input_schema": t.Function.Parameters,
				})
			}
		}
		input.Tools = tools
	}

	body, err := common.MarshalJson(input)
	if err != nil {
		return nil, err
	}

	if options.StreamingFunc != nil {
		modelInput := &bedrockruntime.InvokeModelWithResponseStreamInput{
			ModelId:     aws.String(modelID),
			Accept:      aws.String("*/*"),
			ContentType: aws.String("application/json"),
			Body:        body,
		}
		return parseStreamingCompletionResponse(ctx, client, modelInput, options)
	}

	modelInput := &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		Accept:      aws.String("*/*"),
		ContentType: aws.String("application/json"),
		Body:        body,
	}
	resp, err := client.InvokeModel(ctx, modelInput)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Body) == 0 {
		return nil, errors.New("empty response from Bedrock")
	}

	var output anthropicTextGenerationOutput
	err = common.UnmarshalJson(resp.Body, &output)
	if err != nil {
		return nil, err
	}

	if len(output.Content) == 0 {
		return nil, errors.New("no results")
	}

	genInfo := map[string]any{
		"input_tokens":  output.Usage.InputTokens,
		"output_tokens": output.Usage.OutputTokens,
	}
	if output.Usage.ThinkingTokens > 0 {
		genInfo["ThinkingTokens"] = output.Usage.ThinkingTokens
		genInfo["thinking_tokens"] = output.Usage.ThinkingTokens
	}
	if output.Usage.CacheReadInputTokens > 0 {
		genInfo["CacheReadInputTokens"] = output.Usage.CacheReadInputTokens
	}
	if output.Usage.CacheCreationInputTokens > 0 {
		genInfo["CacheCreationInputTokens"] = output.Usage.CacheCreationInputTokens
	}

	var combinedText strings.Builder
	var combinedThinking strings.Builder
	var toolCalls []llms.ToolCall
	for _, c := range output.Content {
		switch c.Type {
		case "text":
			combinedText.WriteString(c.Text)
		case "thinking":
			combinedThinking.WriteString(c.Thinking)
		case "tool_use":
			args := string(c.Input)
			if strings.TrimSpace(args) == "" || args == "null" {
				args = "{}"
			}
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   c.ID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      c.Name,
					Arguments: args,
				},
			})
		}
	}
	if combinedThinking.Len() > 0 {
		genInfo["thinking"] = combinedThinking.String()
	}

	Contentchoices := []*llms.ContentChoice{
		{
			Content:        combinedText.String(),
			StopReason:     output.StopReason,
			GenerationInfo: genInfo,
			ToolCalls:      toolCalls,
		},
	}
	if len(toolCalls) > 0 {
		Contentchoices[0].FuncCall = toolCalls[0].FunctionCall
	}
	return &llms.ContentResponse{
		Choices: Contentchoices,
	}, nil
}

type streamingCompletionResponseChunk struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"content_block"`
	Delta struct {
		Type         string `json:"type"`
		Text         string `json:"text"`
		Thinking     string `json:"thinking,omitempty"`
		PartialJSON  string `json:"partial_json"`
		StopReason   string `json:"stop_reason"`
		StopSequence any    `json:"stop_sequence"`
	} `json:"delta"`
	AmazonBedrockInvocationMetrics struct {
		InputTokenCount   int `json:"inputTokenCount"`
		OutputTokenCount  int `json:"outputTokenCount"`
		InvocationLatency int `json:"invocationLatency"`
		FirstByteLatency  int `json:"firstByteLatency"`
	} `json:"amazon-bedrock-invocationMetrics"`
	Usage struct {
		OutputTokens   int `json:"output_tokens"`
		ThinkingTokens int `json:"thinking_tokens,omitempty"`
	} `json:"usage"`
	Message struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Role         string `json:"role"`
		Content      []any  `json:"content"`
		Model        string `json:"model"`
		StopReason   any    `json:"stop_reason"`
		StopSequence any    `json:"stop_sequence"`
		Usage        struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			ThinkingTokens           int `json:"thinking_tokens,omitempty"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
		} `json:"usage"`
	} `json:"message"`
}

type bedrockBlockAccumulator struct {
	blockType       string
	id              string
	name            string
	textBuilder     strings.Builder
	thinkingBuilder strings.Builder
	jsonBuilder     strings.Builder
}

func parseStreamingCompletionResponse(ctx context.Context, client *bedrockruntime.Client, modelInput *bedrockruntime.InvokeModelWithResponseStreamInput, options llms.CallOptions) (*llms.ContentResponse, error) {
	output, err := client.InvokeModelWithResponseStream(ctx, modelInput)
	if err != nil {
		return nil, err
	}
	stream := output.GetStream()
	if stream == nil {
		return nil, errors.New("failed to get stream from response")
	}
	defer func() {
		if err := stream.Close(); err != nil {
			fmt.Printf("Error closing stream: %v\n", err)
		}
	}()

	contentchoices := []*llms.ContentChoice{{GenerationInfo: map[string]any{}}}
	blockMap := make(map[int]*bedrockBlockAccumulator)

	for e := range stream.Events() {
		if err = stream.Err(); err != nil {
			return nil, err
		}

		if v, ok := e.(*types.ResponseStreamMemberChunk); ok {
			var resp streamingCompletionResponseChunk
			err := json.NewDecoder(bytes.NewReader(v.Value.Bytes)).Decode(&resp)
			if err != nil {
				return nil, err
			}

			switch resp.Type {
			case "message_start":
				contentchoices[0].GenerationInfo["input_tokens"] = resp.Message.Usage.InputTokens
				if resp.Message.Usage.CacheReadInputTokens > 0 {
					contentchoices[0].GenerationInfo["CacheReadInputTokens"] = resp.Message.Usage.CacheReadInputTokens
				}
				if resp.Message.Usage.CacheCreationInputTokens > 0 {
					contentchoices[0].GenerationInfo["CacheCreationInputTokens"] = resp.Message.Usage.CacheCreationInputTokens
				}

			case "content_block_start":
				acc := &bedrockBlockAccumulator{
					blockType: resp.ContentBlock.Type,
					id:        resp.ContentBlock.ID,
					name:      resp.ContentBlock.Name,
				}
				if resp.ContentBlock.Text != "" {
					acc.textBuilder.WriteString(resp.ContentBlock.Text)
				}
				if resp.ContentBlock.Thinking != "" {
					acc.thinkingBuilder.WriteString(resp.ContentBlock.Thinking)
				}
				blockMap[resp.Index] = acc

			case "content_block_delta":
				acc, exists := blockMap[resp.Index]
				if !exists {
					acc = &bedrockBlockAccumulator{}
					blockMap[resp.Index] = acc
				}

				if resp.Delta.Type == "text_delta" || (resp.Delta.Text != "" && resp.Delta.Type != "thinking_delta") {
					if acc.blockType == "" {
						acc.blockType = "text"
					}
					acc.textBuilder.WriteString(resp.Delta.Text)
					if options.StreamingFunc != nil {
						if err = options.StreamingFunc(ctx, []byte(resp.Delta.Text)); err != nil {
							return nil, err
						}
					}
				} else if resp.Delta.Type == "thinking_delta" || resp.Delta.Thinking != "" {
					if acc.blockType == "" {
						acc.blockType = "thinking"
					}
					acc.thinkingBuilder.WriteString(resp.Delta.Thinking)
				} else if resp.Delta.Type == "input_json_delta" || resp.Delta.PartialJSON != "" {
					if acc.blockType == "" {
						acc.blockType = "tool_use"
					}
					acc.jsonBuilder.WriteString(resp.Delta.PartialJSON)
				}

			case "message_delta":
				contentchoices[0].StopReason = resp.Delta.StopReason
				contentchoices[0].GenerationInfo["output_tokens"] = resp.Usage.OutputTokens
				if resp.Usage.ThinkingTokens > 0 {
					contentchoices[0].GenerationInfo["ThinkingTokens"] = resp.Usage.ThinkingTokens
					contentchoices[0].GenerationInfo["thinking_tokens"] = resp.Usage.ThinkingTokens
				}
			}
		}
	}
	if err = stream.Err(); err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(blockMap))
	for idx := range blockMap {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	var textBuilder strings.Builder
	var thinkingBuilder strings.Builder
	var toolCalls []llms.ToolCall
	for _, idx := range indices {
		acc := blockMap[idx]
		switch acc.blockType {
		case "thinking":
			thinkingBuilder.WriteString(acc.thinkingBuilder.String())
		case "tool_use":
			args := acc.jsonBuilder.String()
			if strings.TrimSpace(args) == "" || args == "null" {
				args = "{}"
			}
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   acc.id,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      acc.name,
					Arguments: args,
				},
			})
		default:
			textBuilder.WriteString(acc.textBuilder.String())
		}
	}
	if thinkingBuilder.Len() > 0 {
		contentchoices[0].GenerationInfo["thinking"] = thinkingBuilder.String()
	}

	contentchoices[0].Content = textBuilder.String()
	contentchoices[0].ToolCalls = toolCalls
	if len(toolCalls) > 0 {
		contentchoices[0].FuncCall = toolCalls[0].FunctionCall
	}

	return &llms.ContentResponse{
		Choices: contentchoices,
	}, nil
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

func resolveAnthropicThinking(modelID string, options llms.CallOptions, maxTokens int) (*anthropicThinking, *anthropicOutputConfig) {
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

	switch {
	case major > 4 || (major == 4 && minor >= 6):
		if lvlLower == "" {
			return nil, nil
		}
		if lvlLower == "none" {
			if strings.Contains(mLower, "opus") {
				return nil, nil
			}
			return &anthropicThinking{Type: "disabled"}, nil
		}
		effort := lvlLower
		if effort != "low" && effort != "medium" && effort != "high" {
			effort = "medium"
		}
		return &anthropicThinking{Type: "adaptive"}, &anthropicOutputConfig{Effort: effort}

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
		return &anthropicThinking{Type: "enabled", BudgetTokens: targetBudget}, nil

	default:
		return nil, nil
	}
}

// process the input messages to anthropic supported input
// returns the input content and system prompt.
func processInputMessagesAnthropic(messages []Message) ([]*anthropicTextGenerationInputMessage, string, error) {
	chunkedMessages := make([][]Message, 0, len(messages))
	currentChunk := make([]Message, 0, len(messages))
	var lastRole llms.ChatMessageType
	for _, message := range messages {
		if message.Role != lastRole {
			if len(currentChunk) > 0 {
				chunkedMessages = append(chunkedMessages, currentChunk)
			}
			currentChunk = make([]Message, 0, len(messages))
		}
		currentChunk = append(currentChunk, message)
		lastRole = message.Role
	}
	if len(currentChunk) > 0 {
		chunkedMessages = append(chunkedMessages, currentChunk)
	}

	systemPrompt := ""
	inputMessages := make([]*anthropicTextGenerationInputMessage, 0, len(chunkedMessages))
	for _, chunk := range chunkedMessages {
		var role string
		switch chunk[0].Role {
		case llms.ChatMessageTypeSystem:
			for _, message := range chunk {
				if strings.TrimSpace(message.Content) == "" {
					continue
				}
				if systemPrompt != "" {
					systemPrompt += "\n\n"
				}
				systemPrompt += message.Content
			}
			continue
		case llms.ChatMessageTypeGeneric, llms.ChatMessageTypeHuman:
			role = AnthropicRoleUser
		case llms.ChatMessageTypeAI:
			role = AnthropicRoleAssistant
		case llms.ChatMessageTypeTool:
			role = AnthropicRoleUser
		default:
			return nil, "", fmt.Errorf("role %v not supported", chunk[0].Role)
		}

		contents := make([]anthropicTextGenerationInputContent, 0, len(chunk))
		for _, message := range chunk {
			switch message.Type {
			case AnthropicMessageTypeText, "tool_call_response", "tool_call":
				if strings.TrimSpace(message.Content) == "" {
					continue
				}
				contents = append(contents, anthropicTextGenerationInputContent{
					Type: AnthropicMessageTypeText,
					Text: message.Content,
				})
			case AnthropicMessageTypeImage:
				contents = append(contents, anthropicTextGenerationInputContent{
					Type: AnthropicMessageTypeImage,
					Source: &anthropicBinGenerationInputSource{
						Type:      "base64",
						MediaType: message.MimeType,
						Data:      base64.StdEncoding.EncodeToString([]byte(message.Content)),
					},
				})
			default:
				if strings.TrimSpace(message.Content) == "" {
					continue
				}
				contents = append(contents, anthropicTextGenerationInputContent{
					Type: AnthropicMessageTypeText,
					Text: message.Content,
				})
			}
		}
		if len(contents) == 0 {
			continue
		}

		inputMessages = append(inputMessages, &anthropicTextGenerationInputMessage{
			Role:    role,
			Content: contents,
		})
	}

	return inputMessages, systemPrompt, nil
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
