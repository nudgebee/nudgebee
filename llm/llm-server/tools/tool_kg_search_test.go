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
	// query is intentionally NOT required — the framework-level required
	// validator treats "field present but empty" as MISSING, but this tool
	// accepts an empty query when paired with node_types/namespace/source/
	// labels/account_ids (see TestValidateKGSearchInput for the semantic
	// check that replaced the schema-level requirement).
	assert.NotContains(t, schema.Required, "query")

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

// TestKGSearchNodesTool_SchemaAllowsEmptyQuery pins the schema change: the
// framework-level required-field validator in executor_planner.go treats
// "field present but empty" as MISSING, which contradicts this tool's
// description that says empty is valid alongside a filter. Dropping `query`
// from Required lets the schema match the description. Semantic validation
// (must supply query OR at least one filter) moves inline into Call().
func TestKGSearchNodesTool_SchemaAllowsEmptyQuery(t *testing.T) {
	tool := KGSearchNodesTool{accountId: "acct-1"}
	schema := tool.InputSchema()
	assert.NotContains(t, schema.Required, "query",
		"query must NOT be in the schema's Required list — the framework validator would reject empty strings, contradicting the description that says empty is valid with a filter")
	// Description still promises the empty-is-valid contract.
	assert.Contains(t, schema.Properties["query"].Description, "empty",
		"description must explain when empty is valid so the model knows the contract")
}

// TestValidateKGSearchInput pins the inline semantic check that replaces the
// framework's blanket required-field rejection. All-empty rejects; empty
// query + ANY filter is accepted — that's the shape the tool description
// promised but the framework validator was previously blocking (2 of 3
// kg_search_nodes errors in the 2026-07-02 sweep were the model correctly
// following the description and getting rejected).
func TestValidateKGSearchInput(t *testing.T) {
	t.Run("all empty rejects with actionable message", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{})
		if assert.NotNil(t, reject) {
			assert.Equal(t, core.NBToolResponseStatusError, reject.Status)
			assert.Contains(t, reject.Data, "at least one of query, node_types, namespace, source, labels, or account_ids",
				"reject message must name the shape that WOULD work so the model can self-correct")
		}
	})

	t.Run("empty query + node_types filter accepted (the bug this fix closes)", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{
			Query:     "",
			NodeTypes: []string{"PersistentVolumeClaim"},
			Namespace: "redis",
		})
		assert.Nil(t, reject, "empty query + a filter is the exact shape the description says is valid — must not reject")
	})

	t.Run("empty query + only source filter accepted", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{Source: "aws"})
		assert.Nil(t, reject)
	})

	t.Run("empty query + only account_ids accepted", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{AccountIDs: []string{"123456789012"}})
		assert.Nil(t, reject)
	})

	t.Run("only query accepted", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{Query: "llm-server"})
		assert.Nil(t, reject)
	})

	t.Run("wildcard query alone accepted (backward compat with the % workaround)", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{Query: "%"})
		assert.Nil(t, reject)
	})

	t.Run("whitespace-only query treated as empty", func(t *testing.T) {
		reject := validateKGSearchInput(kgSearchInput{Query: "   \n"})
		assert.NotNil(t, reject, "whitespace-only query is functionally empty and must not sneak past validation")
	})
}
