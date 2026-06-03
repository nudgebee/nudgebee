package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestKGGetNodeTool_Metadata(t *testing.T) {
	tool := KGGetNodeTool{accountId: "acct-1"}
	assert.Equal(t, ToolKGGetNode, tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())

	schema := tool.InputSchema()
	assert.Contains(t, schema.Properties, "node_id")
	assert.ElementsMatch(t, []string{"node_id"}, schema.Required)

	desc := tool.Description()
	assert.Contains(t, desc, "drill-down")
	assert.Contains(t, desc, "kg_search_nodes")
	assert.Contains(t, desc, "kg_traverse")
	assert.Contains(t, desc, "properties")
}

func TestParseKGGetNodeInput(t *testing.T) {
	t.Run("JSON command", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{
			Command: `{"node_id":"abc-123"}`,
		})
		assert.Equal(t, "abc-123", got)
	})

	t.Run("plain-string command treated as node_id", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{
			Command: "abc-123",
		})
		assert.Equal(t, "abc-123", got)
	})

	t.Run("flat Arguments", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{
			Arguments: map[string]any{"node_id": "abc-123"},
		})
		assert.Equal(t, "abc-123", got)
	})

	t.Run("JSON command falls through to Arguments when node_id missing", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{
			Command:   `{"other_key":"x"}`,
			Arguments: map[string]any{"node_id": "from-args"},
		})
		assert.Equal(t, "from-args", got)
	})

	t.Run("invalid JSON falls through to Arguments", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{
			Command:   `{"node_id":`,
			Arguments: map[string]any{"node_id": "from-args"},
		})
		assert.Equal(t, "from-args", got)
	})

	t.Run("empty input returns empty string", func(t *testing.T) {
		got := parseKGGetNodeInput(core.NBToolCallRequest{})
		assert.Equal(t, "", got)
	})
}
