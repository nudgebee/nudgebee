package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// AgentK8sOrchestratorNativeName is the K8s-native variant of the K8s
// orchestrator. It exposes a lean, kubectl-first tool set — kubectl_execute
// for every cluster-side operation, standard_diagnostic_grep for
// categorised grep bundles on saved log files, shell_execute for
// post-processing, helm_execute for chart operations, plus the metrics
// and traces sub-agents for historical telemetry. Cloud provider
// wrappers (aws/gcp/azure/datadog), NL-wrapper sub-agents (logs / events
// / resource_search / fetch_logs / service_dependency_graph / server) and
// cluster-agnostic orchestration wrappers (workflow / tickets / github /
// kg / watch / remediation) are intentionally not present — the K8s
// investigation flow the operator opted into is kubectl-native and every
// dropped tool is either impossible on a K8s-only account (cloud) or has a
// more predictable direct-kubectl equivalent (log fetch, discovery, event
// list).
//
// Selection is driven by K8sOrchestratorMode == "native" or the per-request
// QueryConfig.K8sOrchestratorMode override — the router picks this agent
// instead of the sub-agent-heavy k8s_orchestrator when the operator or
// caller has declared K8s-native intent. See agents/core/k8s_native_mode.go.
const AgentK8sOrchestratorNativeName = "k8s_orchestrator_native"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentK8sOrchestratorNativeName, func(accountId string) (core.NBAgent, error) {
		return newK8sNativeAgentNamed(accountId, AgentK8sOrchestratorNativeName), nil
	}, "k8s_debug_native")
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sOrchestratorNativeName {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorNativeName)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorNativeName)
	})
}

// K8sNativeAgent is the K8s-native orchestrator. Same model tier and planner
// as the direct k8s orchestrator (Reasoning + ReAct3), but a hand-curated
// tool set that the LLM sees uniformly for every K8s-native account. No
// runtime tool filtering — the allowlist is declared here so a reader can
// enumerate the K8s-native mode's capabilities from this one file.
type K8sNativeAgent struct {
	accountId string
	// name is the handle this instance runs under. Distinct name → distinct
	// tool cache key.
	name string
}

func newK8sNativeAgentNamed(accountId, name string) *K8sNativeAgent {
	return &K8sNativeAgent{accountId: accountId, name: name}
}

func (a *K8sNativeAgent) GetName() string { return a.name }

// GetNameAliases: aliases[0] is what the app/@-picker shows as the friendly
// display name (useAgentConfiguration.ts:98). Put the human label first,
// technical short-form second so @-invocation via `@k8s_debug_native`
// resolves too.
//
// Under the primary name (env K8sOrchestratorMode=native promotes this agent
// to the k8s_orchestrator handle) return the primary's generic aliases so the
// picker shows a unified "Debugger" — the variant is an implementation detail
// the customer shouldn't see there. Matches K8sLeanAgent's behaviour.
func (a *K8sNativeAgent) GetNameAliases() []string {
	if a.name == AgentK8sOrchestratorName {
		return []string{"Debugger", "k8s_debug"}
	}
	return []string{"Debugger (Native)", "k8s_debug_native"}
}

func (a *K8sNativeAgent) GetDescription() string {
	return `SRE/DevOps troubleshooting expert operating in K8s-native mode: cluster-side operations (logs, events, resources, describe, exec, mutations) run directly through kubectl_execute; historical / custom metrics via the metrics sub-agent; traces via the traces sub-agent; helm operations via helm_execute; shell-side post-processing via shell_execute. Selected when the operator or caller has declared K8s-native intent — optimized for direct cluster diagnostics.`
}

func (a *K8sNativeAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (a *K8sNativeAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (a *K8sNativeAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }

// IsWatchCapable returns false — watch_resource is deliberately NOT in the
// K8s-native allowlist. Write patterns that need to block on completion use
// `kubectl wait --for=condition=... --timeout=<Ns>` inline instead.
func (a *K8sNativeAgent) IsWatchCapable() bool { return false }

// GetSupportedTools returns the K8s-native allowlist. Declared inline (not
// through the sprawling getSupportedTools helper) so the surface is
// self-evident from this file — any addition/removal is a deliberate
// contract change reviewed here.
func (a *K8sNativeAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	names := []string{
		tools.ToolExecuteKubectlCommand,  // cluster access: logs, events, resources, describe, exec, mutations
		tools.ToolExecuteHelmCommand,     // chart install / status / history
		tools.ToolStandardDiagnosticGrep, // categorised crash-bundle grep on saved log files
		MetricsAgentName,                 // historical + custom metrics via PromQL wrapper (Prometheus discovery is non-trivial)
		TracesAgentName,                  // in-cluster trace queries
	}
	if config.Config.LlmServerShellToolEnabled {
		// shell_execute handles post-processing (grep/awk/sort) of kubectl
		// output saved to workspace files. standard_diagnostic_grep also
		// depends on the underlying shell primitive.
		names = append(names, toolcore.ToolExecuteShellCommand)
	}
	if config.Config.LlmServerThinkToolEnabled {
		names = append(names, tools.ThinkToolName)
	}
	var tl []toolcore.NBTool
	for _, n := range names {
		// Defensive nil-check on top of `ok`: GetNBTool's first (system)
		// branch relies on the registered factory returning a non-nil tool,
		// but that's not enforced by the type. Guard here so a stray
		// (nil, true) return never smuggles a nil into the planner's tool
		// list.
		if t, ok := toolcore.GetNBTool(a.accountId, n); ok && t != nil {
			tl = append(tl, t)
		}
	}
	return tl
}

func (a *K8sNativeAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptK8sNative, query.AccountId)
	if promptErr != nil {
		// An empty system prompt would silently degrade the agent, so surface the
		// failure. MustResolveAll covers default/v1 at startup; this catches a
		// malformed provider- or version-specific override added later.
		ctx.GetLogger().Error("k8s native: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	if nudge := memoryNudgeIfEnabled(); nudge != "" {
		promptText += "\n\n" + nudge
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}

// UpdateToolResponseForPlanner reuses the shared kubectl log condenser
// (the native agent runs kubectl directly, so raw log output lands in its
// own scratchpad and benefits from the same size-cap the direct
// orchestrator uses).
func (a *K8sNativeAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return filterKubectlLogResponse(toolRequest, toolResponse)
}
