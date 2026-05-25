package tools

import (
	"testing"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestThinkTool_EchoesReasoning(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Arguments: map[string]any{
			"reasoning": "Logs show connection refused but metrics show low CPU. This suggests the issue is not resource exhaustion but rather a network partition or misconfigured service endpoint.",
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "network partition")
}

func TestThinkTool_FallbackToCommand(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Command: "Need to reconsider the approach",
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "reconsider")
}

func TestThinkTool_EmptyArgsFallsBackToCommand(t *testing.T) {
	tool := &thinkTool{}
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Command:   "fallback reasoning",
		Arguments: map[string]any{},
	})
	assert.NoError(t, err)
	assert.Equal(t, "fallback reasoning", resp.Data)
}

func TestThinkTool_Metadata(t *testing.T) {
	tool := &thinkTool{}
	assert.Equal(t, "think", tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())
	assert.Contains(t, tool.Description(), "conflicting evidence")

	schema := tool.InputSchema()
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "reasoning")
	assert.Equal(t, []string{"reasoning"}, schema.Required)
}
