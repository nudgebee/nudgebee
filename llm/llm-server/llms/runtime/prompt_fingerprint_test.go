package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestFingerprintPrompt_PinpointsFirstChangedMessage(t *testing.T) {
	tools := []llms.Tool{{Type: "function", Function: &llms.FunctionDefinition{Name: "kubectl"}}}
	step1 := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "system"),
		llms.TextParts(llms.ChatMessageTypeHuman, "question"),
	}
	step2 := append(append([]llms.MessageContent(nil), step1...),
		llms.MessageContent{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.ToolCall{ID: "1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl", Arguments: "{}"}}}},
		llms.MessageContent{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolCallResponse{ToolCallID: "1", Name: "kubectl", Content: "ok"}}},
	)

	a := fingerprintPrompt(step1, []llms.CallOption{llms.WithTools(tools)})
	b := fingerprintPrompt(step2, []llms.CallOption{llms.WithTools(tools)})
	assert.Equal(t, a.Tools, b.Tools)
	assert.Equal(t, a.Messages, b.Messages[:2], "an appended step leaves earlier hashes unchanged")
	assert.NotContains(t, b.Messages, "err")
	assert.Len(t, b.Messages, 4)

	step2[1] = llms.TextParts(llms.ChatMessageTypeHuman, "question asked at a new time")
	c := fingerprintPrompt(step2, nil)
	assert.Equal(t, a.Messages[0], c.Messages[0])
	assert.NotEqual(t, a.Messages[1], c.Messages[1], "a rewritten opening message changes its hash")
	assert.Empty(t, c.Tools)
}
