package agents

import (
	"log/slog"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
)

// AgentK8sOrchestratorReAct4Name is an EVALUATION handle: the same lean K8s
// orchestrator as k8s_orchestrator, pinned to the ReAct4 provider-native
// tool-calling planner. It exists so ReAct4 can be exercised in a deployed
// environment via @k8s_orchestrator_react4 without flipping
// LlmServerReAct4Enabled for every account and every agent.
//
// Three properties make it safe to register alongside the primary handle:
//
//   - It is NOT router-selectable. The router matches a hardcoded category list
//     (see chain_router.go GetSystemPrompt), not the agent registry, so this
//     handle only ever runs when a user asks for it by name.
//   - It declares Orchestrating, exactly like the handle it mirrors, so every
//     gate keyed on the declared planner type behaves identically. Only the
//     planner ENGINE differs (via NBAgentReAct4Provider).
//   - Its distinct name yields a distinct prompt-cache slot, so its cached
//     content can never be served to the ReAct3 handle or vice versa.
const AgentK8sOrchestratorReAct4Name = "k8s_orchestrator_react4"

func init() {
	// Registered WITHOUT aliases on purpose: "Debugger"/"k8s_debug" belong to
	// the primary handle, and re-registering them here would make those aliases
	// ambiguous.
	core.RegisterNBAgentFactory(AgentK8sOrchestratorReAct4Name, func(accountId string) (core.NBAgent, error) {
		return newK8sOrchestratorReAct4Agent(accountId), nil
	})
}

// K8sOrchestratorReAct4Agent mirrors the lean K8s orchestrator — same tools,
// same prompt, same declared planner type — and differs only in which planner
// engine executes it. The concrete *K8sLeanAgent is embedded (rather than the
// core.NBAgent interface) so that every optional interface the lean agent
// implements — notebook sections, cache scope, model category, watch
// capability — still type-asserts successfully through the wrapper.
type K8sOrchestratorReAct4Agent struct {
	*K8sLeanAgent
}

// newK8sOrchestratorReAct4Agent builds the ReAct4 mirror. It always mirrors the
// LEAN implementation, which is the production default; if the deployment is in
// K8s-native mode the primary handle runs the native agent instead, so an A/B
// against it would compare two different agents rather than two planners. That
// case is logged loudly rather than silently producing a misleading comparison.
func newK8sOrchestratorReAct4Agent(accountId string) core.NBAgent {
	if strings.EqualFold(strings.TrimSpace(config.Config.K8sOrchestratorMode), K8sOrchestratorModeNative) {
		slog.Warn("k8s_orchestrator_react4: primary handle is in native mode but this eval handle mirrors lean — planner A/B against k8s_orchestrator is NOT apples-to-apples",
			"mode", config.Config.K8sOrchestratorMode, "agent", AgentK8sOrchestratorReAct4Name)
	}
	return &K8sOrchestratorReAct4Agent{
		K8sLeanAgent: newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorReAct4Name),
	}
}

// PrefersReAct4 pins this handle to the native tool-calling planner regardless
// of LlmServerReAct4Enabled. It is still subject to the provider/model
// capability gate, so on a provider without native tool support this handle
// degrades to ReAct3 rather than failing.
func (a *K8sOrchestratorReAct4Agent) PrefersReAct4() bool { return true }

// GetNameAliases returns none — see the registration comment above.
func (a *K8sOrchestratorReAct4Agent) GetNameAliases() []string { return nil }

func (a *K8sOrchestratorReAct4Agent) GetDescription() string {
	return `Evaluation handle: the lean K8s orchestrator running on the ReAct4 provider-native tool-calling planner. Identical tools and prompt to k8s_orchestrator — use it to compare planners side by side.`
}
