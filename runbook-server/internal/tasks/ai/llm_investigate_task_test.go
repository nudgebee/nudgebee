package ai

import (
	"nudgebee/runbook/internal/tasks/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseToolsParam(t *testing.T) {
	tests := []struct {
		name      string
		input     any
		want      []string
		expectErr bool
	}{
		{name: "nil returns nil", input: nil, want: nil},
		{name: "string slice passes through", input: []string{"aws_execute", "kubectl_execute"}, want: []string{"aws_execute", "kubectl_execute"}},
		{name: "any slice from JSON normalises to strings", input: []any{"aws_execute", "kubectl_execute"}, want: []string{"aws_execute", "kubectl_execute"}},
		{name: "empty entries are dropped", input: []any{"aws_execute", "", "  "}, want: []string{"aws_execute"}},
		{name: "whitespace is trimmed", input: []string{"  aws_execute  "}, want: []string{"aws_execute"}},
		{name: "empty slice returns nil", input: []string{}, want: nil},
		{name: "non-string element errors", input: []any{"aws_execute", 42}, expectErr: true},
		{name: "wrong outer type errors", input: "aws_execute", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseToolsParam(tc.input)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLLMInvestigateTask_InputSchema_ToolsField(t *testing.T) {
	task := &LLMInvestigateTask{}
	schema := task.InputSchema()

	require.Contains(t, schema.Properties, "tools")
	tools := schema.Properties["tools"]

	assert.Equal(t, types.PropertyTypeArray, tools.Type)
	assert.False(t, tools.Required, "tools should be optional")
	require.NotNil(t, tools.OptionsSource, "tools should declare an OptionsSource so the UI can render a multi-select")
	assert.Equal(t, "llm_tools", tools.OptionsSource.Type)
}
