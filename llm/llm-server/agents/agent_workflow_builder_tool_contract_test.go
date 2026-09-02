package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A tool call rejected for a missing key must name the keys it did receive.
//
// The bare "Error: task 'id' is required" this replaces was undiagnosable: a build where 11 of 18
// add_task calls failed with that exact string left no record of what the model actually sent, so
// there was no way to tell a renamed key from a nested payload from a non-string value (#35390).
func TestMissingKeyError_NamesTheKeysItReceived(t *testing.T) {
	cases := []struct {
		name        string
		tool        string
		wanted      string
		args        map[string]interface{}
		wantMention []string
	}{
		{
			name:        "renamed key",
			tool:        "add_task",
			wanted:      "id",
			args:        map[string]interface{}{"task_id": "fetch_pods", "type": "k8s.cli"},
			wantMention: []string{`"task_id"`, `"type"`, "add_task", `"id"`},
		},
		{
			name:        "payload nested one level down",
			tool:        "add_task",
			wanted:      "id",
			args:        map[string]interface{}{"task": map[string]interface{}{"id": "fetch_pods"}},
			wantMention: []string{`"task"`},
		},
		{
			name:        "unparseable input wrapped by the JSON fallback",
			tool:        "modify_task",
			wanted:      "task_id",
			args:        map[string]interface{}{"input": "task_id=fetch_pods"},
			wantMention: []string{`"input"`, "modify_task", `"task_id"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := missingKeyError(nil, tc.tool, tc.wanted, tc.args)
			for _, want := range tc.wantMention {
				assert.Contains(t, msg, want, "the rejection must name what was received so the mismatch is diagnosable")
			}
		})
	}
}

// The key can be present and still rejected: empty, or not a string. Telling the model it
// "requires \"id\" at the top level" while listing "id" among the keys it sent is a
// contradiction, and a model that cannot tell which of the three failures it hit is liable to
// re-send the same payload — the exact loop this rejection exists to break.
func TestMissingKeyError_DistinguishesMissingFromEmptyFromWrongType(t *testing.T) {
	missing := missingKeyError(nil, "add_task", "id", map[string]interface{}{"task_id": "t1"})
	empty := missingKeyError(nil, "add_task", "id", map[string]interface{}{"id": "   ", "type": "k8s.cli"})
	wrongType := missingKeyError(nil, "add_task", "id", map[string]interface{}{"id": 42, "type": "k8s.cli"})

	assert.Contains(t, missing, "not among the keys you sent")
	assert.Contains(t, empty, "was empty")
	assert.Contains(t, wrongType, "to be a string")
	assert.Contains(t, wrongType, "you sent int", "name the type actually received")

	// The three must not be interchangeable — that is the whole point.
	assert.NotEqual(t, missing, empty)
	assert.NotEqual(t, empty, wrongType)
	assert.NotEqual(t, missing, wrongType)

	// A key that WAS sent must never be described as absent.
	assert.NotContains(t, empty, "not among the keys you sent")
	assert.NotContains(t, wrongType, "not among the keys you sent")
}

// With no parameters at all there are no keys to echo, so the message has to say that rather
// than render an empty list.
func TestMissingKeyError_NoParametersAtAll(t *testing.T) {
	msg := missingKeyError(nil, "add_task", "id", map[string]interface{}{})
	assert.Contains(t, msg, "no parameters were received")
	assert.NotContains(t, msg, "You sent: .", "an empty key list must not render as an empty sentence")
}

// The keys are echoed in a stable order — an error whose text shuffles between identical calls
// defeats the exact-duplicate detection in runToolLoop.
func TestMissingKeyError_KeyOrderIsStable(t *testing.T) {
	args := map[string]interface{}{"zeta": 1, "alpha": 2, "mid": 3}
	first := missingKeyError(nil, "add_task", "id", args)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, missingKeyError(nil, "add_task", "id", args))
	}
	assert.Less(t, strings.Index(first, "alpha"), strings.Index(first, "mid"))
	assert.Less(t, strings.Index(first, "mid"), strings.Index(first, "zeta"))
}

// finalize does not persist anything — saving happens after the loop — so re-finalizing an
// unchanged definition is a no-op that reads as success. One build spent nine calls in 31 seconds
// re-marshalling the same 2520 bytes (#35390); the second call must break that cycle.
func TestFinalize_RepeatOnUnchangedAutomationIsRejected(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state.WorkingWorkflow = map[string]interface{}{
		"name": "daily-restart-check",
		"definition": map[string]interface{}{
			"version":  "v1",
			"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
			"tasks":    []interface{}{map[string]interface{}{"id": "t1", "type": "k8s.cli"}},
		},
	}

	first := agent.executeWorkflowTool(nil, "finalize", `{"change_summary":"built it"}`, "")
	assert.True(t, json.Valid([]byte(first)), "the first finalize still returns the definition JSON")
	assert.True(t, agent.loopFinalized)

	second := agent.executeWorkflowTool(nil, "finalize", `{"change_summary":"built it"}`, "")
	assert.Contains(t, second, "already finalized")
	assert.False(t, json.Valid([]byte(second)),
		"a repeat must not return the same JSON — an identical reply is what invites another identical call")
}

// A model that changes the automation and finalizes again is doing the right thing; only the
// no-op repeat is thrash.
func TestFinalize_AfterARealChangeIsAllowed(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state.WorkingWorkflow = map[string]interface{}{
		"name": "daily-restart-check",
		"definition": map[string]interface{}{
			"version":  "v1",
			"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
			"tasks":    []interface{}{map[string]interface{}{"id": "t1", "type": "k8s.cli"}},
		},
	}

	_ = agent.executeWorkflowTool(nil, "finalize", `{}`, "")

	def := agent.state.WorkingWorkflow["definition"].(map[string]interface{})
	def["tasks"] = append(def["tasks"].([]interface{}), map[string]interface{}{"id": "t2", "type": "core.noop"})

	second := agent.executeWorkflowTool(nil, "finalize", `{}`, "")
	assert.True(t, json.Valid([]byte(second)), "finalizing a genuinely changed automation must still return its JSON")
	assert.Contains(t, second, "t2")
}

// resolveToolLoopOutcome marshals the working state through toolFinalize when the loop finalized
// a mutated definition. That path must stay pure JSON — the repeat guard lives in the tool
// dispatch, not in toolFinalize, precisely so a guard message can never be persisted as the
// automation definition.
func TestToolFinalize_StaysPureJSONForThePersistencePath(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state.WorkingWorkflow = map[string]interface{}{
		"name":       "daily-restart-check",
		"definition": map[string]interface{}{"version": "v1"},
	}

	_ = agent.executeWorkflowTool(nil, "finalize", `{}`, "")
	_ = agent.executeWorkflowTool(nil, "finalize", `{}`, "")

	assert.True(t, json.Valid([]byte(agent.toolFinalize())),
		"toolFinalize is what resolveToolLoopOutcome persists; it must never carry guard prose")
}
