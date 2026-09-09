package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestNbToolsToLlmTools_RendersSchema locks the ToolSchema -> llms.Tool
// conversion that the ReAct4 planner reuses (via planner_prompt.go's existing
// WithTools call) to advertise provider-native tool definitions. It asserts the
// name/description pass through and that Parameters is the map[string]any JSON
// schema shape the providers require (googleai rejects a non-map Parameters).
func TestNbToolsToLlmTools_RendersSchema(t *testing.T) {
	tool := &stubTool{
		name:     "kubectl",
		required: []string{"command"},
		props: map[string]toolcore.ToolSchemaProperty{
			"command": {Type: toolcore.ToolSchemaTypeString, Description: "the kubectl subcommand"},
			"namespace": {
				Type: toolcore.ToolSchemaTypeString,
				Enum: []any{"default", "kube-system"},
			},
		},
	}

	llmTools := nbToolsToLlmTools([]toolcore.NBTool{tool})

	assert.Len(t, llmTools, 1)
	fn := llmTools[0].Function
	assert.Equal(t, "function", llmTools[0].Type)
	assert.Equal(t, "kubectl", fn.Name)

	params, ok := fn.Parameters.(map[string]any)
	assert.True(t, ok, "Parameters must be map[string]any for provider compatibility")
	assert.Equal(t, "object", params["type"])
	assert.Equal(t, []string{"command"}, params["required"])

	properties, ok := params["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, properties, "command")
	assert.Contains(t, properties, "namespace")

	command, ok := properties["command"].(map[string]any)
	assert.True(t, ok)
	// The property type MUST be a plain `string`, not the named ToolSchemaType.
	// Provider converters type-assert it (googleai.go convertSchemaRecursive does
	// `ty.(string)`), and a named string type fails that assertion — which killed
	// every native tool-calling request with "expected string for type" before any
	// tool ran. assert.Equal compares dynamic types, so this pins the conversion.
	assert.Equal(t, "string", command["type"])
	_, isPlainString := command["type"].(string)
	assert.True(t, isPlainString,
		"property type must assert as plain string for provider schema conversion")
	assert.Equal(t, "the kubectl subcommand", command["description"])

	namespace, ok := properties["namespace"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, []any{"default", "kube-system"}, namespace["enum"])
}
