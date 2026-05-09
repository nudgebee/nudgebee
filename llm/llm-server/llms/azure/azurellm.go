package azure

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/llms/azure/azureclient"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

var (
	ErrEmptyResponse              = errors.New("no response")
	ErrMissingToken               = errors.New("missing the AzureAi API key, set it in the AZUREAI_API_KEY environment variable") //nolint:lll
	ErrMissingAzureModel          = errors.New("model needs to be provided when using Azure API")
	ErrMissingAzureEmbeddingModel = errors.New("embeddings model needs to be provided when using Azure API")

	ErrUnexpectedResponseLength = errors.New("unexpected length of response")
)

type ChatMessage = azureclient.ChatMessage

type LLM struct {
	CallbacksHandler callbacks.Handler
	client           *azureclient.Client
}

const (
	RoleSystem    = "system"
	RoleAssistant = "assistant"
	RoleUser      = "user"
	RoleFunction  = "function"
	RoleTool      = "tool"
)

var _ llms.Model = (*LLM)(nil)

// New returns a new OpenAI LLM.
func New(opts ...Option) (*LLM, error) {
	opt, c, err := newClient(opts...)
	if err != nil {
		return nil, err
	}
	return &LLM{
		client:           c,
		CallbacksHandler: opt.callbackHandler,
	}, err
}

// newClient creates an instance of the internal client.
func newClient(opts ...Option) (*options, *azureclient.Client, error) {
	options := &options{
		apiType:    APIType(azureclient.APITypeAzure),
		httpClient: common.HttpClient(),
	}

	for _, opt := range opts {
		opt(options)
	}

	if azureclient.IsAzure(azureclient.APIType(options.apiType)) && options.apiVersion == "" {
		options.apiVersion = DefaultAPIVersion
		if options.model == "" {
			return options, nil, ErrMissingAzureModel
		}
		// if options.embeddingModel == "" {
		// 	return options, nil, ErrMissingAzureEmbeddingModel
		// }
	}

	if len(options.token) == 0 {
		return options, nil, ErrMissingToken
	}

	cli, err := azureclient.New(options.token, options.model, options.baseURL, options.adapter,
		azureclient.APIType(options.apiType), options.apiVersion, options.httpClient)
	return options, cli, err
}

// Call requests a completion for the given prompt.
func (o *LLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, o, prompt, options...)
}

// GenerateContent implements the Model interface.
func (o *LLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) { //nolint: lll, cyclop, goerr113, funlen
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	chatMsgs := make([]*ChatMessage, 0, len(messages))
	for _, mc := range messages {
		msg := &ChatMessage{MultiContent: mc.Parts}
		switch mc.Role {
		case llms.ChatMessageTypeSystem:
			msg.Role = RoleSystem
		case llms.ChatMessageTypeAI:
			msg.Role = RoleAssistant
		case llms.ChatMessageTypeHuman:
			msg.Role = RoleUser
		case llms.ChatMessageTypeGeneric:
			msg.Role = RoleUser
		case llms.ChatMessageTypeFunction:
			msg.Role = RoleFunction
		case llms.ChatMessageTypeTool:
			msg.Role = RoleTool
			// Here we extract tool calls from the message and populate the ToolCalls field.

			// parse mc.Parts (which should have one entry of type ToolCallResponse) and populate msg.Content and msg.ToolCallID
			if len(mc.Parts) != 1 {
				return nil, fmt.Errorf("expected exactly one part for role %v, got %v", mc.Role, len(mc.Parts))
			}
			switch p := mc.Parts[0].(type) {
			case llms.ToolCallResponse:
				msg.ToolCallID = p.ToolCallID
				msg.Content = p.Content
			default:
				return nil, fmt.Errorf("expected part of type ToolCallResponse for role %v, got %T", mc.Role, mc.Parts[0])
			}

		default:
			return nil, fmt.Errorf("role %v not supported", mc.Role)
		}

		// Here we extract tool calls from the message and populate the ToolCalls field.
		newParts, toolCalls := ExtractToolParts(msg)
		msg.MultiContent = newParts
		msg.ToolCalls = toolCallsFromToolCalls(toolCalls)

		chatMsgs = append(chatMsgs, msg)
	}
	req := &azureclient.ChatRequest{
		Model:            o.client.Model,
		StopWords:        opts.StopWords,
		Messages:         chatMsgs,
		Adapter:          o.client.Adapter,
		StreamingFunc:    opts.StreamingFunc,
		Temperature:      opts.Temperature,
		MaxTokens:        opts.MaxTokens,
		N:                opts.N,
		FrequencyPenalty: opts.FrequencyPenalty,
		PresencePenalty:  opts.PresencePenalty,

		ToolChoice:           opts.ToolChoice,
		FunctionCallBehavior: azureclient.FunctionCallBehavior(opts.FunctionCallBehavior),
		Seed:                 opts.Seed,
		Metadata:             opts.Metadata,
	}
	if opts.JSONMode {
		req.ResponseFormat = ResponseFormatJSON
	}

	// since req.Functions is deprecated, we need to use the new Tools API.
	for _, fn := range opts.Functions {
		req.Tools = append(req.Tools, azureclient.Tool{
			Type: "function",
			Function: azureclient.FunctionDefinition{
				Name:        fn.Name,
				Description: fn.Description,
				Parameters:  fn.Parameters,
			},
		})
	}
	// if opts.Tools is not empty, append them to req.Tools
	for _, tool := range opts.Tools {
		t, err := toolFromTool(tool)
		if err != nil {
			return nil, fmt.Errorf("failed to convert llms tool to openai tool: %w", err)
		}
		req.Tools = append(req.Tools, t)
	}

	result, err := o.client.CreateChat(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, ErrEmptyResponse
	}

	choices := make([]*llms.ContentChoice, len(result.Choices))
	for i, c := range result.Choices {
		choices[i] = &llms.ContentChoice{
			Content:    c.Message.Content,
			StopReason: fmt.Sprint(c.FinishReason),
			GenerationInfo: map[string]any{
				"CompletionTokens": result.Usage.CompletionTokens,
				"PromptTokens":     result.Usage.PromptTokens,
				"TotalTokens":      result.Usage.TotalTokens,
			},
		}

		// Legacy function call handling
		if c.FinishReason == "function_call" {
			choices[i].FuncCall = &llms.FunctionCall{
				Name:      c.Message.FunctionCall.Name,
				Arguments: c.Message.FunctionCall.Arguments,
			}
		}
		for _, tool := range c.Message.ToolCalls {
			choices[i].ToolCalls = append(choices[i].ToolCalls, llms.ToolCall{
				ID:   tool.ID,
				Type: string(tool.Type),
				FunctionCall: &llms.FunctionCall{
					Name:      tool.Function.Name,
					Arguments: tool.Function.Arguments,
				},
			})
		}
		// populate legacy single-function call field for backwards compatibility
		if len(choices[i].ToolCalls) > 0 {
			choices[i].FuncCall = choices[i].ToolCalls[0].FunctionCall
		}
	}
	response := &llms.ContentResponse{Choices: choices}
	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, response)
	}
	return response, nil
}

