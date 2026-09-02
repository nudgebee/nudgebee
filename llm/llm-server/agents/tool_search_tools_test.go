package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

type searchToolsTestTool struct {
	name string
	typ  toolcore.NBToolType
}

func (t searchToolsTestTool) Name() string                     { return t.name }
func (t searchToolsTestTool) GetType() toolcore.NBToolType     { return t.typ }
func (t searchToolsTestTool) Description() string              { return t.name + " description" }
func (t searchToolsTestTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (t searchToolsTestTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

func TestCollectSearchToolCandidates_OnlyConfiguredSpecialistsAndLeafTools(t *testing.T) {
	agentDtos := []core.AgentDto{
		{Name: "postgres", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeReAct, Description: "Postgres specialist"},
		{Name: "mysql", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeReAct, Description: "MySQL specialist"},
		{Name: "datadog_orchestrator", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeOrchestrating},
		{Name: "k8s_orchestrator_native", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeOrchestrating},
		{Name: "", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeOrchestrating},
	}
	enabledTools := []toolcore.NBTool{
		searchToolsTestTool{name: "postgres", typ: toolcore.NBToolTypeAgent},
		searchToolsTestTool{name: "datadog_orchestrator", typ: toolcore.NBToolTypeAgent},
		searchToolsTestTool{name: "automation_restart_pods", typ: toolcore.NBToolTypeTool},
	}

	got := collectSearchToolCandidates(agentDtos, enabledTools)
	names := make([]string, 0, len(got))
	for _, candidate := range got {
		names = append(names, candidate.name)
	}

	assert.Equal(t, []string{"postgres", "automation_restart_pods"}, names)
	assert.Equal(t, "agent", got[0].kind)
	assert.Equal(t, "tool", got[1].kind)
}

func TestCollectSearchToolCandidates_NeverExposesInternalObservabilityProvider(t *testing.T) {
	agentDtos := []core.AgentDto{
		{Name: "aws_metrics", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeReAct},
		{Name: "metrics", Status: core.AgentStatusEnabled, ExecutorType: core.AgentPlannerTypeReAct},
	}
	// A same-name tool can come from an account-sourced/custom registration. It
	// must not turn the internal AWS implementation into a public capability.
	enabledTools := []toolcore.NBTool{
		searchToolsTestTool{name: "aws_metrics", typ: toolcore.NBToolTypeAgent},
		searchToolsTestTool{name: "metrics", typ: toolcore.NBToolTypeAgent},
	}

	got := collectSearchToolCandidates(agentDtos, enabledTools)
	names := make([]string, 0, len(got))
	for _, candidate := range got {
		names = append(names, candidate.name)
	}

	assert.Equal(t, []string{"metrics"}, names)
}

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

// The failure this prefix pass exists for, live: asking by category returned
// only the generic workflow tools — whose descriptions happen to say
// "automations" — while the per-account automation tools, whose names say
// "automation", scored zero and did not appear at all. So a model searching for
// the category was sent back down the path those tools exist to replace.
func TestScoreAndRankSearchTools_PluralQueryFindsSingularName(t *testing.T) {
	cands := []searchToolCandidate{
		{name: "automation_restart_crashlooping_pods", kind: "tool",
			description: "Restart pods stuck in CrashLoopBackOff."},
		{name: "workflow_search", kind: "tool",
			description: "Find the account's automations by what they do."},
	}
	got := scoreAndRankSearchTools("automations", cands, 15)

	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.name)
	}
	assert.Contains(t, names, "automation_restart_crashlooping_pods",
		"a plural query must reach the singular name; this scored zero before")
	// A name hit, even a prefix one, still outranks a description hit.
	if assert.NotEmpty(t, got) {
		assert.Equal(t, "automation_restart_crashlooping_pods", got[0].name)
	}
}

// A prefix hit can lift a capability into the results but must never outrank one
// that genuinely matched, or the weaker signal would start deciding the ranking.
func TestScoreAndRankSearchTools_ExactOutranksPrefix(t *testing.T) {
	cands := []searchToolCandidate{
		{name: "automation_restart_crashlooping_pods", description: "Restart stuck pods."},
		{name: "list_automations", description: "List everything available."},
	}
	got := scoreAndRankSearchTools("automations", cands, 15)
	if assert.Len(t, got, 2) {
		assert.Equal(t, "list_automations", got[0].name, "substring hit must come first")
		assert.Equal(t, "automation_restart_crashlooping_pods", got[1].name)
	}
}

// The floor is what keeps the weaker tier from becoming noise. Without it any
// three-letter query token would drag in every capability that happens to start
// with the same letters.
func TestScoreAndRankSearchTools_ShortPrefixDoesNotMatch(t *testing.T) {
	cands := []searchToolCandidate{{name: "awesome_reports", description: "Generate reports."}}
	assert.Empty(t, scoreAndRankSearchTools("aws", cands, 15),
		"aws must not reach awesome — under the shared-character floor")

	// ...while a long enough shared prefix still does, so the floor is not simply
	// switching the tier off.
	cands = []searchToolCandidate{{name: "ingress_inspect", description: "Inspect ingress rules."}}
	assert.NotEmpty(t, scoreAndRankSearchTools("ingresses", cands, 15),
		"ingresses must reach ingress")
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
