package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// A zero-argument call streams back with Arguments "", which is not valid JSON.
// Replaying it into history is what makes the following request fail.
func TestToolCallFromToolCall_DefaultsEmptyArgumentsToEmptyObject(t *testing.T) {
	got := toolCallFromToolCall(llms.ToolCall{
		ID:           "call_1",
		Type:         "function",
		FunctionCall: &llms.FunctionCall{Name: "list_namespaces"},
	})

	assert.Equal(t, "{}", got.Function.Arguments)
	assert.Equal(t, "list_namespaces", got.Function.Name)
	assert.Equal(t, "call_1", got.ID)
}

func TestToolCallFromToolCall_PreservesRealArguments(t *testing.T) {
	got := toolCallFromToolCall(llms.ToolCall{
		ID:           "call_2",
		Type:         "function",
		FunctionCall: &llms.FunctionCall{Name: "kubectl_execute", Arguments: `{"command":"get po"}`},
	})

	assert.Equal(t, `{"command":"get po"}`, got.Function.Arguments)
}

// ExtractToolParts passes any llms.ToolCall through unchecked, so a malformed one
// reaches the converter. It must fail the request, not panic and not emit a
// nameless tool call the API cannot dispatch.
func TestToolCallsFromToolCalls_RejectsNilFunctionCall(t *testing.T) {
	_, err := toolCallsFromToolCalls([]llms.ToolCall{
		{ID: "call_ok", Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl_execute"}},
		{ID: "call_bad", Type: "function"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "call_bad")
}

func TestToolCallsFromToolCalls_ConvertsValidCalls(t *testing.T) {
	got, err := toolCallsFromToolCalls([]llms.ToolCall{
		{ID: "a", Type: "function", FunctionCall: &llms.FunctionCall{Name: "logs", Arguments: `{"ns":"prod"}`}},
	})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, `{"ns":"prod"}`, got[0].Function.Arguments)
}
