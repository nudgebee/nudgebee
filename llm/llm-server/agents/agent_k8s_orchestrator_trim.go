package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"

	"github.com/samber/lo"
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

// trimmedK8sCoreToolNames is the lean preloaded set. The conditional tail mirrors
// the production orchestrator's exactly (remediation/shell/memory/followup) so the
// ONLY difference from the direct orchestrator is the removed specialist agents.
// search_tools is listed unconditionally but only survives the enabled-filter when
// LLM_SERVER_SEARCH_TOOLS_ENABLED is on (else it is silently absent, and the prompt
// falls back to delegate-by-name — see trimOnDemandInstruction).
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

// getTrimmedK8sSupportedTools resolves the lean core name list to enabled NBTools
// (account-authorized + configured via GetEnabledNBTools) and merges MCP tools
// fresh — mirroring the production getSupportedTools resolution, but over the
// trimmed base list. Kept separate from getSupportedTools so the production path
// is untouched.
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
	merged := make([]toolcore.NBTool, len(staticTools), len(staticTools)+len(mcpTools))
	copy(merged, staticTools)
	merged = append(merged, mcpTools...)
	return lo.UniqBy(merged, func(t toolcore.NBTool) string { return t.Name() })
}

// trimOnDemandInstruction is the override paragraph appended to the k8s prompt. It
// tells the planner its tool set is intentionally lean and to reach specialists it
// does not hold by discovering them with `search_tools` (always registered). The
// base prompt already covers *how* to delegate, so this only redirects away from the
// heavy prompt's "call the specialist directly" guidance.
func trimOnDemandInstruction() string {
	return "**Lean tool set — discover specialists on demand.** Your tool list holds only the core Kubernetes investigation tools. When a task needs a specialist capability you do not already hold (a database, Helm, a cloud CLI, GitHub, a security scan, etc.), use `search_tools` to find it rather than calling it directly. This OVERRIDES any earlier instruction to call a specialized agent directly."
}
