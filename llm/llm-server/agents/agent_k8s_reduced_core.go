package agents

import (
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/samber/lo"
)

// The reduced k8s core tool set is the on-demand-reach-back preloaded set shared by BOTH
// @k8s_orchestrator_trim (heavy prompt) and the lean orchestrator (minimal prompt). It
// lives in this stable file — not in agent_k8s_orchestrator_trim.go — because the trim
// agent is an experimental eval handle slated for deletion once the reduction question is
// settled, whereas the lean orchestrator is the production default. Keeping the helper
// here decouples the prod path from the experimental agent's lifecycle.

// trimmedK8sCoreToolNames is the lean preloaded set. The conditional tail mirrors
// the production orchestrator's exactly (remediation/shell/memory/followup) so the
// ONLY difference from the direct orchestrator is the removed specialist agents.
// search_tools is always registered, so it survives the enabled-filter; specialists are
// reached on-demand via search_tools + delegate_agent.
func trimmedK8sCoreToolNames() []string {
	names := []string{
		tools.ToolExecuteKubectlCommand,
		LogsAgentName,
		EventsAgentName,
		MetricsAgentName,
		TracesAgentName,
		ResourceSearchAgentName,
		ServiceDependencyGraph,
		RecommendationsAgentName,
		DelegateAgentToolName,
		SearchToolsToolName,
	}
	if config.Config.RemediationAgentEnabled {
		names = append(names, RemediationAgentName)
	}
	if config.Config.LlmServerShellToolEnabled {
		names = append(names, toolcore.ToolExecuteShellCommand)
	}
	names = appendMemoryToolName(names)
	if core.IsAgentsFollowupEnabled() {
		names = append(names, FollowupAgentName)
	}
	return names
}

// getTrimmedK8sSupportedTools resolves the reduced core name list to enabled NBTools
// (account-authorized + configured via GetEnabledNBTools) and merges MCP tools fresh.
// Cached per (accountId, agentName), so trim and lean get isolated cache slots.
func getTrimmedK8sSupportedTools(ctx *security.RequestContext, accountId, agentName string) []toolcore.NBTool {
	var staticTools []toolcore.NBTool
	if cached, ok := agentSupportedToolsCacheInstance.get(accountId, agentName); ok {
		staticTools = cached
	} else {
		enabledTools := toolcore.GetEnabledNBTools(ctx, accountId)
		enabledMap := make(map[string]toolcore.NBTool, len(enabledTools))
		for _, t := range enabledTools {
			enabledMap[strings.ToLower(t.Name())] = t
		}
		agentTools := []toolcore.NBTool{}
		for _, name := range trimmedK8sCoreToolNames() {
			if t, ok := enabledMap[strings.ToLower(name)]; ok {
				agentTools = append(agentTools, t)
			}
		}
		staticTools = lo.UniqBy(agentTools, func(t toolcore.NBTool) string { return t.Name() })
		agentSupportedToolsCacheInstance.set(accountId, agentName, staticTools)
	}

	mcpTools := toolcore.ListMCPIntegrationTools(accountId)
	if len(mcpTools) == 0 {
		return staticTools
	}
	merged := make([]toolcore.NBTool, len(staticTools)+len(mcpTools))
	copy(merged, staticTools)
	copy(merged[len(staticTools):], mcpTools)
	return lo.UniqBy(merged, func(t toolcore.NBTool) string { return t.Name() })
}
