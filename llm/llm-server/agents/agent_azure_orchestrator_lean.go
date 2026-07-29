package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
)

// AgentAzureOrchestratorLeanName is the EXPERIMENTAL, @-invocable lean eval handle for Azure
// (mirrors @aws_orchestrator_lean). The router never selects it; invoke via
// @azure_orchestrator_lean and A/B against @azure_orchestrator_2 on the same query. When
// AzureOrchestratorMode is "lean", the router-selected primary runs the lean agent under
// AgentAzureOrchestratorName so routing/history/@azure_debug are unchanged — only the prompt
// differs.
const AgentAzureOrchestratorLeanName = "azure_orchestrator_lean"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentAzureOrchestratorLeanName, func(accountId string) (core.NBAgent, error) {
		return newAzureLeanAgentNamed(accountId, AgentAzureOrchestratorLeanName), nil
	}, "azure_debug_lean")
	// The lean agent preloads the reduced cloud core and reaches specialists on-demand
	// via search_tools, so its tool set (cached per account) must be invalidated when the
	// account's agent config or enabled-tool set changes. It is the only path that populates
	// agentSupportedToolsCacheInstance for Azure, and it caches under TWO names: the eval handle
	// AgentAzureOrchestratorLeanName, and — when AzureOrchestratorMode == "lean" — the primary
	// AgentAzureOrchestratorName (the primary runs the lean agent under its own name). Invalidate
	// both, or a lean-mode primary would serve a stale surface after a config/tools change.
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentAzureOrchestratorLeanName {
			InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorLeanName)
		}
		if agentName == "" || agentName == AgentAzureOrchestratorName {
			InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorName)
		}
	})
	tocore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorLeanName)
		InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorName)
	})
}

// AzureLeanAgent is a deliberately minimal Azure orchestrator: a short principle-level prompt
// (agent_azure_lean.txt) over the reduced cloud core — azure_execute + service_dependency_graph
// + events + recommendations + delegate_agent + search_tools — with every specialist (databases,
// kubectl, other clouds, github, tickets, …) dropped from context and reached on-demand via
// search_tools + delegate_agent. Everything else — including the answer critique — runs through
// the same ReAct3 planner under the standard gates, so this isolates the PROMPT + reduced
// surface as the variables.
type AzureLeanAgent struct {
	accountId string
	// name is the handle this instance runs under: AgentAzureOrchestratorLeanName for the
	// always-lean eval handle, or AgentAzureOrchestratorName when the primary runs lean.
	// Distinct name → distinct cache key.
	name string
}

func newAzureLeanAgentNamed(accountId, name string) *AzureLeanAgent {
	return &AzureLeanAgent{accountId: accountId, name: name}
}

func (l *AzureLeanAgent) GetName() string { return l.name }

func (l *AzureLeanAgent) GetNameAliases() []string {
	// Under the primary name it must answer to the primary's aliases so stored history
	// and @azure_debug invocations keep resolving.
	if l.name == AgentAzureOrchestratorName {
		return []string{"azure debug", "microsoft_azure_debug", "azure_debug"}
	}
	return []string{"azure_debug_lean"}
}

func (l *AzureLeanAgent) GetDescription() string {
	return `Experimental lean-loop Azure SRE/DevOps troubleshooting agent: minimal principle-level prompt, direct azure_execute. For eval only.`
}

func (l *AzureLeanAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (l *AzureLeanAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (l *AzureLeanAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }
func (l *AzureLeanAgent) IsWatchCapable() bool             { return true }

// NB: no CritiqueEnabled() method on purpose — like the heavy/direct Azure orchestrator, the
// lean agent does not implement NBAgentReActPlannerCritiqueSupport, so critique is governed
// by the standard gate (LlmServerReActCritiqueEnabled && top-level && investigation).

func (l *AzureLeanAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	// Reduced cloud core (azure_execute + SDG + events + recommendations + delegate +
	// search_tools); specialists reached on-demand via search_tools. Distinct name →
	// distinct tool cache key.
	return getCloudLeanSupportedTools(ctx, l.accountId, l.GetName(), tools.ToolExecuteAzureCliCommand)
}

func (l *AzureLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAzureLean)
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
