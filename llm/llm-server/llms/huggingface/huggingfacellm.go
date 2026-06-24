package huggingface

import (
	"context"
	"errors"
	"nudgebee/llm/llms/huggingface/huggingfaceclient"
	"os"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

var (
	ErrEmptyResponse            = errors.New("empty response")
	ErrMissingToken             = errors.New("missing the Hugging Face API token. Set it in the HUGGINGFACEHUB_API_TOKEN environment variable") //nolint:lll
	ErrUnexpectedResponseLength = errors.New("unexpected length of response")
)

type LLM struct {
	CallbacksHandler callbacks.Handler
	client           *huggingfaceclient.Client
}

var _ llms.Model = (*LLM)(nil)

// Call implements the LLM interface.
func (o *LLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, o, prompt, options...)
}

func generateMessageContent(msges []llms.MessageContent) string {
	builder := strings.Builder{}
	for _, msg := range msges {
		if msg.Role == llms.ChatMessageTypeAI {
			builder.WriteString("<|assistant|>\n")
		} else if msg.Role == llms.ChatMessageTypeHuman {
			builder.WriteString("<|user|>\n")
		} else if msg.Role == llms.ChatMessageTypeSystem {
			// builder.WriteString("<|system|>\n")
			continue
		}
		builder.WriteString(msg.Parts[0].(llms.TextContent).Text)
		builder.WriteString("<|end|>\n")
	}
	builder.WriteString("<|assistant|>")
	return builder.String()
}

func parseMessageResponse(msges string) string {
	if msges == "" {
		return msges
	}
	splits := strings.Split(msges, "<|assistant|>")
	response := strings.TrimSpace(splits[len(splits)-1])
	response = strings.TrimSuffix(response, "<|end|>")
	return response
}

// Map our internal MessageContent to OpenAI chat roles. The host's chat
// template (vLLM/TGI/Ollama) handles all the wrapping — we just provide
// roles so it knows where system / user / assistant boundaries are.
//
// Qwen3.x and several other recent chat templates reject any payload that
// has a system message anywhere but position 0 ("System message must be at
// the beginning"). Collect all system messages, merge them with "\n\n",
// and emit as a single leading system message; keep all other messages in
// their original order.
func toChatMessages(msges []llms.MessageContent) []huggingfaceclient.ChatMessage {
	var systemParts []string
	rest := make([]huggingfaceclient.ChatMessage, 0, len(msges))
	for _, m := range msges {
		var content string
		for _, p := range m.Parts {
			if tp, ok := p.(llms.TextContent); ok {
				content += tp.Text
			}
		}
		if content == "" {
			continue
		}
		switch m.Role {
		case llms.ChatMessageTypeSystem:
			systemParts = append(systemParts, content)
		case llms.ChatMessageTypeAI:
			rest = append(rest, huggingfaceclient.ChatMessage{Role: "assistant", Content: content})
		default:
			rest = append(rest, huggingfaceclient.ChatMessage{Role: "user", Content: content})
		}
	}
	if len(systemParts) == 0 {
		return rest
	}
	out := make([]huggingfaceclient.ChatMessage, 0, 1+len(rest))
	out = append(out, huggingfaceclient.ChatMessage{Role: "system", Content: strings.Join(systemParts, "\n\n")})
	out = append(out, rest...)
	return out
}

// GenerateContent implements the Model interface.
func (o *LLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) { //nolint: lll, cyclop, whitespace

	if o.CallbacksHandler != nil {
		o.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := &llms.CallOptions{Model: defaultModel}
	for _, opt := range options {
		opt(opts)
	}

	req := &huggingfaceclient.InferenceRequest{
		Model:             o.client.Model,
		Adapter:           o.client.Adapter,
		Task:              huggingfaceclient.InferenceTaskTextGeneration,
		Temperature:       opts.Temperature,
		TopP:              opts.TopP,
		TopK:              opts.TopK,
		MinLength:         opts.MinLength,
		MaxLength:         opts.MaxLength,
		RepetitionPenalty: opts.RepetitionPenalty,
		Seed:              opts.Seed,
	}
	if o.client.APIType == "openai" {
		req.Messages = toChatMessages(messages)
	} else {
		req.Prompt = generateMessageContent(messages)
	}
	result, err := o.client.RunInference(ctx, req)
	if err != nil {
		if o.CallbacksHandler != nil {
			o.CallbacksHandler.HandleLLMError(ctx, err)
		}
		return nil, err
	}

	choice := &llms.ContentChoice{
		Content: parseMessageResponse(result.Text),
	}
	if result.PromptTokens > 0 || result.CompletionTokens > 0 {
		// Key names match the extractor in agents/core/llm_common.go:653-663
		// (uses Anthropic-style InputTokens/OutputTokens, not OpenAI naming).
		choice.GenerationInfo = map[string]any{
			"InputTokens":  result.PromptTokens,
			"OutputTokens": result.CompletionTokens,
			"total_tokens": result.TotalTokens,
		}
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{choice}}, nil
}

func New(opts ...Option) (*LLM, error) {
	options := &options{
		token: os.Getenv(tokenEnvVarName),
		model: defaultModel,
		url:   defaultURL,
	}

	for _, opt := range opts {
		opt(options)
	}

	if len(options.token) == 0 {
		return nil, ErrMissingToken
	}

	c, err := huggingfaceclient.NewWithAPIType(options.token, options.model, options.url, options.adapter, options.apiType)
	if err != nil {
		return nil, err
	}

	return &LLM{
		client: c,
	}, nil
}

// CreateEmbedding creates embeddings for the given input texts.
func (o *LLM) CreateEmbedding(
	ctx context.Context,
	inputTexts []string,
	model string,
	task string,
) ([][]float32, error) {
	embeddings, err := o.client.CreateEmbedding(ctx, model, task, &huggingfaceclient.EmbeddingRequest{
		Inputs: inputTexts,
		Options: map[string]any{
			"use_gpu":        false,
			"wait_for_model": true,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, ErrEmptyResponse
	}
	if len(inputTexts) != len(embeddings) {
		return embeddings, ErrUnexpectedResponseLength
	}
	return embeddings, nil
}
