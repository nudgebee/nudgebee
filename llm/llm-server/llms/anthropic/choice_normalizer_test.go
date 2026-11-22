package anthropic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// A [thinking, text] response must surface the text choice first; a plain
// text response is untouched.
func TestNormalizeChoices_PromotesTextChoice(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{
		{Content: "", GenerationInfo: map[string]any{"ThinkingContent": "hmm"}},
		{Content: "<thought_action>real answer</thought_action>"},
	}}
	normalizeChoices(resp)
	assert.Contains(t, resp.Choices[0].Content, "real answer")
	assert.Len(t, resp.Choices, 2, "thinking choice preserved behind")

	single := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "only"}}}
	normalizeChoices(single)
	assert.Equal(t, "only", single.Choices[0].Content)
}
