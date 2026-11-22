package anthropic

import (
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// normalizeChoices fixes Claude 5-family responses: the client emits one
// Choice PER CONTENT BLOCK in order, and sonnet-5's adaptive reasoning returns
// [thinking, text] — so Choices[0] is the thinking block with Content == "",
// and every caller that reads Choices[0] concludes "llm returned empty
// content". It moves the first choice carrying actual content (text or tool
// calls) to the front; thinking-only choices keep riding behind it with their
// GenerationInfo intact.
func normalizeChoices(resp *llms.ContentResponse) {
	if resp == nil || len(resp.Choices) < 2 {
		return
	}
	if resp.Choices[0] != nil && (strings.TrimSpace(resp.Choices[0].Content) != "" || len(resp.Choices[0].ToolCalls) > 0) {
		return
	}
	for i := 1; i < len(resp.Choices); i++ {
		c := resp.Choices[i]
		if c != nil && (strings.TrimSpace(c.Content) != "" || len(c.ToolCalls) > 0) {
			resp.Choices[0], resp.Choices[i] = resp.Choices[i], resp.Choices[0]
			break
		}
	}
}
