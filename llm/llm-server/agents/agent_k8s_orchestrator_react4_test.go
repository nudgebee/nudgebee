package agents

import (
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// The ReAct4 handle must differ from the primary in exactly one respect: the
// planner engine. Everything the runtime gates on — declared planner type,
// tools, prompt — has to match, or an A/B between the two measures more than
// the planner.
func TestK8sOrchestratorReAct4_MirrorsPrimaryExceptEngine(t *testing.T) {
	variant := newK8sOrchestratorReAct4Agent("acct-1")
	primary := newK8sOrchestratorAgent("acct-1")

	assert.Equal(t, AgentK8sOrchestratorReAct4Name, variant.GetName())
	assert.NotEqual(t, primary.GetName(), variant.GetName(),
		"distinct name is what gives the variant its own prompt-cache slot")

	// Declared type must match the primary so every gate keyed on it (query
	// cap, memory extraction, response formatting) behaves identically.
	assert.Equal(t, primary.GetPlannerType(), variant.GetPlannerType())
	assert.Equal(t, core.AgentPlannerTypeOrchestrating, variant.GetPlannerType())
}

// The variant is pinned; the primary must NOT be. If the marker ever spreads to
// the primary handle it would silently move production traffic onto ReAct4
// with the global flag still off.
func TestK8sOrchestratorReAct4_OnlyVariantIsPinned(t *testing.T) {
	variant, ok := newK8sOrchestratorReAct4Agent("acct-1").(core.NBAgentReAct4Provider)
	assert.True(t, ok, "variant must implement NBAgentReAct4Provider")
	assert.True(t, variant.PrefersReAct4())

	primary, pinned := newK8sOrchestratorAgent("acct-1").(core.NBAgentReAct4Provider)
	if pinned {
		assert.False(t, primary.PrefersReAct4(),
			"the router-selected k8s_orchestrator must never pin itself to ReAct4")
	}
}

// "Debugger"/"k8s_debug" belong to the primary handle. Duplicating them here
// would make those aliases resolve ambiguously.
func TestK8sOrchestratorReAct4_ClaimsNoAliases(t *testing.T) {
	assert.Empty(t, newK8sOrchestratorReAct4Agent("acct-1").GetNameAliases())
	assert.NotEmpty(t, newK8sOrchestratorAgent("acct-1").GetNameAliases(),
		"guards the premise: the primary is the handle that owns the aliases")
}
