package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestK8sNativeAgent_Identity pins the agent's identity + planner + tier
// invariants. Any change to these has downstream cache / routing / model-
// selection implications and should be a conscious decision.
func TestK8sNativeAgent_Identity(t *testing.T) {
	a := newK8sNativeAgentNamed("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", AgentK8sOrchestratorNativeName)
	assert.Equal(t, AgentK8sOrchestratorNativeName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeOrchestrating, a.GetPlannerType())
	assert.Equal(t, core.ModelTierReasoning, a.GetModelCategory())
	assert.Equal(t, core.CacheScopeAccount, a.GetCacheScope())
	assert.False(t, a.IsWatchCapable(), "watch tools are not in native allowlist")
}

// TestK8sNativeAgent_Registered verifies the factory is wired so the router
// (and any @-invocation) can resolve the agent by name.
func TestK8sNativeAgent_Registered(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, AgentK8sOrchestratorNativeName, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	assert.True(t, ok, "k8s_orchestrator_native must be registered via RegisterNBAgentFactoryWithAliases")
	assert.NotNil(t, agent)
	assert.Equal(t, AgentK8sOrchestratorNativeName, agent.GetName())
}

// TestK8sNativeAgent_GetSupportedTools_Allowlist locks the exact tool set
// the K8s-native agent presents to the planner. The set is deliberately
// hand-curated (not run through the sprawling shared getSupportedTools);
// any addition/removal should surface here as a conscious contract change.
// Tools gated on config flags (think) are asserted conditionally.
func TestK8sNativeAgent_GetSupportedTools_Allowlist(t *testing.T) {
	// Force the think flag on so the assertion covers the maximal set.
	prevThink := config.Config.LlmServerThinkToolEnabled
	config.Config.LlmServerThinkToolEnabled = true
	defer func() {
		config.Config.LlmServerThinkToolEnabled = prevThink
	}()

	ctx := security.NewRequestContextForSuperAdmin()
	a := newK8sNativeAgentNamed("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", AgentK8sOrchestratorNativeName)
	got := a.GetSupportedTools(ctx)

	names := make(map[string]struct{}, len(got))
	for _, tl := range got {
		names[tl.Name()] = struct{}{}
	}

	wantAll := []string{
		tools.ToolExecuteKubectlCommand,
		tools.ToolExecuteHelmCommand,
		tools.ToolStandardDiagnosticGrep,
		MetricsAgentName,
		TracesAgentName,
		toolcore.ToolExecuteShellCommand,
		tools.ThinkToolName,
	}
	for _, n := range wantAll {
		_, ok := names[n]
		assert.True(t, ok, "native toolset must include %q; got %v", n, names)
	}

	// Nothing else — a K8s-native agent that quietly gains a sub-agent (logs,
	// events, resource_search, aws, datadog, …) would silently defeat the
	// purpose of the mode. Fail loudly.
	forbidden := []string{
		LogsAgentName, FetchLogsAgentName, tools.ToolResourceSearch,
		EventsAgentName, "aws", "datadog_orchestrator", "gcp_orchestrator", "azure_orchestrator",
		ServiceDependencyGraph, "server", GithubAgentName,
		"workflow", RecommendationsAgentName,
	}
	for _, n := range forbidden {
		_, ok := names[n]
		assert.False(t, ok, "native toolset must NOT include %q — the whole point of K8s-native mode is to hide it", n)
	}
}

// TestK8sNativeAgent_GetSystemPrompt_NoDroppedSubAgentRefs is the key
// safeguard against the failure mode we caught in the design phase: the
// existing k8s prompt hard-codes references to sub-agents that native mode
// filters out. When we forked to a purpose-written prompt, those references
// must be gone. If any of them leaks back in, the LLM will try to call a
// tool it doesn't have.
func TestK8sNativeAgent_GetSystemPrompt_NoDroppedSubAgentRefs(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	a := newK8sNativeAgentNamed("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", AgentK8sOrchestratorNativeName)
	p := a.GetSystemPrompt(ctx, core.NBAgentRequest{})

	body := strings.Join(p.Instructions, "\n") + "\n" + strings.Join(p.Constraints, "\n")

	// These refs would make the LLM emit a tool call the native toolset
	// doesn't advertise → error observation → wasted turn.
	forbidden := []string{
		"`resource_search`", "`fetch_logs`", "`service_dependency_graph`",
		"`server` agent", "watch_resource",
	}
	for _, f := range forbidden {
		assert.NotContains(t, body, f,
			"native prompt must not reference %s — that sub-agent is dropped from the K8s-native allowlist", f)
	}

	// And these MUST be present — they are the K8s-native flow's core tools
	// named at the orchestrator level. `standard_diagnostic_grep` intentionally
	// is NOT asserted here — the prompt was rewritten to speak at the
	// principle level (categorized error-pattern search) and let each tool's
	// own description name the mechanic.
	mustHave := []string{
		"kubectl_execute",
		"metrics",
		"traces",
	}
	for _, m := range mustHave {
		assert.Contains(t, body, m,
			"native prompt must reference %s — it's part of the allowlist", m)
	}

	// Surface-completeness principle — locks the fix for benchmark 73a/73b
	// (agent concluded "no issues" from status alone) and the redis-PVC case
	// (agent skipped describe). The prompt now requires corroborating evidence
	// from more than one surface before concluding healthy, names each surface
	// (get / describe / logs / metrics) at capability level, and calls out
	// indirect failure signals (not just the literal word "error"). If any
	// of these phrases regresses, the LLM reverts to single-surface confidence
	// and the false-negative "no issues" answers reappear.
	surfaceCompleteness := []string{
		"Surface completeness",
		"more than one surface",
		"kubectl describe",
		"indirect failure signals",
		"single clean surface is not evidence of health",
	}
	for _, r := range surfaceCompleteness {
		assert.Contains(t, body, r,
			"native prompt must retain surface-completeness language: %q", r)
	}
}
