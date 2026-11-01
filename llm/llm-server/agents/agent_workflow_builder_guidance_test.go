package agents

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules for steps that touch a cluster or a cloud reach both authoring
// prompts, and the planner no longer tells the model to put account_id in a
// {{ Configs.* }} reference the build and edit prompts forbid.
func TestWorkflowBuilder_ExecutionIdentityRules(t *testing.T) {
	schema := getWorkflowSchema()
	for name, prompt := range map[string]string{
		"build": getBuildSystemPrompt("i", "p", schema),
		"edit":  getEditSystemPrompt("e", "", "", schema),
	} {
		for _, want := range []string{
			"STEPS THAT TOUCH A CLUSTER OR A CLOUD",
			"Prefer a built-in task over scripting.run_script",
			"Never sign in inside a script",
			"One step, one identity",
			"ask the user to connect or extend it",
			"Never invent input values",
		} {
			assert.Contains(t, prompt, want, "%s prompt", name)
		}
	}
	assert.Contains(t, getEditSystemPrompt("e", "", "", schema), "look up that account's kind",
		"an edit must check the step's account kind before moving or dropping its account")
	assert.NotContains(t, planIntegrationAndAccountRule, "integration_id and account_id fields: use {{ Configs")
	assert.Contains(t, planIntegrationAndAccountRule, "never a {{ Configs.* }} reference")
}

func TestBlockingLintMessage(t *testing.T) {
	resp := func(warnings ...map[string]string) []byte {
		b, _ := json.Marshal(map[string]any{"message": "workflow is valid", "warnings": warnings})
		return b
	}
	login := map[string]string{"code": "script_login_code", "task_id": "diag", "message": "task 'diag': the script signs itself in (sts assume-role)."}
	branch := map[string]string{"code": "switch_target_depends_on", "task_id": "b", "message": "task 'b' is a branch of switch 's'."}
	other := map[string]string{"code": "some_future_code", "task_id": "x", "message": "informational"}

	assert.Empty(t, blockingLintMessage(resp(), nil), "no warnings, nothing to fix")
	assert.Empty(t, blockingLintMessage(resp(other), nil), "codes the builder does not know stay informational")
	assert.Empty(t, blockingLintMessage([]byte(`{"message":"workflow is valid"}`), nil), "older servers send no warnings")

	msg := blockingLintMessage(resp(login, other, branch), nil)
	assert.True(t, strings.HasPrefix(msg, "Validation FAILED"))
	assert.Contains(t, msg, "sts assume-role")
	assert.Contains(t, msg, "branch of switch")
	assert.NotContains(t, msg, "informational")

	// On the agent or a VM, assuming another role can be deliberate: login code
	// stays a warning there, while the switch-branch problem still blocks.
	onAgent := func(taskID string) bool { return taskID == "diag" }
	msg = blockingLintMessage(resp(login, branch), onAgent)
	assert.NotContains(t, msg, "sts assume-role")
	assert.Contains(t, msg, "branch of switch")
	assert.Empty(t, blockingLintMessage(resp(login), onAgent))
}

func TestRunsOnCustomerSide(t *testing.T) {
	a := &WorkflowBuilderAgent{}
	a.state.WorkingWorkflow = map[string]interface{}{"definition": map[string]interface{}{"tasks": []interface{}{
		map[string]interface{}{"id": "on-agent", "params": map[string]interface{}{"executor_type": "agent"}},
		map[string]interface{}{"id": "on-ssm", "params": map[string]interface{}{"executor_type": "aws_ssm"}},
		map[string]interface{}{"id": "on-ours", "params": map[string]interface{}{"executor_type": "kubernetes"}},
		map[string]interface{}{"id": "default-unknown-account", "params": map[string]interface{}{}},
	}}}
	onCustomer := a.runsOnCustomerSide(nil)
	assert.True(t, onCustomer("on-agent"))
	assert.True(t, onCustomer("on-ssm"))
	assert.False(t, onCustomer("on-ours"))
	assert.False(t, onCustomer("default-unknown-account"), "with no account kind known, assume Nudgebee's executor and keep blocking")
}

func TestFinalizedAfterFailure(t *testing.T) {
	finalized := map[string]interface{}{"name": "wf", "definition": map[string]interface{}{"tasks": []interface{}{}}}
	b, _ := json.Marshal(finalized)

	a := &WorkflowBuilderAgent{}
	_, ok := a.finalizedAfterFailure("")
	assert.False(t, ok, "a loop that never finalized has nothing to keep")

	a.loopFinalized, a.finalizedSnapshot = true, string(b)
	_, ok = a.finalizedAfterFailure(string(b))
	assert.False(t, ok, "finalizing an unchanged automation is not a change to keep")

	// Work continued after finalize and the model then failed: keep what was
	// finalized (validated), not the unvalidated later state.
	a.state.WorkingWorkflow = map[string]interface{}{"name": "half-edited"}
	out, ok := a.finalizedAfterFailure(`{"name":"before"}`)
	require.True(t, ok)
	assert.Contains(t, out, `"name": "wf"`)
	assert.Equal(t, "wf", a.state.WorkingWorkflow["name"])
}

func setParamsAgent() *WorkflowBuilderAgent {
	a := &WorkflowBuilderAgent{}
	a.state.WorkingWorkflow = map[string]interface{}{"definition": map[string]interface{}{"tasks": []interface{}{
		map[string]interface{}{"id": "a", "type": "scripting.run_script", "params": map[string]interface{}{"executor_type": "kubernetes", "image": "x", "script": "echo a", "language": "bash"}},
		map[string]interface{}{"id": "b", "type": "scripting.run_script", "params": map[string]interface{}{"executor_type": "kubernetes", "script": "echo b", "language": "bash"}},
		map[string]interface{}{"id": "c", "type": "core.print", "params": map[string]interface{}{"message": "hi"}},
	}}}
	return a
}

