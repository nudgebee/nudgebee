package agents

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestAgentToolOutputs_CarryRelayContract pins the relay contract on the
// agent-as-tool interfaces whose formatted answers were observed being
// re-encoded by a calling orchestrator: k8s_orchestrator received finops's
// markdown table (citations and all) and rewrote it into a raw JSON object as
// the user-facing reply. The tool's rendered Description — the one interface
// the caller actually reads — must keep telling callers the output is
// user-ready markdown to relay, not data to restructure.
func TestAgentToolOutputs_CarryRelayContract(t *testing.T) {
	for _, name := range []string{FinOpsAgentName, RecommendationsAgentName} {
		tool, ok := toolcore.GetNBTool("relay-contract-test", name)
		assert.True(t, ok, "%s must be registered as a tool", name)
		desc := tool.Description()
		assert.Contains(t, desc, "markdown", "%s tool output must declare markdown", name)
		assert.Contains(t, desc, "relay the markdown as-is", "%s tool output must carry the relay instruction", name)
		assert.Contains(t, desc, "do NOT re-encode it into JSON", "%s tool output must forbid re-encoding", name)
	}
}
