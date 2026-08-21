package agents

import (
	"strings"
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestScoreAndRankSearchTools_NameBeatsDescription(t *testing.T) {
	cands := []searchToolCandidate{
		{name: "postgres", kind: "agent", description: "Troubleshoot PostgreSQL databases."},
		{name: "recommendations", kind: "agent", description: "RightSizing across pods; also reads from a postgres backend."},
		{name: "helm", kind: "agent", description: "Manage Helm releases."},
	}
	got := scoreAndRankSearchTools("query the postgres database", cands, 15)

	if assert.NotEmpty(t, got) {
		// name hit (postgres) must outrank the tool that only mentions postgres in prose.
		assert.Equal(t, "postgres", got[0].name)
	}
	// helm shares no query token → must not appear.
	for _, c := range got {
		assert.NotEqual(t, "helm", c.name, "unrelated capability should not match")
	}
}

func TestScoreAndRankSearchTools_NoTokensOrNoMatch(t *testing.T) {
	cands := []searchToolCandidate{{name: "helm", description: "Manage Helm releases."}}
	assert.Empty(t, scoreAndRankSearchTools("   ", cands, 15), "blank query yields no matches")
	assert.Empty(t, scoreAndRankSearchTools("kubernetes pods", cands, 15), "no shared token yields no matches")
}

func TestScoreAndRankSearchTools_DeterministicAndLimited(t *testing.T) {
	cands := []searchToolCandidate{
		{name: "mysql", description: "database"},
		{name: "mssql", description: "database"},
		{name: "oracle", description: "database"},
	}
	// All three tie on the "database" description hit (score 1); ordering must be
	// name-asc and the limit must be honored.
	got := scoreAndRankSearchTools("database", cands, 2)
	assert.Len(t, got, 2)
	assert.Equal(t, "mssql", got[0].name)
	assert.Equal(t, "mysql", got[1].name)
}

func TestSummarizeToolInputSchema_RequiredFirst(t *testing.T) {
	schema := toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command":   {Type: toolcore.ToolSchemaTypeString},
			"namespace": {Type: toolcore.ToolSchemaTypeString},
		},
		Required: []string{"command"},
	}
	assert.Equal(t, "command (required), namespace", summarizeToolInputSchema(schema))
	assert.Equal(t, "", summarizeToolInputSchema(toolcore.ToolSchema{}))
}

func TestRenderSearchToolsOutput_ShapeAndInvocationHint(t *testing.T) {
	out := renderSearchToolsOutput([]searchToolCandidate{
		{name: "postgres", kind: "agent", description: "Troubleshoot PostgreSQL."},
		{name: "helm_execute", kind: "tool", description: "Run helm.", inputUsage: "command (required)"},
	})
	assert.Contains(t, out, "delegate_agent", "must tell the planner how to invoke discovered capabilities")
	assert.Contains(t, out, `name="postgres"`)
	assert.Contains(t, out, `kind="tool"`)
	assert.Contains(t, out, "<input>command (required)</input>")
	// Agent entry has no <input> line.
	postgresBlock := out[strings.Index(out, `name="postgres"`):strings.Index(out, `name="helm_execute"`)]
	assert.NotContains(t, postgresBlock, "<input>")
}

func TestParseSearchToolsQuery(t *testing.T) {
	assert.Equal(t, "query mysql", parseSearchToolsQuery(toolcore.NBToolCallRequest{
		Arguments: map[string]any{"query": "  query mysql  "},
	}))
	assert.Equal(t, "list helm releases", parseSearchToolsQuery(toolcore.NBToolCallRequest{
		Command: "list helm releases",
	}))
	assert.Equal(t, "", parseSearchToolsQuery(toolcore.NBToolCallRequest{}))
}