func taskParams(a *WorkflowBuilderAgent, id string) map[string]interface{} {
	for _, t := range a.state.WorkingWorkflow["definition"].(map[string]interface{})["tasks"].([]interface{}) {
		if task := t.(map[string]interface{}); task["id"] == id {
			return task["params"].(map[string]interface{})
		}
	}
	return nil
}

func TestToolSetTaskParams(t *testing.T) {
	a := setParamsAgent()
	out := a.toolSetTaskParams(nil, map[string]interface{}{
		"task_ids": []interface{}{"a", "b"},
		"set":      map[string]interface{}{"executor_type": "agent"},
		"unset":    []interface{}{"image"},
	})
	assert.True(t, strings.HasPrefix(out, "Updated params on 2 task(s): a, b."), out)
	assert.Equal(t, "agent", taskParams(a, "a")["executor_type"])
	assert.Equal(t, "agent", taskParams(a, "b")["executor_type"])
	assert.NotContains(t, taskParams(a, "a"), "image")
	assert.Equal(t, "echo a", taskParams(a, "a")["script"], "params not named are untouched")
	assert.Equal(t, "hi", taskParams(a, "c")["message"], "tasks not named are untouched")

	a = setParamsAgent()
	out = a.toolSetTaskParams(nil, map[string]interface{}{"task_ids": []interface{}{"a", "nope"}, "set": map[string]interface{}{"executor_type": "agent"}})
	assert.Contains(t, out, "task(s) not found: nope")
	assert.Equal(t, "kubernetes", taskParams(a, "a")["executor_type"], "an unknown id changes nothing")

	out = setParamsAgent().toolSetTaskParams(nil, map[string]interface{}{"task_ids": []interface{}{"a"}})
	assert.Contains(t, out, "provide 'set'")
}

// Steps inside a group or a foreach body are reachable too — the same steps a
// user sees on the canvas.
func TestToolSetTaskParams_NestedTasks(t *testing.T) {
	a := &WorkflowBuilderAgent{}
	a.state.WorkingWorkflow = map[string]interface{}{"definition": map[string]interface{}{"tasks": []interface{}{
		map[string]interface{}{"id": "grp", "type": "core.group", "tasks": []interface{}{
			map[string]interface{}{"id": "in-group", "type": "scripting.run_script", "params": map[string]interface{}{"executor_type": "kubernetes"}},
		}},
		map[string]interface{}{"id": "loop", "type": "core.foreach", "params": map[string]interface{}{"tasks": []interface{}{
			map[string]interface{}{"id": "in-loop", "type": "scripting.run_script"},
		}}},
	}}}
	out := a.toolSetTaskParams(nil, map[string]interface{}{
		"task_ids": []interface{}{"in-group", "in-loop"},
		"set":      map[string]interface{}{"executor_type": "agent"},
	})
	require.True(t, strings.HasPrefix(out, "Updated params on 2 task(s)"), out)

	var seen []string
	walkTaskMaps(a.state.WorkingWorkflow["definition"].(map[string]interface{})["tasks"].([]interface{}), func(task map[string]interface{}) {
		if params, ok := task["params"].(map[string]interface{}); ok && params["executor_type"] == "agent" {
			seen = append(seen, task["id"].(string))
		}
	})
	assert.ElementsMatch(t, []string{"in-group", "in-loop"}, seen)
}

func TestWithoutDefinition(t *testing.T) {
	raw := []byte(`{"status":"FAILED","definition":{"tasks":[{"id":"a","params":{"script":"` + strings.Repeat("x", 5000) + `"}}]},"tasks":{"a":{"error":"boom"}}}`)
	out := withoutDefinition(raw)
	assert.NotContains(t, string(out), `"definition"`)
	assert.Contains(t, string(out), "boom", "run status and errors stay")
	assert.Less(t, len(out), 200)
	assert.Equal(t, []byte("not json"), withoutDefinition([]byte("not json")))
}

func TestRecentConversation(t *testing.T) {
	assert.Empty(t, recentConversation(""))
	assert.Empty(t, recentConversation("No additional context provided."))
	assert.Equal(t, "user: fix it\nassistant: set executor_type to agent?", recentConversation("  user: fix it\nassistant: set executor_type to agent?  "))

	long := strings.Repeat("é", 3000) + "proposal: switch every step to the agent"
	got := recentConversation(long)
	assert.True(t, utf8.ValidString(got), "trimming must not split a character")
	assert.True(t, strings.HasSuffix(got, "proposal: switch every step to the agent"), "the most recent turn survives")
	assert.LessOrEqual(t, len(got), 4000+len("…"))
}

// Prose instructions must not spell out the answer tags. When the model repeats
// such a sentence, the reply parser captures everything after the tag, and the
// user is shown the tail of the prompt instead of an answer.
func TestWorkflowBuilderPromptsDoNotQuoteAnswerTags(t *testing.T) {
	schema := getWorkflowSchema()
	for name, prompt := range map[string]string{
		"read-only": buildReadOnlyPrompt("what does this do?", "{}"),
		"edit":      getEditSystemPrompt("e", "", "", schema),
		"build":     getBuildSystemPrompt("i", "p", schema),
	} {
		assert.NotContains(t, prompt, "<final_answer>", "%s prompt", name)
		assert.NotContains(t, prompt, "<content>", "%s prompt", name)
	}
}
