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
// the production orchestrator's exactly (remediation/memory/followup) so the
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
		// Resource discovery via the direct DB tools, NOT the resource_search agent
		// (removed, #32503 Phase 2): resource_search_execute resolves k8s resources
		// (with namespace) and cloud_resource_search_execute resolves cloud resources —
		// both over the unified cloud_resourses inventory in one query, no 12-30s LLM
		// agent / parallel fan-out. Both preloaded so the primary/hybrid k8s entrypoint
		// resolves cross-platform without the agent.
		tools.ToolResourceSearch,
		tools.ToolCloudResourceSearch,
		ServiceDependencyGraph,
		RecommendationsAgentName,
		// websearch is preloaded so the model can ground error-message /
		// version-specific investigation questions ("what's the fix for X CVE",
		// "is Y a known bug in kubernetes 1.29") without needing to route via
		// search_tools + delegate_agent first. Parity with aws_lean which has
		// had websearch preloaded from day one; k8s/azure/gcp lean previously
		// omitted it, so error-shape lookups on those paths silently fell back
		// to the model's training knowledge.
		WebSearchAgentName,
		// finops is preloaded rather than reached on-demand: unmentioned cost/
		// spend questions default-route to this agent (the router reaches finops
		// only via @mention), and nothing steers the planner to search for it.
		FinOpsAgentName,
		DelegateAgentToolName,
		SearchToolsToolName,
		// search_skills preload preserved so the model can query knowledge bases
		// by keyword directly, without an extra delegate_agent+search_tools hop.
		// Same invariant PR #34819 established across every orchestrator.
		tools.SearchSkillsToolName,
	}
	if config.Config.RemediationAgentEnabled {
		names = append(names, RemediationAgentName)
	}
	names = append(names, toolcore.ToolExecuteShellCommand)
	names = appendMemoryToolName(names)
	if core.IsAgentsFollowupEnabled() {
		names = append(names, FollowupAgentName)
	}
	return names
}

// getTrimmedK8sSupportedTools resolves the reduced core name list to enabled NBTools
// (account-authorized + configured via GetEnabledNBTools) and merges MCP tools fresh.
// Cached per (accountId, agentName), so trim and lean get isolated cache slots.
//
// ALWAYS returns a fresh slice — never the raw cached backing array. Downstream
// planner code appends to this slice (`FilterAndInjectDefaultTools` calls
// `toolList = append(toolList, ...)` at utils.go:122, and other injection paths
// do the same). Returning the cached slice directly would let an in-place append
// mutate the cached entry across concurrent requests / accounts. The MCP-merge
// branch already returned a fresh slice; this makes the no-MCP branch match.
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
		// Defensive copy: downstream may append; never hand out the cached slice.
		out := make([]toolcore.NBTool, len(staticTools))
		copy(out, staticTools)
		return out
	}
	merged := make([]toolcore.NBTool, len(staticTools)+len(mcpTools))
	copy(merged, staticTools)
	copy(merged[len(staticTools):], mcpTools)
	return lo.UniqBy(merged, func(t toolcore.NBTool) string { return t.Name() })
}
