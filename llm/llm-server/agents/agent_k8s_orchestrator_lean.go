package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// AgentK8sOrchestratorLeanName is an EXPERIMENTAL, @-invocable handle used to test
// the "lean loop" hypothesis: that nudgebee's heavy finish-machinery (rule-dense
// domain prompt + answer critique) suppresses the model's native RCA ability, and
// that a capable model given a short principle-level prompt, direct tools, and no
// critique matches a general agentic harness (agy/claude-code) — which found the
// Helm ownership + durable fix on the same gemini-class model that nudgebee's heavy
// loop stopped short on. Router never selects it; invoke via `@k8s_orchestrator_lean`
// and A/B against `@k8s_orchestrator_2` on the same query. Delete once the loop-shape
// question is settled — this is not a shipping agent.
const AgentK8sOrchestratorLeanName = "k8s_orchestrator_lean"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentK8sOrchestratorLeanName, func(accountId string) (core.NBAgent, error) {
		return newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorLeanName), nil
	}, "k8s_debug_lean")
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sOrchestratorLeanName {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorLeanName)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorLeanName)
	})
}

// K8sLeanAgent is a deliberately minimal orchestrator: same model tier and tools as
// the direct k8s orchestrator (kubectl_execute + helm + logs/events/metrics via the
// shared tool list), but a short principle-level prompt (agent_k8s_lean.txt).
// Everything else — including the answer critique — runs through the same ReAct3
// planner under the standard gates, so this isolates the PROMPT as the single
// variable. Critique is deliberately NOT special-cased here: it is governed by
// LlmServerReActCritiqueEnabled (same as the heavy orchestrator), so its behavior
// on lean answers can be observed independently in real usage.
type K8sLeanAgent struct {
	accountId string
	// name is the handle this instance runs under. The always-lean eval handle
	// uses AgentK8sOrchestratorLeanName; when K8sOrchestratorMode is "lean",
	// the router-selected primary constructs it under AgentK8sOrchestratorName so
	// routing, stored history, and the @k8s_orchestrator handle are unchanged —
	// only the prompt differs. Distinct name → distinct cache key.
	name string
}

func newK8sLeanAgentNamed(accountId, name string) *K8sLeanAgent {
	return &K8sLeanAgent{accountId: accountId, name: name}
}

func (l *K8sLeanAgent) GetName() string { return l.name }

func (l *K8sLeanAgent) GetNameAliases() []string {
	// Under the primary name it must answer to the primary's aliases so stored
	// history and @k8s_debug invocations keep resolving.
	if l.name == AgentK8sOrchestratorName {
		return []string{"Debugger", "k8s_debug"}
	}
	return []string{"k8s_debug_lean"}
}

func (l *K8sLeanAgent) GetDescription() string {
	return `Experimental lean-loop SRE/DevOps troubleshooting agent: minimal principle-level prompt, direct kubectl/helm. For eval only.`
}

func (l *K8sLeanAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (l *K8sLeanAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (l *K8sLeanAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }
func (l *K8sLeanAgent) IsWatchCapable() bool             { return true }

// NB: no CritiqueEnabled() method on purpose — like the heavy k8s orchestrator, the
// lean agent does not implement NBAgentReActPlannerCritiqueSupport, so critique is
// governed by the standard gate (LlmServerReActCritiqueEnabled && top-level &&
// investigation). Keeping critique flag-controlled (not force-off) isolates the
// prompt as the variable and lets us review how critique behaves on lean answers.

func (l *K8sLeanAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Lean now preloads only the reduced core (kubectl_execute + logs/events/metrics/
	// traces/resource_search/SDG/recommendations + delegate_agent + search_tools),
	// shared with @k8s_orchestrator_trim, instead of the full ~28-tool specialist set.
	// Every specialist (databases, helm, cloud CLIs, …) is dropped from context and
	// reached on-demand via search_tools + delegate_agent. This is the tool-context
	// reduction applied to the lean prompt (the "minimal prompt × reduced surface" cell).
	// Distinct name → distinct tool cache key.
	return getTrimmedK8sSupportedTools(ctx, l.accountId, l.GetName())
}

func (l *K8sLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentK8sLean)
	return core.ParsePromptToNBAgentPrompt(promptText)
}

// UpdateToolResponseForPlanner reuses the shared kubectl log condenser (the lean
// agent runs kubectl directly, so raw log output lands in its own scratchpad).
func (l *K8sLeanAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return filterKubectlLogResponse(toolRequest, toolResponse)
}
