package tools

import (
	"testing"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestNotebookTool_AcceptsContentFromArguments(t *testing.T) {
	tool := &notebookTool{}
	body := "## Hypothesis Tree\n- OOMKill [SUPPORTED]: pod restarts correlate with memory limit"
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{
		Arguments: map[string]any{notebookContentArg: body},
	})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, body, resp.Data)
}

func TestNotebookTool_AcceptsContentFromCommand(t *testing.T) {
	tool := &notebookTool{}
	body := "notebook body on the command field"
	resp, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{Command: body})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Equal(t, body, resp.Data)
}

func TestNotebookTool_RejectsEmptyContent(t *testing.T) {
	tool := &notebookTool{}
	for _, tc := range []struct {
		name string
		req  core.NBToolCallRequest
	}{
		{"no fields", core.NBToolCallRequest{}},
		{"empty arg", core.NBToolCallRequest{Arguments: map[string]any{notebookContentArg: ""}}},
		{"whitespace command", core.NBToolCallRequest{Command: "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tool.Call(core.NbToolContext{}, tc.req)
			assert.NoError(t, err)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.Equal(t, notebookEmptyRejectionMsg, resp.Data)
		})
	}
}

func TestNotebookTool_Schema(t *testing.T) {
	tool := &notebookTool{}
	schema := tool.InputSchema()
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, notebookContentArg)
	assert.Equal(t, []string{notebookContentArg}, schema.Required)
	assert.Equal(t, NotebookToolName, tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())
}
