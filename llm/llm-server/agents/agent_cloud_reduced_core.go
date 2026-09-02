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

// The reduced cloud core is the on-demand-reach-back preloaded set used by the GCP and
// Azure lean orchestrators, the cloud analog of trimmedK8sCoreToolNames (agent_k8s_reduced_core.go).
// A lean orchestrator preloads only its cloud CLI plus the handful of cross-cutting
// investigation tools it calls on nearly every query; every specialist (databases,
// kubectl, other clouds, github, tickets, …) is dropped from context and reached
// on-demand via search_tools + delegate_agent. search_tools is always registered, so it
// survives the enabled-filter.

// cloudLeanCoreToolNames is the lean preloaded set for a cloud orchestrator. cliToolName is
// the cloud's direct CLI tool (gcloud_execute / azure_execute / aws_execute). Observability
// queries route to the provider-specific sub-agents (aws_logs/metrics/traces, gcp_logs/metrics/traces,
// azure_logs/metrics/traces) via the mounted logs, metrics, and traces dispatchers.
func cloudLeanCoreToolNames(cliToolName string) []string {
	names := []string{
		cliToolName,
		LogsAgentName,
		MetricsAgentName,
		TracesAgentName,
		ServiceDependencyGraph,
		EventsAgentName,
		RecommendationsAgentName,
		// websearch is preloaded so the model can ground cloud-specific
		// error-message / API-version investigation questions ("what does
		// gcloud error X mean", "which region supports Y service") without
		// routing through search_tools + delegate_agent first. Parity with
		// aws_lean which has had websearch preloaded from day one.
		WebSearchAgentName,
		// finops is preloaded rather than reached on-demand: unmentioned cost/
		// spend questions on cloud accounts default-route to these orchestrators
		// (the router reaches finops only via @mention), and nothing steers the
		// planner to search for it — without the mount the model hand-writes
		// billing CLI calls instead. Same rationale as the k8s reduced core.
		FinOpsAgentName,
		DelegateAgentToolName,
		SearchToolsToolName,
		// search_skills preload preserved so the model can query knowledge bases
		// by keyword directly, without an extra delegate_agent+search_tools hop.
		// Same invariant PR #34819 established across every orchestrator.
		tools.SearchSkillsToolName,
	}
	// Resource discovery goes through the direct DB tool, NOT the resource_search agent:
	// cloud_resource_search_execute resolves from the unified cloud_resourses inventory in one
	// query (no 12-30s LLM agent, no parallel fan-out). Part of the resource_search agent
	// removal (#32503 Phase 2). AWS additionally relies on it for region resolution (no
	// cross-region list); gcp/azure use it for the same name→inventory resolution.
	names = append(names, tools.ToolCloudResourceSearch)
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

// getCloudLeanSupportedTools resolves the reduced cloud core name list to enabled NBTools
// (account-authorized + configured via GetEnabledNBTools) and merges MCP tools fresh.
// Cached per (accountId, agentName), so each lean handle gets an isolated cache slot.
//
// ALWAYS returns a fresh slice — never the raw cached backing array. Downstream
// planner code appends to this slice (`FilterAndInjectDefaultTools` calls
// `toolList = append(toolList, ...)` at utils.go:122, and other injection paths
// do the same). Returning the cached slice directly would let an in-place append
// mutate the cached entry across concurrent requests / accounts. The MCP-merge
// branch already returned a fresh slice; this makes the no-MCP branch match.
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
