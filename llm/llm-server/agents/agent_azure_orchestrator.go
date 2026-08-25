package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
)

// Lean-only: delegating/direct dropped 2026-08-11 (#32503 Phase 1).
// The Azure orchestrator now runs the lean-loop implementation under the
// primary handle; the always-direct eval handle (azure_orchestrator_2) and
// the direct/delegating-mode branch were removed with the collapse.
const AgentAzureOrchestratorName = "azure_orchestrator"

func init() {
	// Legacy aliases (`azure_orchestrator_2`, `azure_debug_2`, `azure_orchestrator_lean`,
	// `azure_debug_lean`) kept as registry lookup keys so stored conversation history
	// referencing the pre-collapse eval / lean handles still resolves to the primary.
	// Not surfaced in GetNameAliases so the @-picker isn't polluted.
	core.RegisterNBAgentFactoryWithAliases(AgentAzureOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newAzureOrchestratorAgent(accountId), nil
	}, "azure_debug", "azure_orchestrator_2", "azure_debug_2", "azure_orchestrator_lean", "azure_debug_lean")
	// The lean-only orchestrator preloads the reduced cloud core and reaches
	// specialists on-demand via search_tools + delegate_agent; its tool set is
	// cached per account and must be invalidated on agent-config or enabled-tool
	// changes so a stale surface is not served after a change.
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentAzureOrchestratorName {
			InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorName)
		}
	})
	tocore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentAzureOrchestratorName)
	})
}

// AzureOrchestratorAgent is a deliberately minimal Azure orchestrator: the reduced
// cloud core (azure_execute + service_dependency_graph + events + recommendations
// + websearch + delegate_agent + search_tools) plus a short principle-level
// prompt (agent_azure_lean). Every specialist (databases, kubectl, other clouds,
// github, tickets, …) is dropped from context and reached on-demand via
// search_tools + delegate_agent. Everything else — including the answer critique
// — runs through the same ReAct3 planner under the standard gates.
type AzureOrchestratorAgent struct {
	accountId string
}

// newAzureOrchestratorAgent is the router-selected constructor. Lean-only; the
// former mode switch (delegating / direct / lean) was collapsed away in #32503
// Phase 1.
func newAzureOrchestratorAgent(accountId string) core.NBAgent {
	return &AzureOrchestratorAgent{accountId: accountId}
}

func (a *AzureOrchestratorAgent) GetName() string { return AgentAzureOrchestratorName }

func (a *AzureOrchestratorAgent) GetNameAliases() []string {
	return []string{"azure debug", "microsoft_azure_debug", "azure_debug"}
}

func (a *AzureOrchestratorAgent) GetDescription() string {
	return `Lean-loop Azure SRE/DevOps troubleshooting orchestrator: minimal principle-level prompt, direct azure_execute, specialists reached on-demand via search_tools + delegate_agent.`
}

func (a *AzureOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (a *AzureOrchestratorAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (a *AzureOrchestratorAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }

// IsWatchCapable: drives action sub-agents whose async outcome completes later,
// so it may register a background watch.
func (a *AzureOrchestratorAgent) IsWatchCapable() bool { return true }

// NB: no CritiqueEnabled() method on purpose — the orchestrator does not
// implement NBAgentReActPlannerCritiqueSupport, so critique is governed by the
// standard gate (LlmServerReActCritiqueEnabled && top-level && investigation).

func (a *AzureOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	// Reduced cloud core (azure_execute + SDG + events + recommendations + delegate
	// + search_tools); specialists reached on-demand via search_tools.
	return getCloudLeanSupportedTools(ctx, a.accountId, a.GetName(), tools.ToolExecuteAzureCliCommand)
}

func (a *AzureOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptAzureLean, query.AccountId)
	if promptErr != nil {
		// Return nothing rather than continue: everything appended below is
		// decoration, so carrying on yields a "system prompt" that is just a memory
		// nudge — worse than empty, because it looks like a prompt. MustResolveAll
		// covers default/v1 at startup; this catches a malformed provider- or
		// version-specific override added later.
		ctx.GetLogger().Error("azure orchestrator: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	if n := memoryNudgeIfEnabled(); n != "" {
		promptText += "\n\n" + n
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}
