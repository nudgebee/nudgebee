package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// resolveToolLoopOutcome decides between "persist a definition" and "surface a
// prose answer" after an agentic tool loop. These tests pin the contract that
// fixes the "Cannot save automation: resolveCloudAccountIds: parse workflow
// JSON" failure: a prose <final_answer> on a no-change turn (question, dry-run
// request) must come back as prose, never be force-parsed as workflow JSON.

func outcomeTestAgent(withWorkflow bool) *WorkflowBuilderAgent {
	agent := newWorkflowBuilderAgent("acct-1")
	if withWorkflow {
		agent.state.WorkingWorkflow = map[string]interface{}{
			"name": "hello-world-print",
			"definition": map[string]interface{}{
				"version":  "v1",
				"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
				"tasks": []interface{}{
					map[string]interface{}{"id": "print_task", "type": "core.print", "params": map[string]interface{}{"message": "Hello World"}},
				},
			},
		}
	}
	return agent
}

// A valid workflow-JSON echo is used verbatim — unchanged behavior.
func TestResolveToolLoopOutcome_ValidJSONEcho(t *testing.T) {
	agent := outcomeTestAgent(true)
	before := agent.workflowSnapshot()

	echo := `{"name": "hello-world-print", "definition": {"tasks": []}}`
	workflowJSON, prose := agent.resolveToolLoopOutcome(echo, before)

	assert.Equal(t, echo, workflowJSON)
	assert.Empty(t, prose)
}

// A prose answer with an unchanged definition (the dry-run / question case) is
// returned as prose — this is the exact scenario that used to produce
// "Cannot save automation: ... invalid character 'a'".
func TestResolveToolLoopOutcome_ProseNoChange(t *testing.T) {
	agent := outcomeTestAgent(true)
	before := agent.workflowSnapshot()

	answer := "The dry run completed successfully. print_task: COMPLETED."
	workflowJSON, prose := agent.resolveToolLoopOutcome(answer, before)

	assert.Empty(t, workflowJSON)
	assert.Equal(t, answer, prose)
}

// A prose answer after a FINALIZED mutation persists the STATE marshal (not
// the echo), so a truncated/malformed echo can no longer lose a legitimate edit.
func TestResolveToolLoopOutcome_ProseAfterFinalizedMutation(t *testing.T) {
	agent := outcomeTestAgent(true)
	before := agent.workflowSnapshot()

	def := agent.state.WorkingWorkflow["definition"].(map[string]interface{})
	def["tasks"] = append(def["tasks"].([]interface{}),
		map[string]interface{}{"id": "notify", "type": "notifications.im", "params": map[string]interface{}{}, "depends_on": []interface{}{"print_task"}})
	agent.loopFinalized = true // the loop called finalize — its commit point

	workflowJSON, prose := agent.resolveToolLoopOutcome("Applied the change: added a notify task.", before)

	assert.Empty(t, prose)
	assert.True(t, isRawWorkflowJSON(workflowJSON), "state marshal must be valid workflow JSON")
	assert.Contains(t, workflowJSON, `"notify"`)
}

// A mutated-but-ABORTED loop (no finalize call — e.g. validation kept failing
// and the model bailed with a prose error) must NOT persist the half-applied
// state; the prose failure explanation is the answer.
func TestResolveToolLoopOutcome_AbortedMutationNotSaved(t *testing.T) {
	agent := outcomeTestAgent(true)
	before := agent.workflowSnapshot()

	def := agent.state.WorkingWorkflow["definition"].(map[string]interface{})
	def["tasks"] = append(def["tasks"].([]interface{}),
		map[string]interface{}{"id": "broken", "type": "no.such.type", "params": map[string]interface{}{}})
	// loopFinalized deliberately left false — the loop never reached finalize.

	answer := "I could not apply the change: validation kept failing on task 'broken'."
	workflowJSON, prose := agent.resolveToolLoopOutcome(answer, before)

	assert.Empty(t, workflowJSON, "aborted edits must not be persisted")
	assert.Equal(t, answer, prose)
}

// Type coercion applied by validate/dry_run on the shared state must NOT count
// as a change: the snapshot itself normalizes, so a definition loaded with
// float-typed integers compares equal before and after a coercing read-only
// tool ran.
func TestResolveToolLoopOutcome_CoercionIsNotAChange(t *testing.T) {
	agent := outcomeTestAgent(true)
	def := agent.state.WorkingWorkflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	tasks[0].(map[string]interface{})["params"].(map[string]interface{})["count"] = 5.0

	before := agent.workflowSnapshot()
	// Simulate toolValidate / toolDryRun normalizing the shared state mid-loop.
	coerceWorkflowTypes(agent.state.WorkingWorkflow)

	workflowJSON, prose := agent.resolveToolLoopOutcome("No changes needed.", before)

	assert.Empty(t, workflowJSON)
	assert.Equal(t, "No changes needed.", prose)
}

// With no working workflow at all, a prose answer is returned as prose.
func TestResolveToolLoopOutcome_NilWorkflowProse(t *testing.T) {
	agent := outcomeTestAgent(false)
	workflowJSON, prose := agent.resolveToolLoopOutcome("I could not load the automation.", "")

	assert.Empty(t, workflowJSON)
	assert.Equal(t, "I could not load the automation.", prose)
}

// The edit/build tool list must expose dry_run — the tool has always been
// dispatchable (executeWorkflowTool) but was invisible to the model, so
// edit-mode dry-run requests dead-ended.
func TestGetWorkflowToolDescriptions_IncludesDryRun(t *testing.T) {
	descriptions := getWorkflowToolDescriptions()
	assert.True(t, strings.Contains(descriptions, "**dry_run**"), "dry_run must be listed in the loop tool descriptions")
}
