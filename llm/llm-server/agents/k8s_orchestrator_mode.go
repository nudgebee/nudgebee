// K8s orchestrator mode resolver. When a request or the env config declares
// a specific K8s orchestrator variant, the router upgrades any K8s-variant
// selection to the corresponding always-that-variant handle.
//
// Signal precedence (highest first):
//  1. Per-request QueryConfig.K8sOrchestratorMode — caller-scoped opt-in.
//  2. Env-wide config.Config.K8sOrchestratorMode — operator's boot-time /
//     fleet-wide declaration.
//
// After the delegating/direct collapse (#32503 Phase 1) only two modes remain:
//
//	"native" → k8s_orchestrator_native  (kubectl-first, lean tool set)
//	"lean"   → k8s_orchestrator         (the primary handle, which runs lean)
//	""       → no override
//
// Lives in the agents package (not agents/core) because it returns the
// concrete agent name constants that are declared here — putting it in core
// would either duplicate the strings or force an init-time registry hop.

package agents

import (
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
)

// k8sOrchestratorModeOverrides maps the mode value to the always-that-variant
// agent name the router should force. Only "native" has a dedicated handle;
// "lean" targets the primary (which runs lean by default), so per-request
// mode="lean" is effectively a re-affirmation of the default rather than a
// separate handle.
var k8sOrchestratorModeOverrides = map[string]string{
	// Native → kubectl-first lean orchestrator (see agent_k8s_orchestrator_native.go).
	K8sOrchestratorModeNative: AgentK8sOrchestratorNativeName,
	// Lean → primary handle (which runs lean by default after the collapse).
	K8sOrchestratorModeLean: AgentK8sOrchestratorName,
}

// ResolveK8sOrchestratorOverride returns the always-that-variant handle name
// to route to, or "" if no override applies. Callers gate on whether the
// current router selection is a K8s variant (see isK8sOrchestratorVariant
// in chain_router.go) before invoking this — non-K8s orchestrators
// (aws/gcp/azure/datadog) must not be upgraded by the K8s mode signal.
func ResolveK8sOrchestratorOverride(request core.NBAgentRequest) string {
	mode := strings.ToLower(strings.TrimSpace(request.QueryConfig.K8sOrchestratorMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(config.Config.K8sOrchestratorMode))
	}
	return k8sOrchestratorModeOverrides[mode]
}
