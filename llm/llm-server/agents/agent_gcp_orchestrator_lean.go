package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
)

// AgentGcpOrchestratorLeanName is the EXPERIMENTAL, @-invocable lean eval handle for GCP
// (mirrors @aws_orchestrator_lean). The router never selects it; invoke via
// @gcp_orchestrator_lean and A/B against @gcp_orchestrator_2 on the same query. When
// GcpOrchestratorMode is "lean", the router-selected primary runs the lean agent under
// AgentGcpOrchestratorName so routing/history/@gcp_debug are unchanged — only the prompt
// differs.
const AgentGcpOrchestratorLeanName = "gcp_orchestrator_lean"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentGcpOrchestratorLeanName, func(accountId string) (core.NBAgent, error) {
		return newGcpLeanAgentNamed(accountId, AgentGcpOrchestratorLeanName), nil
	}, "gcp_debug_lean")
	// The lean agent preloads the reduced cloud core and reaches specialists on-demand
	// via search_tools, so its tool set (cached per account) must be invalidated when the
	// account's agent config or enabled-tool set changes. It is the only path that populates
	// agentSupportedToolsCacheInstance for GCP, and it caches under TWO names: the eval handle
	// AgentGcpOrchestratorLeanName, and — when GcpOrchestratorMode == "lean" — the primary
	// AgentGcpOrchestratorName (the primary runs the lean agent under its own name). Invalidate
	// both, or a lean-mode primary would serve a stale surface after a config/tools change.
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentGcpOrchestratorLeanName {
			InvalidateAgentSupportedToolsCache(accountId, AgentGcpOrchestratorLeanName)
		}
		if agentName == "" || agentName == AgentGcpOrchestratorName {
			InvalidateAgentSupportedToolsCache(accountId, AgentGcpOrchestratorName)
		}
	})
	tocore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentGcpOrchestratorLeanName)
		InvalidateAgentSupportedToolsCache(accountId, AgentGcpOrchestratorName)
	})
}

// GcpLeanAgent is a deliberately minimal GCP orchestrator: a short principle-level prompt
// (agent_gcp_lean.txt) over the reduced cloud core — gcloud_execute + service_dependency_graph
// + events + recommendations + delegate_agent + search_tools — with every specialist (databases,
// kubectl, other clouds, github, tickets, …) dropped from context and reached on-demand via
// search_tools + delegate_agent. Everything else — including the answer critique — runs through
// the same ReAct3 planner under the standard gates, so this isolates the PROMPT + reduced
// surface as the variables.
type GcpLeanAgent struct {
	accountId string
	// name is the handle this instance runs under: AgentGcpOrchestratorLeanName for the
	// always-lean eval handle, or AgentGcpOrchestratorName when the primary runs lean.
	// Distinct name → distinct cache key.
	name string
}

func newGcpLeanAgentNamed(accountId, name string) *GcpLeanAgent {
	return &GcpLeanAgent{accountId: accountId, name: name}
}

func (l *GcpLeanAgent) GetName() string { return l.name }

func (l *GcpLeanAgent) GetNameAliases() []string {
	// Under the primary name it must answer to the primary's aliases so stored history
	// and @gcp_debug invocations keep resolving.
	if l.name == AgentGcpOrchestratorName {
		return []string{"gcp debug", "google_cloud_debug", "gcp_debug"}
	}
	return []string{"gcp_debug_lean"}
}

func (l *GcpLeanAgent) GetDescription() string {
	return `Experimental lean-loop GCP SRE/DevOps troubleshooting agent: minimal principle-level prompt, direct gcloud_execute. For eval only.`
}

func (l *GcpLeanAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (l *GcpLeanAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (l *GcpLeanAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }
func (l *GcpLeanAgent) IsWatchCapable() bool             { return true }

// NB: no CritiqueEnabled() method on purpose — like the heavy/direct GCP orchestrator, the
// lean agent does not implement NBAgentReActPlannerCritiqueSupport, so critique is governed
// by the standard gate (LlmServerReActCritiqueEnabled && top-level && investigation).

func (l *GcpLeanAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	// Reduced cloud core (gcloud_execute + SDG + events + recommendations + delegate +
	// search_tools); specialists reached on-demand via search_tools. Distinct name →
	// distinct tool cache key.
	return getCloudLeanSupportedTools(ctx, l.accountId, l.GetName(), tools.ToolExecuteGcpCliCommand)
}

func (l *GcpLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentGcpLean)
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
