package agents

import (
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/samber/lo"
)

// The reduced cloud core is the on-demand-reach-back preloaded set used by the GCP and
// Azure lean orchestrators, the cloud analog of trimmedK8sCoreToolNames (agent_k8s_reduced_core.go).
// A lean orchestrator preloads only its cloud CLI plus the handful of cross-cutting
// investigation tools it calls on nearly every query; every specialist (databases,
// kubectl, other clouds, github, tickets, …) is dropped from context and reached
// on-demand via search_tools + delegate_agent. search_tools is always registered, so it
// survives the enabled-filter.

// cloudLeanCoreToolNames is the lean preloaded set for a cloud orchestrator. cliToolName is
// the cloud's direct CLI tool (gcloud_execute / azure_execute) — its observability surface
// (Cloud Logging/Monitoring, Azure Monitor) is the CLI itself, so no separate logs/metrics
// sub-agents are preloaded. The conditional tail (shell/memory/followup) mirrors the k8s
// reduced core, so the ONLY difference from the direct orchestrator is the removed specialists.
func cloudLeanCoreToolNames(cliToolName string) []string {
	names := []string{
		cliToolName,
		ServiceDependencyGraph,
		EventsAgentName,
		ResourceSearchAgentName,
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

// getCloudLeanSupportedTools resolves the reduced cloud core name list to enabled NBTools
// (account-authorized + configured via GetEnabledNBTools) and merges MCP tools fresh.
// Cached per (accountId, agentName), so each lean handle gets an isolated cache slot.
func getCloudLeanSupportedTools(ctx *security.RequestContext, accountId, agentName, cliToolName string) []toolcore.NBTool {
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
		for _, name := range cloudLeanCoreToolNames(cliToolName) {
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
