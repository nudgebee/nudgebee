package openai

import (
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// mergeSystemMessageContents joins every system-role message's text into a single
// system message at the position of the first one; non-system messages keep their
// relative order and position untouched. A single TextContent part (rather than one
// part per source message) keeps the eventual "content" field a plain string, which
// is what strict chat templates expect for the system role. Non-text parts are rare
// on a system message (they're always literal/templated instruction text here) but
// are preserved, appended after the joined text, so nothing is silently dropped.
//
// The client applies it when built WithMergedSystemMessages. planner_react_3.go
// builds the ReAct3 prompt as several separate system messages (base prompt, client-tool priority instruction, account context, agent-additional
// prompt, agent prompt) so Anthropic/Bedrock can cache-breakpoint each block
// independently. The OpenAI Chat Completions wire format tolerates that fine — each
// becomes its own "role":"system" entry — but self-hosted/gateway models reached
// through the `custom` provider (vLLM, SGLang, ...) often serve a strict chat
// template (e.g. Qwen's) that only tolerates one system message, at index 0, and
// otherwise raises "System message must be at the beginning" as a 400. Joining the
// system messages into one preserves their content and order and is valid for every
// backend speaking the Chat Completions format, so it's safe to turn on for the
// whole provider rather than special-casing it per model.
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
	placeholderIdx := -1
	for _, mc := range messages {
		if mc.Role != llms.ChatMessageTypeSystem {
			merged = append(merged, mc)
			continue
		}
		for _, part := range mc.Parts {
			if tc, ok := part.(llms.TextContent); ok {
				if tc.Text != "" {
					texts = append(texts, tc.Text)
				}
			} else {
				otherParts = append(otherParts, part)
			}
		}
		if placeholderIdx == -1 {
			// Reserve this message's position; filled in below once all system
			// parts across the whole slice have been collected.
			placeholderIdx = len(merged)
			merged = append(merged, llms.MessageContent{Role: llms.ChatMessageTypeSystem})
		}
	}

	parts := make([]llms.ContentPart, 0, 1+len(otherParts))
	if len(texts) > 0 {
		parts = append(parts, llms.TextContent{Text: strings.Join(texts, "\n\n")})
	} else if len(otherParts) == 0 {
		// Every merged system message was empty text with no other parts. Keep one
		// empty TextContent rather than leaving Parts empty — some providers reject
		// a message with zero content parts outright.
		parts = append(parts, llms.TextContent{Text: ""})
	}
	parts = append(parts, otherParts...)
	merged[placeholderIdx].Parts = parts
	return merged
}
