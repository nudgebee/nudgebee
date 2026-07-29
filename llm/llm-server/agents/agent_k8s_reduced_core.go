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

// The reduced k8s core tool set is the on-demand-reach-back preloaded set used by the
// lean orchestrator (the production default): the core investigation tools plus
// search_tools + delegate_agent for reaching specialists on demand, instead of preloading
// the full specialist set. (It was previously also shared with the experimental
// @k8s_orchestrator_trim eval handle, since removed.)

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
