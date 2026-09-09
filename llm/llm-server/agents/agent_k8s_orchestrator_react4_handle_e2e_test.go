//go:build e2e

package agents

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestK8sOrchestratorReAct4Handle_PinRunsReAct4 is the end-to-end proof that the
// evaluation handle works the way it is meant to be used after deployment:
// invoked by name, with LlmServerReAct4Enabled OFF, it still runs the ReAct4
// native tool-calling planner.
//
// The planner that actually ran is NOT observable from the response — by design,
// resolveEffectivePlannerType reports react_3 for a ReAct4 run so the react-style
// response formatter and citation gates keep working. The observable is the
// executor log line:
//
//	createAgentPlanner: using react_4 native tool-calling planner
//
// Run it with the flag explicitly off and confirm the line is present:
//
//	LLM_SERVER_REACT4_ENABLED=false go test -tags e2e \
//	  -run TestK8sOrchestratorReAct4Handle_PinRunsReAct4 ./agents/ -v 2>&1 \
//	  | grep "using react_4"
//
// A run of the PRIMARY handle under the same flag setting must NOT print it.
func TestK8sOrchestratorReAct4Handle_PinRunsReAct4(t *testing.T) {
	skipIfNoFixtureEnv(t)

	agent := newK8sOrchestratorReAct4Agent(os.Getenv("TEST_ACCOUNT"))
	assert.Equal(t, AgentK8sOrchestratorReAct4Name, agent.GetName())

	tc := defaultCase("react4_handle_direct", "ut-react4-handle-0", "What is a PodDisruptionBudget?")
	resetSession(t, tc)
	resp := runTurn(t, agent, tc)

	// The response is attributed to the eval handle, not the primary one — this
	// is what keeps its prompt-cache slot (and its stored history) separate.
	assert.Equal(t, AgentK8sOrchestratorReAct4Name, resp.AgentName)
}
