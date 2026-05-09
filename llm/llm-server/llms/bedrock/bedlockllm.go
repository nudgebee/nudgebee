package bedrock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/llms/bedrock/internal/bedrockclient"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

const defaultModel = ModelAmazonTitanTextLiteV1

// LLM is a Bedrock LLM implementation.
type LLM struct {
	modelID          string
	client           *bedrockclient.Client
	CallbacksHandler callbacks.Handler
}

// New creates a new Bedrock LLM implementation.
func New(opts ...Option) (*LLM, error) {
	o, c, err := newClient(opts...)
	if err != nil {
		return nil, err
	}
	return &LLM{
		client:           c,
		modelID:          o.modelID,
		CallbacksHandler: o.callbackHandler,
	}, nil
}

func newClient(opts ...Option) (*options, *bedrockclient.Client, error) {
	options := &options{
		modelID: defaultModel,
	}

	for _, opt := range opts {
		opt(options)
	}

	if options.client == nil {
		cfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return options, nil, err
		}
		options.client = bedrockruntime.NewFromConfig(cfg)
	}

	return options, bedrockclient.NewClient(options.client), nil
}

// Call implements llms.Model.
func (l *LLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, l, prompt, options...)
}

// GenerateContent implements llms.Model.
func (l *LLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if l.CallbacksHandler != nil {
		l.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{
		Model: l.modelID,
	}
	for _, opt := range options {
		opt(&opts)
	}

	m, err := processMessages(ctx, messages)
	if err != nil {
		return nil, err
	}

	res, err := l.client.CreateCompletion(ctx, opts.Model, m, opts)
	if err != nil {
		if l.CallbacksHandler != nil {
			l.CallbacksHandler.HandleLLMError(ctx, err)
		}
		return nil, err
	}

	if l.CallbacksHandler != nil {
		l.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, res)
	}

	return res, nil
}

func processMessages(ctx context.Context, messages []llms.MessageContent) ([]bedrockclient.Message, error) {
	bedrockMsgs := make([]bedrockclient.Message, 0, len(messages))

	for _, m := range messages {
		for _, part := range m.Parts {
			switch part := part.(type) {
			case llms.TextContent:
				bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
					Role:    m.Role,
					Content: part.Text,
					Type:    "text",
				})
			case llms.BinaryContent:
				bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
					Role:     m.Role,
					Content:  string(part.Data),
					MimeType: part.MIMEType,
					Type:     "image",
				})
			case llms.ImageURLContent:
				mimeType, data, dlErr := downloadImageForBedrock(ctx, part.URL)
				if dlErr != nil {
					slog.Warn("bedrock: failed to download image for model",
						"url", part.URL, "error", dlErr)
					// Graceful fallback: generic note — don't leak URL or error to model
					bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
						Role:    m.Role,
						Content: "[An image attachment could not be loaded.]",
						Type:    "text",
					})
					continue
				}
				bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
					Role:     m.Role,
					Content:  string(data),
					MimeType: mimeType,
					Type:     "image",
				})
			case llms.ToolCallResponse:
				// workaround for json serialization
				toolCallMap := map[string]any{
					"content":      part.Content,
					"tool_call_id": part.ToolCallID,
					"name":         part.Name,
				}
				toolCallResponse, _ := common.MarshalJson(toolCallMap)
				bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
					Role:    llms.ChatMessageTypeTool,
					Content: string(toolCallResponse),
					Type:    "tool_call_response",
				})
			case llms.ToolCall:
				toolCallMap := map[string]any{
					"id":   part.ID,
					"type": part.Type,
					"function": map[string]any{
						"name":      part.FunctionCall.Name,
						"arguments": part.FunctionCall.Arguments,
					},
				}
				toolCall, _ := common.MarshalJson(toolCallMap)
				bedrockMsgs = append(bedrockMsgs, bedrockclient.Message{
					Role:    llms.ChatMessageTypeTool,
					Content: string(toolCall),
					Type:    "tool_call",
				})
			default:
				return nil, errors.New("unsupported message type")
			}
		}
	}
	return bedrockMsgs, nil
}

// maxImageDownloadBytes caps the size of images downloaded from URLs to prevent OOM.
const maxImageDownloadBytes = 10 * 1024 * 1024 // 10 MB

// downloadImageForBedrock fetches an image from a URL and returns its MIME type and raw bytes.
// Bedrock requires inline binary data — it does not support image URLs directly.
//
// SSRF defense-in-depth: the API ingress path normally converts URL inputs to
// inline base64 inside ValidateImages, so this code path is reached only when a
// future caller hands an ImageURLContent to Bedrock directly (history replay,
// agent-generated content, planner refactors). It uses the same SSRF-safe
// fetcher (private-IP rejection at dial time, redirect re-validation) as the
// API ingress.
func downloadImageForBedrock(ctx context.Context, url string) (string, []byte, error) {
	mimeType, data, err := common.FetchImageSafely(ctx, url, common.SafeFetchOptions{
		MaxSizeBytes:        maxImageDownloadBytes,
		Timeout:             30 * time.Second,
		AllowedMIMEPrefixes: []string{"image/"},
	})
	if err != nil {
		return "", nil, fmt.Errorf("bedrock: failed to fetch image: %w", err)
	}
	return mimeType, data, nil
}

var _ llms.Model = (*LLM)(nil)
