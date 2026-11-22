package tools

import (
	"testing"

	core "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestNotebookV2Tool_Call(t *testing.T) {
	tool := &notebookV2Tool{}

	ok, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{Command: `{"steps":[{"id":"s1","title":"Scope","status":"in_progress"}]}`})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, ok.Status)

	bad, err := tool.Call(core.NbToolContext{}, core.NBToolCallRequest{Command: `{"steps":[{"id":"h1","status":"supported"}]}`})
	assert.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, bad.Status)
	assert.Contains(t, bad.Data, "finding", "the error tells the model what to add")
}

func TestNotebookV2Tool_SchemaRequiresSteps(t *testing.T) {
	schema := (&notebookV2Tool{}).InputSchema()
	assert.Equal(t, []string{core.PlanStepsArgument}, schema.Required)
	assert.Equal(t, core.ToolSchemaTypeArray, schema.Properties[core.PlanStepsArgument].Type)
}
