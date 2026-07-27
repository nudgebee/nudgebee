package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// AgentK8sOrchestratorTrimName is an EXPERIMENTAL, @-invocable eval handle that
// tests the tool-context-reduction hypothesis (docs/tool-context-reduction.md):
// preload only a lean CORE of tools and reach every specialist on-demand via
// `search_tools` (discovery) + `delegate_agent` (invocation), instead of
// preloading ~28 specialist agents into the planner context. The router never
// selects it; invoke via `@k8s_orchestrator_trim` and A/B against
// `@k8s_orchestrator_2` (direct kubectl, full tool set) on the same query.
// Delete once the reduction question is settled — this is not a shipping agent.
const AgentK8sOrchestratorTrimName = "k8s_orchestrator_trim"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentK8sOrchestratorTrimName, func(accountId string) (core.NBAgent, error) {
		return newK8sTrimAgent(accountId, AgentK8sOrchestratorTrimName), nil
	}, "k8s_debug_trim")
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sOrchestratorTrimName {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorTrimName)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorTrimName)
	})
}

// K8sTrimAgent runs the same direct-kubectl behavior and prompt as the direct k8s
// orchestrator, with exactly ONE variable changed: its preloaded tool set is the
// lean core (kubectl/logs/events/metrics/traces/resource_search/SDG/recommendations
// + delegate_agent + search_tools + the standard shell/memory/remediation
// conditionals). Every specialist agent (databases, helm, github, cloud CLIs, …)
// is dropped from context and reached on-demand. Isolating the tool set as the
// single variable is what makes the A/B meaningful.
type K8sTrimAgent struct {
	accountId string
	name      string
}

func newK8sTrimAgent(accountId, name string) *K8sTrimAgent {
	return &K8sTrimAgent{accountId: accountId, name: name}
}

func (a *K8sTrimAgent) GetName() string { return a.name }

func (a *K8sTrimAgent) GetNameAliases() []string { return []string{"k8s_debug_trim"} }

func (a *K8sTrimAgent) GetDescription() string {
	return `Experimental lean-context SRE/DevOps troubleshooting agent: preloads only core tools; discovers specialists on-demand via search_tools + delegate_agent. For eval only.`
}

func (a *K8sTrimAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (a *K8sTrimAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (a *K8sTrimAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }
func (a *K8sTrimAgent) IsWatchCapable() bool             { return true }

// UpdateToolResponseForPlanner reuses the shared kubectl log condenser — the trim
// agent runs kubectl directly, so raw log output lands in its own scratchpad.
func (a *K8sTrimAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return filterKubectlLogResponse(toolRequest, toolResponse)
}

func (a *K8sTrimAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getTrimmedK8sSupportedTools(ctx, a.accountId, a.GetName())
}

func (a *K8sTrimAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Reuse the shared direct-kubectl k8s prompt verbatim, then append a final
	// override instruction that redirects specialist work to discovery+delegation.
	// The k8s prompt still names postgres/redis/aws as if directly callable; since
	// those are no longer in this agent's tool list, that guidance must be
	// overridden or the planner emits an action the dispatch auth check rejects.
	prompt := renderK8sDebugReactPrompt(ctx, query, true)
	prompt.Instructions = append(prompt.Instructions, trimOnDemandInstruction())
	return prompt
}

// trimmedK8sCoreToolNames and getTrimmedK8sSupportedTools moved to
// agent_k8s_reduced_core.go — the reduced core is now shared with the lean
// orchestrator (the production default), so it must not live in this
// experimental, delete-slated file.

// trimOnDemandInstruction is the override paragraph appended to the k8s prompt. It
// tells the planner its tool set is intentionally lean and to reach specialists it
// does not hold by discovering them with `search_tools` (always registered). The
// base prompt already covers *how* to delegate, so this only redirects away from the
// heavy prompt's "call the specialist directly" guidance.
func trimOnDemandInstruction() string {
	return "**Lean tool set — discover specialists on demand.** Your tool list holds only the core Kubernetes investigation tools. When a task needs a specialist capability you do not already hold (a database, Helm, a cloud CLI, GitHub, a security scan, etc.), use `search_tools` to find it rather than calling it directly. This OVERRIDES any earlier instruction to call a specialized agent directly."
}
