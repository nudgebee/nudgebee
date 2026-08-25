package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsK8sOrchestratorVariant pins the router's list of "K8s orchestrator
// variants" — the set of agent names the RouterAgent.Execute path upgrades
// to k8s_orchestrator_native when the K8s-native signal is on. Any K8s
// variant added later (or a rename of existing ones) must appear here or
// the upgrade will silently miss it. Non-K8s orchestrators (aws / gcp /
// azure / datadog) must NOT match — the mode signal is K8s-scoped.
//
// After the #32503 Phase 1 collapse the surviving handles are primary +
// native; the delegating/direct/lean strings are kept as recognised legacy
// aliases so stored history routes through the override path unchanged.
func TestIsK8sOrchestratorVariant(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		want  bool
	}{
		{"primary k8s_orchestrator", AgentK8sOrchestratorName, true},
		{"already native — still a k8s variant (router's override != plannerAgent guard prevents re-upgrade)", AgentK8sOrchestratorNativeName, true},
		{"legacy alias k8s_debug", "k8s_debug", true},
		{"legacy alias k8s_orchestrator_delegating (pre-collapse handle)", "k8s_orchestrator_delegating", true},
		{"legacy alias k8s_orchestrator_direct (pre-collapse handle)", "k8s_orchestrator_direct", true},
		{"legacy alias k8s_orchestrator_lean (pre-collapse handle)", "k8s_orchestrator_lean", true},
		{"legacy alias k8s_orchestrator_2 kept for back-compat", "k8s_orchestrator_2", true},
		{"legacy alias k8s_debug_2 kept for back-compat", "k8s_debug_2", true},
		{"legacy alias k8s_debug_direct", "k8s_debug_direct", true},
		{"legacy alias k8s_debug_delegating", "k8s_debug_delegating", true},
		{"legacy alias k8s_debug_lean", "k8s_debug_lean", true},
		{"alias k8s_debug_native", "k8s_debug_native", true},
		{"aws orchestrator NOT a k8s variant", "aws_orchestrator", false},
		{"datadog orchestrator NOT a k8s variant", "datadog_orchestrator", false},
		{"unrelated agent", "postgres", false},
		{"empty", "", false},
		{"whitespace normalised", "  k8s_orchestrator  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isK8sOrchestratorVariant(tc.agent))
		})
	}
}
