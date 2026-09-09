//go:build e2e

package agents

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// ============================================================
// Multi-turn conversation continuity
// ============================================================
//
// Every other fixture in this package calls runTest, which wipes the session
// with DeleteConversationBySession before each case — so each one is a
// single-turn conversation on a fresh thread, and NOTHING exercised follow-up
// turns. That gap matters most for ReAct4: it rebuilds the native message list
// (assistant tool_use + tool tool_result pairs) from intermediateSteps on every
// Plan() call, and prior turns arrive through a separate history block. A
// regression in how those two combine is invisible to single-turn tests.
//
// The follow-up queries below are deliberately unanswerable in isolation —
// "one", "that" and "it" have no antecedent without the previous turn — so a
// planner that loses history cannot accidentally pass by answering the literal
// text.

// runTurn executes one turn WITHOUT resetting the session, so turn N sees the
// history of turns 1..N-1. It is otherwise runTest: same baseline assertions,
// same opt-in expectation knobs. Turn 1 of a scenario should reset the session
// explicitly (see resetSession) rather than relying on runTest.
func runTurn(t *testing.T, agent core.NBAgent, tc k8sTestCase) core.NBAgentResponse {
	t.Helper()
	sc := newSC(tc)

	opts := buildRequestOpts(tc)
	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, opts...)

	for _, approval := range tc.ApprovalResponses {
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = sendApproval(t, sc, agent, resp, tc, approval)
		}
	}

	assert.Nil(t, err)
	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.Greater(t, len(resp.Response), 0)

	fmt.Printf("[%s] response: %s\n", tc.Name, resp.Response)
	fmt.Printf("[%s] tools: %d invocations\n", tc.Name, len(resp.AgentStepResponse))

	assertExpectations(t, tc, resp)
	return resp
}

// resetSession clears a session so a multi-turn scenario starts from turn 1.
func resetSession(t *testing.T, tc k8sTestCase) {
	t.Helper()
	assert.Nil(t, core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId))
}

// TestK8sAgent_MultiTurnContinuity_NoTools covers the cheapest continuity case:
// two direct-answer turns where the second refers back with a pronoun. No tools
// are involved, so a failure isolates history propagation itself rather than
// tool-result plumbing.
func TestK8sAgent_MultiTurnContinuity_NoTools(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT"))
	const session = "ut-multiturn-pdb-0"

	turn1 := defaultCase("multiturn_pdb_turn1", session, "What is a PodDisruptionBudget?")
	resetSession(t, turn1)
	runTurn(t, agent, turn1)

	// "one" only resolves via turn 1. A planner that lost history would have to
	// ask what to write an example of.
	turn2 := defaultCase("multiturn_pdb_turn2", session, "Give me a YAML example of one.")
	turn2.WantContainsAny = []string{"PodDisruptionBudget", "minAvailable", "maxUnavailable", "policy/v1"}
	turn2.WantLLMClaims = []string{
		"The answer provides a PodDisruptionBudget example (or explains one) rather than asking the user to clarify what to give an example of.",
	}
	runTurn(t, agent, turn2)
}

// TestK8sAgent_MultiTurnContinuity_AfterToolUse is the ReAct4-critical case: the
// first turn invokes tools, so the follow-up turn must carry BOTH the prior
// tool_use/tool_result exchange and the conversation history. This is the shape
// most likely to break when a planner reconstructs native messages per call.
func TestK8sAgent_MultiTurnContinuity_AfterToolUse(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT"))
	const session = "ut-multiturn-pods-0"

	turn1 := defaultCase("multiturn_pods_turn1", session, "List the pods in the nudgebee namespace.")
	turn1.WantMinToolCalls = 1
	resetSession(t, turn1)
	runTurn(t, agent, turn1)

	// "that namespace" is only resolvable from turn 1. The assertion is
	// deliberately about the SUBJECT being carried, not about a specific pod
	// count, which would be cluster-state dependent and flaky.
	turn2 := defaultCase("multiturn_pods_turn2", session, "How many were in that namespace?")
	turn2.WantLLMClaims = []string{
		"The answer refers to pods in the nudgebee namespace, rather than asking the user which namespace they mean.",
	}
	runTurn(t, agent, turn2)
}
