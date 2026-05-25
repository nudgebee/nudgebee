// Package googleai provides caching support for Google AI models.
package googleai

import (
	"context"
	"time"

	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

// CachingHelper provides utilities for working with Google AI's cached content feature.
// Unlike Anthropic which supports inline cache control, Google AI requires
// pre-creating cached content through the API.
type CachingHelper struct {
	client *genai.Client
}

// NewCachingHelper creates a helper for managing cached content.
func NewCachingHelper(ctx context.Context, opts ...Option) (*CachingHelper, error) {
	gai, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &CachingHelper{
		client: gai.client,
	}, nil
}

// CreateCachedContent creates cached content that can be reused across multiple requests.
// This is useful for caching large system prompts, context documents, or frequently used instructions.
func (ch *CachingHelper) CreateCachedContent(
	ctx context.Context,
	modelName string,
	messages []llms.MessageContent,
	ttl time.Duration,
	displayName string,
) (*genai.CachedContent, error) {
	contents := make([]*genai.Content, 0, len(messages))
	// Merge all system messages into a single SystemInstruction.
	// Google AI expects exactly one SystemInstruction — multiple system messages
	// must have their parts concatenated, not overwritten.
	var systemParts []*genai.Part

	for _, msg := range messages {
		parts := make([]*genai.Part, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				if p.Text == "" {
					continue // Skip empty text parts — Google AI rejects them
				}
				parts = append(parts, &genai.Part{Text: p.Text})
			case llms.CachedContent:
				// Extract the underlying content if it's wrapped with cache control
				// (Google AI doesn't use inline cache control like Anthropic)
				if textPart, ok := p.ContentPart.(llms.TextContent); ok && textPart.Text != "" {
					parts = append(parts, &genai.Part{Text: textPart.Text})
				}
			}
		}

		if len(parts) == 0 {
			continue // Skip messages with no valid parts
		}

		switch msg.Role {
		case llms.ChatMessageTypeSystem:
			systemParts = append(systemParts, parts...)
		case llms.ChatMessageTypeHuman:
			contents = append(contents, &genai.Content{Role: "user", Parts: parts})
		case llms.ChatMessageTypeAI:
			contents = append(contents, &genai.Content{Role: "model", Parts: parts})
		}
	}

	var systemInstruction *genai.Content
	if len(systemParts) > 0 {
		systemInstruction = &genai.Content{Role: "system", Parts: systemParts}
	}

	cfg := &genai.CreateCachedContentConfig{
		DisplayName:       displayName,
		TTL:               ttl,
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	return ch.client.Caches.Create(ctx, modelName, cfg)
}

// GetCachedContent retrieves existing cached content by name.
func (ch *CachingHelper) GetCachedContent(ctx context.Context, name string) (*genai.CachedContent, error) {
	return ch.client.Caches.Get(ctx, name, nil)
}

// DeleteCachedContent removes cached content.
func (ch *CachingHelper) DeleteCachedContent(ctx context.Context, name string) error {
	_, err := ch.client.Caches.Delete(ctx, name, nil)
	return err
}

// CountTokens counts the number of tokens in the given messages using Google AI's token counting API.
// Note: CountTokens API does not support SystemInstruction, so all messages (including system)
// are passed in the contents array. The token count will be slightly higher than what
// CreateCachedContent sees (which uses SystemInstruction), but this is acceptable for
// the minimum-threshold check — overestimating is safe.
func (ch *CachingHelper) CountTokens(ctx context.Context, modelName string, messages []llms.MessageContent) (int32, error) {
	contents := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		parts := make([]*genai.Part, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				if p.Text == "" {
					continue // Skip empty text parts — Google AI rejects them
				}
				parts = append(parts, &genai.Part{Text: p.Text})
			case llms.CachedContent:
				// Extract the underlying content if it's wrapped
				if textPart, ok := p.ContentPart.(llms.TextContent); ok && textPart.Text != "" {
					parts = append(parts, &genai.Part{Text: textPart.Text})
				}
			}
		}

		if len(parts) == 0 {
			continue // Skip messages with no valid parts
		}

		content := &genai.Content{Parts: parts}

		switch msg.Role {
		case llms.ChatMessageTypeSystem:
			content.Role = "system"
		case llms.ChatMessageTypeHuman:
			content.Role = "user"
		case llms.ChatMessageTypeAI:
			content.Role = "model"
		}
		contents = append(contents, content)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	resp, err := ch.client.Models.CountTokens(ctx, modelName, contents, nil)
	if err != nil {
		return 0, err
	}

	return resp.TotalTokens, nil
}
