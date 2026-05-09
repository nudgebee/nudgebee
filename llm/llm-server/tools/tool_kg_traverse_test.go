package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestKGTraverseTool_Metadata(t *testing.T) {
	tool := KGTraverseTool{accountId: "acct-1"}
	assert.Equal(t, ToolKGTraverse, tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())

	schema := tool.InputSchema()
	assert.Contains(t, schema.Properties, "query")
	assert.Contains(t, schema.Properties, "node_id")
	assert.Contains(t, schema.Properties, "node_ids")
	assert.Contains(t, schema.Properties, "direction")
	assert.Contains(t, schema.Properties, "node_types")
	assert.Contains(t, schema.Properties, "max_depth")
	assert.Contains(t, schema.Properties, "relationship_types")
	assert.Contains(t, schema.Properties, "exclude_node_types")
	assert.Contains(t, schema.Properties, "result_limit")
	assert.ElementsMatch(t, []string{"direction"}, schema.Required)

	direction := schema.Properties["direction"]
	assert.ElementsMatch(t, []any{"downstream", "upstream", "both"}, direction.Enum)

	desc := tool.Description()
	assert.Contains(t, desc, "PRIMARY")
	assert.Contains(t, desc, "CALLS")
	assert.Contains(t, desc, "service_dependency_graph")
	assert.Contains(t, desc, "METRICS")
	assert.Contains(t, desc, "RUNS_ON")
	assert.Contains(t, desc, "ROUTES_TO")
}

