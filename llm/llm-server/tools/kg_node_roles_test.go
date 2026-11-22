package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The role list here is a copy of the api-server's (a separate Go module);
// this fails when the two drift, before the tool offers a role the graph does
// not know or hides one it does.
func TestKGNodeRolesMatchAPIServer(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "api-server", "services", "knowledge_graph", "core", "node_roles.go"))
	require.NoError(t, err)
	var server []string
	for _, m := range regexp.MustCompile(`NodeRole\w+\s*=\s*"([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		server = append(server, m[1])
	}
	require.NotEmpty(t, server, "no role constants found; fix the pattern")
	ours := append([]string(nil), kgNodeRoleValues...)
	sort.Strings(server)
	sort.Strings(ours)
	assert.Equal(t, server, ours)
}

func TestParseKGSearchInput_Roles(t *testing.T) {
	in, err := parseKGSearchInput(core.NBToolCallRequest{Command: `{"query":"","roles":[" Database ","cache"]}`})
	require.NoError(t, err)
	assert.Equal(t, []string{"database", "cache"}, in.Roles)

	in, err = parseKGSearchInput(core.NBToolCallRequest{Arguments: map[string]any{"roles": []any{"MessageQueue", ""}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"messagequeue"}, in.Roles)
}

func TestValidateKGSearchInput_Roles(t *testing.T) {
	// roles alone is a filter: "find all databases" needs nothing else.
	assert.Nil(t, validateKGSearchInput(kgSearchInput{Roles: []string{"database"}}))

	reject := validateKGSearchInput(kgSearchInput{Roles: []string{"db"}})
	require.NotNil(t, reject)
	assert.Equal(t, core.NBToolResponseStatusError, reject.Status)
	assert.Contains(t, reject.Data, `unknown role "db"`)
	assert.Contains(t, reject.Data, "database, cache, messagequeue")
}

func TestKGSearchNodesTool_SchemaOffersRoles(t *testing.T) {
	schema := KGSearchNodesTool{}.InputSchema()
	roles, ok := schema.Properties["roles"]
	require.True(t, ok)
	assert.Equal(t, kgNodeRoleValuesAny(), roles.Items["enum"])
}

func TestFormatKGSearchResponse_RoleColumn(t *testing.T) {
	out := formatKGSearchResponse(map[string]any{
		"nodes": []any{
			map[string]any{"id": "n1", "name": "redis-master", "node_type": "Workload", "properties": map[string]any{"role": "cache", "engine": "redis"}},
			map[string]any{"id": "n2", "name": "billing-prod", "node_type": "Database", "properties": map[string]any{"engine": "postgres"}},
			map[string]any{"id": "n3", "name": "sw-core-01", "node_type": "CloudResource", "properties": map[string]any{"role": "core-switch"}},
		},
		"total_count": 3,
	}, nil)
	assert.Contains(t, out, "| Type | Role |")
	// Type stays the exact value node_types takes; the role has its own cell.
	assert.Contains(t, out, "| Workload | cache (redis) |")
	assert.Contains(t, out, "| Database |  |")
	// A custom kind's free "role" is not a role.
	assert.NotContains(t, out, "core-switch")
}

func TestFormatKGTraverseResponse_ShowsRole(t *testing.T) {
	out := formatKGTraverseResponse(map[string]any{
		"data": map[string]any{
			"nodes": []any{
				map[string]any{"id": "a", "kind": "Workload", "name": "checkout"},
				map[string]any{"id": "b", "kind": "Workload", "name": "redis-master", "role": "cache", "engine": "redis"},
			},
			"edges": []any{map[string]any{"source_node_id": "a", "dest_node_id": "b", "relationship_type": "CALLS"}},
		},
		"seed_node_ids": []any{"b"},
	}, nil, 50)
	assert.Contains(t, out, "redis-master [id: b] (Workload, acts as cache (redis))")
	assert.Contains(t, out, "- checkout (Workload) [id: a] → CALLS → redis-master (Workload, acts as cache (redis)) [id: b]")
	// Counts stay by type.
	assert.Contains(t, out, "2 Workload")
}

func TestFormatKGGetNodeResponse_Role(t *testing.T) {
	userSet := formatKGGetNodeResponse(map[string]any{
		"id": "n1", "node_type": "Workload",
		"properties": map[string]any{
			"name": "redis-master", "role": "cache", "engine": "redis",
			"role_source": "user", "detected_role": "database", "detected_engine": "postgres",
		},
	})
	assert.Contains(t, userSet, "- Role: cache (redis), set by a user; detected: database (postgres)")
	// The bookkeeping keys are folded into that line, not listed again.
	for _, k := range []string{"role_source", "detected_role", "detected_engine", "role", "engine"} {
		assert.NotContains(t, userSet, "- "+k+":")
	}

	// A custom kind's free role is not a role: no Role line, and it stays a property.
	custom := formatKGGetNodeResponse(map[string]any{
		"id": "n4", "node_type": "CloudResource", "properties": map[string]any{"name": "sw-core-01", "role": "core-switch"},
	})
	assert.NotContains(t, custom, "- Role:")
	assert.Contains(t, custom, "core-switch")

	detected := formatKGGetNodeResponse(map[string]any{
		"id": "n2", "node_type": "Workload", "properties": map[string]any{"name": "pg", "role": "database"},
	})
	assert.Contains(t, detected, "- Role: database, detected")

	none := formatKGGetNodeResponse(map[string]any{"id": "n3", "node_type": "Workload", "properties": map[string]any{"name": "api"}})
	assert.NotContains(t, none, "- Role:")
}