// CreateEmbedding creates embeddings for the given input texts.
func (o *LLM) CreateEmbedding(ctx context.Context, inputTexts []string) ([][]float32, error) {
	embeddings, err := o.client.CreateEmbedding(ctx, &azureclient.EmbeddingRequest{
		Input: inputTexts,
		Model: o.client.EmbeddingModel,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openai embeddings: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, ErrEmptyResponse
	}
	if len(inputTexts) != len(embeddings) {
		return embeddings, ErrUnexpectedResponseLength
	}
	return embeddings, nil
}

// ExtractToolParts extracts the tool parts from a message.
func ExtractToolParts(msg *ChatMessage) ([]llms.ContentPart, []llms.ToolCall) {
	var content []llms.ContentPart
	var toolCalls []llms.ToolCall
	for _, part := range msg.MultiContent {
		switch p := part.(type) {
		case llms.TextContent:
			content = append(content, p)
		case llms.ImageURLContent:
			content = append(content, p)
		case llms.BinaryContent:
			content = append(content, p)
		case llms.ToolCall:
			toolCalls = append(toolCalls, p)
		}
	}
	return content, toolCalls
}

// toolFromTool converts an llms.Tool to a Tool.
func toolFromTool(t llms.Tool) (azureclient.Tool, error) {
	tool := azureclient.Tool{
		Type: azureclient.ToolType(t.Type),
	}
	switch t.Type {
	case string(azureclient.ToolTypeFunction):
		tool.Function = azureclient.FunctionDefinition{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
	default:
		return azureclient.Tool{}, fmt.Errorf("tool type %v not supported", t.Type)
	}
	return tool, nil
}

// toolCallsFromToolCalls converts a slice of llms.ToolCall to a slice of ToolCall.
func toolCallsFromToolCalls(tcs []llms.ToolCall) []azureclient.ToolCall {
	toolCalls := make([]azureclient.ToolCall, len(tcs))
	for i, tc := range tcs {
		toolCalls[i] = toolCallFromToolCall(tc)
	}
	return toolCalls
}

// toolCallFromToolCall converts an llms.ToolCall to a ToolCall.
func toolCallFromToolCall(tc llms.ToolCall) azureclient.ToolCall {
	return azureclient.ToolCall{
		ID:   tc.ID,
		Type: azureclient.ToolType(tc.Type),
		Function: azureclient.ToolFunction{
			Name:      tc.FunctionCall.Name,
			Arguments: tc.FunctionCall.Arguments,
		},
	}
}
