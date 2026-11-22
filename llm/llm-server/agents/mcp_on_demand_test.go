package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withMCPToolsOnDemand(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.LlmServerMCPToolsOnDemandEnabled
	config.Config.LlmServerMCPToolsOnDemandEnabled = on
	t.Cleanup(func() { config.Config.LlmServerMCPToolsOnDemandEnabled = prev })
}

func onDemandAgentTools() []toolcore.NBTool {
	return []toolcore.NBTool{
		toolcore.NewMCPToolForTest("qa", "Use for browser tests of the Nudgebee UI.", "app_map", "Map the app's pages."),
		toolcore.NewMCPToolForTest("qa", "Use for browser tests of the Nudgebee UI.", "screenshot", "Take a screenshot."),
		toolcore.NewMCPUnavailableToolForTest("test-mcp"),
	}
}

func toolNamesOf(ts []toolcore.NBTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

// With MCP tools on demand no orchestrator declares MCP tools or the
// unreachable placeholder; each can reach them through search_tools.
func TestOrchestrators_MCPToolsOnDemand_NotDeclared(t *testing.T) {
	const account = "acct-orch-mcp-on-demand"
	withMCPToolsOnDemand(t, true)
	toolcore.SetMCPIntegrationToolCache(account, onDemandAgentTools())
	defer toolcore.ClearMCPIntegrationToolCache(account)
	sc := security.NewRequestContextForSuperAdmin()

	cases := map[string]func() []toolcore.NBTool{
		"k8s": func() []toolcore.NBTool { return getTrimmedK8sSupportedTools(sc, account, AgentK8sOrchestratorName) },
		"aws": func() []toolcore.NBTool {
			return getCloudLeanSupportedTools(sc, account, AgentAwsOrchestratorName, tools.ToolExecuteAwsCliCommand)
		},
		"gcp": func() []toolcore.NBTool {
			return getCloudLeanSupportedTools(sc, account, AgentGcpOrchestratorName, tools.ToolExecuteGcpCliCommand)
		},
		"azure": func() []toolcore.NBTool {
			return getCloudLeanSupportedTools(sc, account, AgentAzureOrchestratorName, tools.ToolExecuteAzureCliCommand)
		},
		"datadog": func() []toolcore.NBTool { return getDatadogPlannerSupportedTools(sc, account) },
	}
	for name, get := range cases {
		t.Run(name, func(t *testing.T) {
			names := toolNamesOf(get())
			for _, n := range names {
				assert.False(t, strings.HasPrefix(n, "qa_") || strings.HasSuffix(n, "_unavailable"), "%s declares %s", name, n)
			}
			assert.Contains(t, names, SearchToolsToolName)
		})
	}
}

func TestOrchestrators_MCPToolsOnDemandOff_StillDeclared(t *testing.T) {
	const account = "acct-orch-mcp-off"
	withMCPToolsOnDemand(t, false)
	toolcore.SetMCPIntegrationToolCache(account, onDemandAgentTools())
	defer toolcore.ClearMCPIntegrationToolCache(account)
	sc := security.NewRequestContextForSuperAdmin()

	names := toolNamesOf(getTrimmedK8sSupportedTools(sc, account, AgentK8sOrchestratorName))
	assert.Contains(t, names, "qa_app_map")
	assert.Contains(t, names, "test_mcp_unavailable")
	assert.Empty(t, mcpIntegrationsNudge(account))
}

func TestMCPIntegrationsNudge_InOrchestratorPrompts(t *testing.T) {
	const account = "acct-orch-mcp-prompt"
	withMCPToolsOnDemand(t, true)
	toolcore.SetMCPIntegrationToolCache(account, onDemandAgentTools())
	defer toolcore.ClearMCPIntegrationToolCache(account)

	nudge := mcpIntegrationsNudge(account)
	require.Contains(t, nudge, "MCP integrations connected to this account: `qa` (unreachable right now: `test-mcp`")
	assert.NotContains(t, nudge, "browser tests", "integration instructions stay out of the prompt")

	dd := (&DatadogOrchestratorAgent{accountId: account}).GetSystemPrompt(security.NewRequestContextForSuperAdmin(), core.NBAgentRequest{AccountId: account})
	assert.Contains(t, dd.Instructions, nudge)
}

// Size guard: with the flag on, the k8s orchestrator's declarations repeat no
// paragraph and stay under a ceiling however many MCP tools the account has.
// Declared, k8s-dev's 37 MCP tools each carried their integration's
// instructions (~14K tokens a round).
func TestK8sOrchestrator_DeclarationSizeGuard(t *testing.T) {
	const account = "acct-orch-mcp-size"
	withMCPToolsOnDemand(t, true)
	var mcp []toolcore.NBTool
	for i := 0; i < 37; i++ {
		mcp = append(mcp, toolcore.NewMCPToolForTest("qa", strings.Repeat("Use for browser tests of the UI. ", 7), "t"+string(rune('a'+i%26))+string(rune('a'+i/26)), "Step."))
	}
	toolcore.SetMCPIntegrationToolCache(account, mcp)
	defer toolcore.ClearMCPIntegrationToolCache(account)

	rendered := core.RenderToolDescriptions(getTrimmedK8sSupportedTools(security.NewRequestContextForSuperAdmin(), account, AgentK8sOrchestratorName))
	seen := map[string]bool{}
	for _, para := range strings.Split(rendered, "\n\n") {
		// Short lines ("Usage:") repeat by design; the failure this guards
		// against is a whole instructions paragraph copied onto every tool.
		para = strings.TrimSpace(para)
		if len(para) < 80 {
			continue
		}
		assert.False(t, seen[para], "paragraph repeated across declarations: %.80q", para)
		seen[para] = true
	}
	assert.NotContains(t, rendered, "browser tests")
	assert.LessOrEqual(t, len(rendered), 24000, "k8s orchestrator declarations grew past the ceiling")
	t.Logf("k8s orchestrator declarations: %d chars", len(rendered))
}

// search_tools finds an MCP tool by its integration's instructions and name,
// prints the instructions once as a header, and says how many tools of an
// integration the result cap cut.
func TestSearchTools_MCPOnDemandHeaderAndOverflow(t *testing.T) {
	withMCPToolsOnDemand(t, true)
	var enabled []toolcore.NBTool
	for i := 0; i < searchToolsMaxResults+5; i++ {
		enabled = append(enabled, toolcore.NewMCPToolForTest("qa", "Use for browser tests of the Nudgebee UI.", "tool"+string(rune('a'+i)), "Step "+string(rune('a'+i))))
	}
	cands := collectSearchToolCandidates(nil, enabled)
	ranked := rankSearchTools("browser tests", cands)
	require.Len(t, ranked, len(enabled), "every qa tool is found through its integration's instructions")

	out := renderSearchToolsOutput(ranked[:searchToolsMaxResults]) + renderSearchToolsOverflow(ranked[searchToolsMaxResults:])
	assert.Equal(t, 1, strings.Count(out, "Use for browser tests of the Nudgebee UI."))
	assert.Contains(t, out, `<integration name="qa">`)
	assert.Contains(t, out, `integration="qa"`)
	assert.Contains(t, out, "5 more qa tools matched but are not shown")

	assert.NotEmpty(t, rankSearchTools("qa", cands), "the integration name finds its tools")
}
