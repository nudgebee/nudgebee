package core

import (
	"encoding/json"
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestAgentHandoffTargetNativeSchema(t *testing.T) {
	tool := &nbAgentTool{name: "targeted_agent", input: "investigate", output: "result", targetSupported: true}
	require.NoError(t, ValidateNativeToolSchemas([]toolcore.NBTool{tool}))
}

type customTargetTestAgent struct{ NBAgent }

func (customTargetTestAgent) GetName() string                  { return "custom_agent" }
func (customTargetTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeCustom }

func TestTargetedCustomAgentFailsBeforeExecution(t *testing.T) {
	_, err := ExecuteAgentToolCall(toolcore.NbToolContext{}, customTargetTestAgent{}, toolcore.NBToolCallRequest{
		Arguments: map[string]any{"target": map[string]any{"provider": "aws", "name": "prod"}},
	})
	require.ErrorContains(t, err, "does not support a delegated execution target")
}

// TestNbToolsToLlmTools_NilRequiredRendersEmptySlice verifies that when a tool's
// InputSchema has a nil Required slice, nbToolsToLlmTools defensively normalizes it
// to an empty slice ([]string{}) rather than serializing as "required": null in JSON.
// OpenAI and OpenAI-compatible endpoints reject "required": null with:
// "400: Invalid schema for function '...': None is not of type 'array'".
func TestNbToolsToLlmTools_NilRequiredRendersEmptySlice(t *testing.T) {
	tool := &stubTool{
		name:     "cloud_resource_search_execute",
		required: nil,
		props: map[string]toolcore.ToolSchemaProperty{
			"query": {Type: toolcore.ToolSchemaTypeString, Description: "resource search query"},
		},
	}

	llmTools := nbToolsToLlmTools([]toolcore.NBTool{tool})
	require.Len(t, llmTools, 1)

	params, ok := llmTools[0].Function.Parameters.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{}, params["required"])

	rawJSON, err := json.Marshal(params)
	require.NoError(t, err)
	assert.Contains(t, string(rawJSON), `"required":[]`)
	assert.NotContains(t, string(rawJSON), `"required":null`)
}

// TestNbToolsToLlmTools_PrunesUndeclaredRequiredAndDefaultsEmptyType verifies that
// when a custom or client tool sends an undeclared required property or an empty
// property type, nbToolsToLlmTools prunes the dangling required field and defaults
// the empty type to "string". Strict JSON schema validators reject required fields
// that are not defined in properties with:
// "400: Invalid schema for function '...': required property '...' is not defined in properties".
func TestNbToolsToLlmTools_PrunesUndeclaredRequiredAndDefaultsEmptyType(t *testing.T) {
	tool := &stubTool{
		name:     "custom_client_tool",
		required: []string{"command", "deleted_or_dangling_param"},
		props: map[string]toolcore.ToolSchemaProperty{
			"command": {Type: "", Description: "command with empty type"},
			"options": {Type: toolcore.ToolSchemaTypeArray, Description: "array without items"},
		},
	}

	llmTools := nbToolsToLlmTools([]toolcore.NBTool{tool})
	require.Len(t, llmTools, 1)

	params, ok := llmTools[0].Function.Parameters.(map[string]any)
	require.True(t, ok)

	// Dangling parameter was pruned from required
	assert.Equal(t, []string{"command"}, params["required"])

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)

	// Empty type was defaulted to string
	cmdProp, ok := props["command"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", cmdProp["type"])

	// Array without items was defaulted to string items
	optionsProp, ok := props["options"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "array", optionsProp["type"])
	items, ok := optionsProp["items"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", items["type"])
}
