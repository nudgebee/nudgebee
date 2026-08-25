package core

import (
	"strings"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capQueryTestAgent is a minimal NBAgent with a configurable planner type and
// tool set, so the cap gate (orchestrating + shell) can be exercised in both
// directions.
type capQueryTestAgent struct {
	planner AgentPlannerType
	tools   []toolcore.NBTool
}

func (a capQueryTestAgent) GetName() string          { return "cap_query_test_agent" }
func (a capQueryTestAgent) GetNameAliases() []string { return nil }
func (a capQueryTestAgent) GetDescription() string   { return "cap query test agent" }
func (a capQueryTestAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return a.tools
}
func (a capQueryTestAgent) GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a capQueryTestAgent) GetPlannerType() AgentPlannerType { return a.planner }

func shellCapableOrchestrator() capQueryTestAgent {
	return capQueryTestAgent{
		planner: AgentPlannerTypeOrchestrating,
		tools:   []toolcore.NBTool{mockToolWithAliases{name: toolcore.ToolExecuteShellCommand}},
	}
}

// small query is returned verbatim regardless of agent.
func TestCapOrchestratorQuery_UnderBudget(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	q := strings.Repeat("x", maxOrchestratorQueryBytes-1)
	req := NBAgentRequest{Query: q, AccountId: "acct", SessionId: ""}

	got := capOrchestratorQuery(ctx, shellCapableOrchestrator(), req)
	assert.Equal(t, q, got, "under-budget query must be returned unchanged")
}

// Oversized query on a shell-capable orchestrator with no workspace available
// (empty session) is middle-truncated with an explicit marker and NO dangling
// file pointer, and the head + tail survive.
func TestCapOrchestratorQuery_OverBudget_WorkspaceDisabled(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	head := "HEAD_MARKER_UNIQUE"
	tail := "TAIL_MARKER_UNIQUE"
	q := head + strings.Repeat("y", maxOrchestratorQueryBytes*2) + tail
	req := NBAgentRequest{Query: q, AccountId: "acct", SessionId: ""} // empty session disables offload

	got := capOrchestratorQuery(ctx, shellCapableOrchestrator(), req)

	require.NotEqual(t, q, got, "over-budget query must be condensed")
	assert.Less(t, len(got), len(q), "condensed query must be smaller than the original")
	assert.Contains(t, got, head, "head of the query must survive truncation")
	assert.Contains(t, got, tail, "tail of the query must survive truncation")
	assert.Contains(t, got, "full text unavailable", "workspace-disabled path must state the full text is unavailable")
	assert.NotContains(t, got, "workspace file", "workspace-disabled path must not emit a dangling file pointer")
}

// Gate: an oversized query on a non-orchestrating agent is left untouched.
func TestCapOrchestratorQuery_NonOrchestrating_Untouched(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	q := strings.Repeat("z", maxOrchestratorQueryBytes*2)
	req := NBAgentRequest{Query: q, AccountId: "acct", SessionId: ""}

	agent := capQueryTestAgent{
		planner: AgentPlannerTypeReAct,
		tools:   []toolcore.NBTool{mockToolWithAliases{name: toolcore.ToolExecuteShellCommand}},
	}
	got := capOrchestratorQuery(ctx, agent, req)
	assert.Equal(t, q, got, "non-orchestrating agent must not have its query capped")
}

// Gate: an oversized query on an orchestrating agent WITHOUT shell_execute is
// left untouched — it could not act on a grep pointer.
func TestCapOrchestratorQuery_OrchestratorNoShell_Untouched(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	q := strings.Repeat("w", maxOrchestratorQueryBytes*2)
	req := NBAgentRequest{Query: q, AccountId: "acct", SessionId: ""}

	agent := capQueryTestAgent{
		planner: AgentPlannerTypeOrchestrating,
		tools:   []toolcore.NBTool{mockToolWithAliases{name: "some_other_tool"}},
	}
	got := capOrchestratorQuery(ctx, agent, req)
	assert.Equal(t, q, got, "orchestrator without shell_execute must not have its query capped")
}

func TestAgentHasShellTool(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	with := capQueryTestAgent{tools: []toolcore.NBTool{mockToolWithAliases{name: toolcore.ToolExecuteShellCommand}}}
	without := capQueryTestAgent{tools: []toolcore.NBTool{mockToolWithAliases{name: "kubectl"}}}
	req := NBAgentRequest{}
	assert.True(t, agentHasShellTool(ctx, with, req))
	assert.False(t, agentHasShellTool(ctx, without, req))
}
