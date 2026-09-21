package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNudgebeeAgentRegistrationAndAlias(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, name := range []string{"nudgebee", "nubi"} {
		agent, ok := core.GetNBAgent(ctx, name, "account-1", "")
		require.True(t, ok, "%s must resolve", name)
		assert.Equal(t, NudgebeeAgentName, agent.GetName())
	}
	assert.True(t, core.IsSystemAgentAlias("nubi"))
}

func TestNudgebeeAgentToolAllowlist(t *testing.T) {
	agent := newNudgebeeAgent("account-1")
	got := map[string]bool{}
	for _, tool := range agent.GetSupportedTools(security.NewRequestContextForSuperAdmin()) {
		got[tool.Name()] = true
	}
	want := []string{
		tools.ToolNudgebeeDocsSearch,
		tools.ToolNudgebeeAccountsList,
		tools.ToolNudgebeeAccountsCount,
		tools.ToolNudgebeeAccountGet,
		tools.ToolNudgebeeIntegrationsList,
		tools.ToolNudgebeeIntegrationsCount,
		tools.ToolNudgebeeIntegrationGetStatus,
	}
	assert.Len(t, got, len(want))
	for _, name := range want {
		assert.True(t, got[name], "missing tool %s", name)
	}
}

func TestNudgebeeAgentEffectiveToolSurfaceIsClosed(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newNudgebeeAgent("account-1")
	supported := agent.GetSupportedTools(ctx)
	effective := core.FilterAndInjectDefaultTools(
		"account-1",
		agent,
		"<skill-lists>must not enable default skills</skill-lists>",
		supported,
		toolcore.AgentCapabilities{},
	)

	got := make([]string, 0, len(effective))
	for _, tool := range effective {
		got = append(got, tool.Name())
	}
	assert.Len(t, got, 7)
	assert.NotContains(t, got, toolcore.ToolExecuteShellCommand)
	assert.NotContains(t, got, tools.LoadSkillsToolName)
}

func TestNudgebeeAgentUsesBoundedNonInvestigationPlanner(t *testing.T) {
	agent := newNudgebeeAgent("account-1")
	optOut, ok := interface{}(agent).(core.DefaultToolsOptOut)
	require.True(t, ok)
	assert.True(t, optOut.OptOutDefaultTools())
	assert.False(t, agent.GetNotebookEnabled())
	assert.Equal(t, 4, agent.GetMaxIterations())
}

func TestNudgebeeAgentPromptSeparatesDocsFromLiveState(t *testing.T) {
	prompt := newNudgebeeAgent("account-1").GetSystemPrompt(nil, core.NBAgentRequest{})
	text := strings.Join(prompt.Instructions, "\n") + "\n" + strings.Join(prompt.Constraints, "\n")
	assert.Contains(t, text, tools.ToolNudgebeeDocsSearch)
	assert.Contains(t, text, tools.ToolNudgebeeIntegrationGetStatus)
	assert.Contains(t, text, "Never answer current state from documentation")
	assert.Contains(t, text, "Never invent or accept a tenant id")
}

func TestGetAgentRoutesNudgebeeNamesToCanonicalAgent(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, name := range []string{"nudgebee", "nubi", "NudgebeeAgent"} {
		agent, ok := getAgent(ctx, name, "account-1")
		require.True(t, ok)
		assert.Equal(t, NudgebeeAgentName, agent.GetName())
	}

	legacyDocs, ok := getAgent(ctx, "nudgebee_docs", "account-1")
	require.True(t, ok)
	assert.Equal(t, WebSearchAgentName, legacyDocs.GetName())
}
