package core

import (
	"context"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// mergeSystemMessages wraps an OpenAI-compatible llms.Model to collapse multiple
// system-role MessageContent entries into one before delegating.
//
// planner_react_3.go builds the ReAct3 prompt as several separate system messages
// (base prompt, client-tool priority instruction, account context, agent-additional
// prompt, agent prompt) so Anthropic/Bedrock can cache-breakpoint each block
// independently. The OpenAI Chat Completions wire format tolerates that fine — each
// becomes its own "role":"system" entry — but self-hosted/gateway models reached
// through the `custom` provider (vLLM, SGLang, ...) often serve a strict chat
// template (e.g. Qwen's) that only tolerates one system message, at index 0, and
// otherwise raises "System message must be at the beginning" as a 400. Joining the
// system messages into one preserves their content and order and is valid for every
// backend speaking the Chat Completions format, so it's safe to always do on this
// path rather than special-casing it per model.
type mergeSystemMessages struct {
	inner llms.Model
}

// wrapMergeSystemMessages decorates an OpenAI-compatible model; nil passes through.
func wrapMergeSystemMessages(model llms.Model) llms.Model {
	if model == nil {
		return model
	}
	return &mergeSystemMessages{inner: model}
}

func (w *mergeSystemMessages) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	return w.inner.GenerateContent(ctx, mergeSystemMessageContents(messages), options...)
}

func (w *mergeSystemMessages) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, w, prompt, options...)
}

// mergeSystemMessageContents joins every system-role message's text into a single
// system message at the position of the first one; non-system messages keep their
// relative order and position untouched. A single TextContent part (rather than one
// part per source message) keeps the eventual "content" field a plain string, which
// is what strict chat templates expect for the system role. Non-text parts are rare
// on a system message (they're always literal/templated instruction text here) but
// are preserved, appended after the joined text, so nothing is silently dropped.
func mergeSystemMessageContents(messages []llms.MessageContent) []llms.MessageContent {
	systemCount := 0
	for _, mc := range messages {
		if mc.Role == llms.ChatMessageTypeSystem {
			systemCount++
		}
	}
	if systemCount <= 1 {
		return messages
	}

	merged := make([]llms.MessageContent, 0, len(messages)-systemCount+1)
	var texts []string
	var otherParts []llms.ContentPart
	placed := false
	for _, mc := range messages {
		if mc.Role != llms.ChatMessageTypeSystem {
			merged = append(merged, mc)
			continue
		}
		for _, part := range mc.Parts {
			if tc, ok := part.(llms.TextContent); ok {
				texts = append(texts, tc.Text)
			} else {
				otherParts = append(otherParts, part)
			}
		}
		if !placed {
			// Reserve this message's position; filled in below once all system
			// parts across the whole slice have been collected.
			merged = append(merged, llms.MessageContent{Role: llms.ChatMessageTypeSystem})
			placed = true
		}
	}

	parts := make([]llms.ContentPart, 0, 1+len(otherParts))
	parts = append(parts, llms.TextContent{Text: strings.Join(texts, "\n\n")})
	parts = append(parts, otherParts...)
	for i := range merged {
		if merged[i].Role == llms.ChatMessageTypeSystem {
			merged[i].Parts = parts
			break
		}
	}
	return merged
}