func TestParseKGTraverseInput(t *testing.T) {
	t.Run("minimal JSON command", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"query":"llm-server","direction":"downstream"}`,
		})
		assert.NoError(t, err)
		assert.Equal(t, "llm-server", in.Query)
		assert.Equal(t, "downstream", in.Direction)
		assert.False(t, in.excludeNodeTypesPassed)
	})

	t.Run("plain-string command treated as query", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: "llm-server",
			Arguments: map[string]any{
				"direction": "upstream",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, "llm-server", in.Query)
		assert.Equal(t, "upstream", in.Direction)
	})

	t.Run("exclude_node_types explicit empty is preserved", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"query":"lb","direction":"both","node_types":["LoadBalancer"],"exclude_node_types":[]}`,
		})
		assert.NoError(t, err)
		assert.True(t, in.excludeNodeTypesPassed)
		assert.NotNil(t, in.ExcludeNodeTypes)
		assert.Empty(t, *in.ExcludeNodeTypes)
	})

	t.Run("exclude_node_types explicit value is preserved", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"query":"x","direction":"both","exclude_node_types":["SecurityGroup"]}`,
		})
		assert.NoError(t, err)
		assert.True(t, in.excludeNodeTypesPassed)
		assert.Equal(t, []string{"SecurityGroup"}, *in.ExcludeNodeTypes)
	})

	t.Run("result_limit passthrough", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"query":"x","direction":"downstream","result_limit":150}`,
		})
		assert.NoError(t, err)
		assert.Equal(t, 150, in.ResultLimit)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := parseKGTraverseInput(core.NBToolCallRequest{Command: `{"query":`})
		assert.Error(t, err)
	})

	t.Run("node_id from JSON command", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"node_id":"abc-123","direction":"downstream"}`,
		})
		assert.NoError(t, err)
		assert.Equal(t, "abc-123", in.NodeID)
		assert.Empty(t, in.Query)
	})

	t.Run("node_ids from JSON command", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Command: `{"node_ids":["a","b","c"],"direction":"both"}`,
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, in.NodeIDs)
	})

	t.Run("node_id from Arguments", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Arguments: map[string]any{
				"node_id":   "xyz-9",
				"direction": "upstream",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, "xyz-9", in.NodeID)
		assert.Equal(t, "upstream", in.Direction)
	})

	t.Run("node_ids from Arguments", func(t *testing.T) {
		in, err := parseKGTraverseInput(core.NBToolCallRequest{
			Arguments: map[string]any{
				"node_ids":  []any{"a", "b"},
				"direction": "downstream",
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, in.NodeIDs)
	})
}

// resolveSeedIDs collapses node_id + node_ids into a single ordered slice and
// must dedupe so the backend doesn't traverse the same seed twice.
func TestResolveSeedIDs(t *testing.T) {
	cases := []struct {
		name string
		in   kgTraverseInput
		want []string
	}{
		{name: "empty", in: kgTraverseInput{}, want: []string{}},
		{name: "node_id only", in: kgTraverseInput{NodeID: "a"}, want: []string{"a"}},
		{name: "node_ids only", in: kgTraverseInput{NodeIDs: []string{"a", "b"}}, want: []string{"a", "b"}},
		{
			name: "node_id and node_ids merged",
			in:   kgTraverseInput{NodeID: "c", NodeIDs: []string{"a", "b"}},
			want: []string{"a", "b", "c"},
		},
		{
			name: "duplicate between node_id and node_ids deduped",
			in:   kgTraverseInput{NodeID: "a", NodeIDs: []string{"a", "b"}},
			want: []string{"a", "b"},
		},
		{
			name: "duplicates within node_ids deduped",
			in:   kgTraverseInput{NodeIDs: []string{"a", "a", "b"}},
			want: []string{"a", "b"},
		},
		{
			name: "empty strings dropped",
			in:   kgTraverseInput{NodeID: "", NodeIDs: []string{"", "a", ""}},
			want: []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSeedIDs(tc.in)
			if len(tc.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestContainsFold(t *testing.T) {
	assert.True(t, containsFold([]string{"loadbalancer"}, "LoadBalancer"))
	assert.True(t, containsFold([]string{"LoadBalancer", "Workload"}, "workload"))
	assert.False(t, containsFold([]string{"Workload"}, "LoadBalancer"))
	assert.False(t, containsFold(nil, "LoadBalancer"))
}

// Exercises the default exclude_node_types resolution: when the LLM omits the
// parameter AND direction=both AND node_types includes LoadBalancer, the tool
// fills in the SG/NIC/Subnet default. When the LLM passes an explicit value
// (including []), that wins.
func TestKGTraverseExcludeNodeTypesResolution(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		expected []string // nil = "no exclude applied"
	}{
		{
			name:     "omitted + LB + both -> default exclusion",
			command:  `{"query":"lb","direction":"both","node_types":["LoadBalancer"]}`,
			expected: kgTraverseLoadBalancerAutoExclude,
		},
		{
			name:     "omitted + LB + downstream -> no default (only both triggers it)",
			command:  `{"query":"lb","direction":"downstream","node_types":["LoadBalancer"]}`,
			expected: nil,
		},
		{
			name:     "omitted + no LB -> no default",
			command:  `{"query":"x","direction":"both","node_types":["Workload"]}`,
			expected: nil,
		},
		{
			name:     "explicit [] overrides default",
			command:  `{"query":"lb","direction":"both","node_types":["LoadBalancer"],"exclude_node_types":[]}`,
			expected: []string{},
		},
		{
			name:     "explicit value overrides default",
			command:  `{"query":"lb","direction":"both","node_types":["LoadBalancer"],"exclude_node_types":["SecurityGroup"]}`,
			expected: []string{"SecurityGroup"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseKGTraverseInput(core.NBToolCallRequest{Command: tc.command})
			assert.NoError(t, err)

			var excludes []string
			if parsed.excludeNodeTypesPassed {
				if parsed.ExcludeNodeTypes != nil {
					excludes = *parsed.ExcludeNodeTypes
				} else {
					excludes = []string{}
				}
			} else if parsed.Direction == "both" && containsFold(parsed.NodeTypes, "LoadBalancer") {
				excludes = append(excludes, kgTraverseLoadBalancerAutoExclude...)
			}

			if tc.expected == nil {
				assert.Nil(t, excludes, "expected no excludes applied")
			} else {
				assert.Equal(t, tc.expected, excludes)
			}
		})
	}
}
