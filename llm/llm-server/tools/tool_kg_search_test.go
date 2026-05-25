package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestKGSearchNodesTool_Metadata(t *testing.T) {
	tool := KGSearchNodesTool{accountId: "acct-1"}
	assert.Equal(t, ToolKGSearchNodes, tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())

	schema := tool.InputSchema()
	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "query")
	assert.Contains(t, schema.Properties, "node_types")
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "source")
	assert.Contains(t, schema.Properties, "labels")
	assert.Contains(t, schema.Required, "query")

	// Description MUST position KG as primary and steer to SDG only for metrics.
	desc := tool.Description()
	assert.Contains(t, desc, "PRIMARY")
	assert.Contains(t, desc, "CALLS")
	assert.Contains(t, desc, "service_dependency_graph")
	assert.Contains(t, desc, "METRICS")
}

func TestParseKGSearchInput(t *testing.T) {
	t.Run("command as plain string becomes query", func(t *testing.T) {
		in, err := parseKGSearchInput(core.NBToolCallRequest{Command: "redis"})
		assert.NoError(t, err)
		assert.Equal(t, "redis", in.Query)
	})

	t.Run("command as JSON populates all fields", func(t *testing.T) {
		in, err := parseKGSearchInput(core.NBToolCallRequest{
			Command: `{"query":"redis%","node_types":["Workload"],"namespace":"nudgebee","source":"k8s","labels":"{\"app\":\"kibana\"}"}`,
		})
		assert.NoError(t, err)
		assert.Equal(t, "redis%", in.Query)
		assert.Equal(t, []string{"Workload"}, in.NodeTypes)
		assert.Equal(t, "nudgebee", in.Namespace)
		assert.Equal(t, "k8s", in.Source)
		assert.Equal(t, `{"app":"kibana"}`, in.Labels)
	})

	t.Run("flat arguments merged with command", func(t *testing.T) {
		in, err := parseKGSearchInput(core.NBToolCallRequest{
			Command: "redis",
			Arguments: map[string]any{
				"node_types": []any{"Workload", "Database"},
				"namespace":  "prod",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, "redis", in.Query)
		assert.Equal(t, []string{"Workload", "Database"}, in.NodeTypes)
		assert.Equal(t, "prod", in.Namespace)
	})

	t.Run("invalid command JSON returns error", func(t *testing.T) {
		_, err := parseKGSearchInput(core.NBToolCallRequest{Command: `{"query":`})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid command JSON")
	})
}
