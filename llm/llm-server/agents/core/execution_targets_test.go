package core

import (
	"strings"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// The execution-targets lookup needs a database; planner tests run without
// one, so the package's tests use an empty block unless a test sets its own.
func init() {
	renderExecutionTargetsBlock = func(*security.RequestContext, NBAgentRequest, []toolcore.NBTool) string { return "" }
}

func TestExecutionTargetsBlockReachesHumanMessage(t *testing.T) {
	block := "<execution_targets>\nKubernetes clusters: current k8s-prod; others you can use: k8s-dev.\n</execution_targets>"
	for _, parent := range []string{"", "orch-1"} {
		planner := &NBReActPlanner4{
			request:               NBAgentRequest{AgentId: "agent-1", ParentAgentId: parent, AccountId: "acc-1"},
			executionTargetsBlock: block,
		}
		human := planner.humanText("list pods on k8s-dev")
		assert.Contains(t, human, block, "top-level and sub-agents both see the targets (parent=%q)", parent)
		assert.Less(t, strings.Index(human, block), strings.Index(human, "<question>"))
	}

	empty := (&NBReActPlanner4{request: NBAgentRequest{AgentId: "a", AccountId: "acc-1"}}).humanText("q")
	assert.NotContains(t, empty, "execution_targets")
}
