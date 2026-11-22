package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Once tasks exist, a second init_workflow call can still correct an input that validate
// refuses: each input it carries replaces the one with the same id or is added, and the inputs
// it leaves out are kept.
func TestInitWorkflow_ASecondCallReplacesInputsById(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")
	agent.cachedTaskTypes = "[]"
	require.Contains(t, agent.executeWorkflowTool(nil, "init_workflow", `{"name":"pvc-cleanup","triggers":[{"type":"manual"}],"inputs":[
		{"id":"dry_run","type":"boolean","default":true},
		{"id":"slack_channel","type":"string","default":"{{ Configs.slack_channel }}"}]}`, ""), "initialized")
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("inspect", "approve"), "")

	observation := agent.executeWorkflowTool(nil, "init_workflow", `{"inputs":[
		{"id":"slack_channel","type":"string"},
		{"id":"max_deletions","type":"number","default":25}]}`, "")

	assert.Equal(t, "Automation settings updated: input slack_channel, input max_deletions. The name, triggers, other inputs and 2 task(s) were left as they are.", observation)
	var built struct {
		Name       string `json:"name"`
		Definition struct {
			Inputs []map[string]any `json:"inputs"`
		} `json:"definition"`
	}
	finalized := agent.toolFinalize()
	require.NoError(t, json.Unmarshal([]byte(finalized), &built))
	assert.Equal(t, "pvc-cleanup", built.Name)
	assert.Equal(t, []map[string]any{
		{"id": "dry_run", "type": "boolean", "default": true},
		{"id": "slack_channel", "type": "string"},
		{"id": "max_deletions", "type": "number", "default": float64(25)},
	}, built.Definition.Inputs)
	assert.Equal(t, []string{"inspect", "approve"}, taskIDs(t, finalized))
}

// Inputs the call cannot use change nothing, settings included, and say so, even when some
// entries are usable; an id given twice is reported once, and its last entry wins.
func TestInitWorkflow_ASecondCallRefusesUnusableInputs(t *testing.T) {
	for _, inputs := range []string{`{"id":"a"}`, `[{"type":"string"}]`, `[{"id":"a"},{"type":"string"}]`, `"nope"`, `[]`} {
		agent := initializedBuilder(t)
		agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch"), "")
		before := agent.toolFinalize()

		observation := agent.executeWorkflowTool(nil, "init_workflow", `{"timeout":"15m","inputs":`+inputs+`}`, "")

		assert.Equal(t, `Error: "inputs" must be a list of inputs, each with an "id". Nothing was changed.`, observation, inputs)
		assert.Equal(t, before, agent.toolFinalize(), inputs)
	}

	agent := initializedBuilder(t)
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch"), "")
	observation := agent.executeWorkflowTool(nil, "init_workflow", `{"inputs":[{"id":"a","default":"x"},{"id":"a","default":"y"}]}`, "")
	assert.Equal(t, "Automation settings updated: input a. The name, triggers, other inputs and 1 task(s) were left as they are.", observation)
	var built struct {
		Definition struct {
			Inputs []map[string]any `json:"inputs"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal([]byte(agent.toolFinalize()), &built))
	assert.Equal(t, []map[string]any{{"id": "a", "default": "y"}}, built.Definition.Inputs)
}

// The workflow server refuses a template in an input default; validate tells the model which
// tool changes inputs, since no task tool does.
func TestValidate_SaysHowToFixATemplatedInputDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"validation failed: an input default is used as written and never rendered (slack_channel: \"{{ Configs.slack_channel }}\"): drop the default, leave the input not required, and have the step that reads the input fall back to the template instead, as in {{ Inputs.slack_channel or Configs.slack_channel }}"}`))
	}))
	t.Cleanup(server.Close)
	swap(t, &config.Config.WorkflowServerEndpoint, server.URL)

	agent := newWorkflowBuilderAgent("acct-1")
	require.NoError(t, json.Unmarshal([]byte(`{"name":"pvc-cleanup","definition":{"version":"v1","triggers":[{"type":"manual"}],
		"inputs":[{"id":"slack_channel","type":"string","default":"{{ Configs.slack_channel }}"}],
		"tasks":[{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"{{ Inputs.slack_channel }}","message":"done"}}]}}`), &agent.state.WorkingWorkflow))

	result := agent.executeWorkflowTool(stepBudgetTestContext(), "validate", "", "")

	assert.Contains(t, result, "Validation FAILED")
	assert.Contains(t, result, "an input default is used as written and never rendered")
	assert.Contains(t, result, `Only init_workflow changes inputs: call it with just "inputs", giving each named input no default and "required": false.`)
	assert.Contains(t, result, "fall back to the template, as in {{ Inputs.<id> or Configs.<key> }}, so a caller can still override it")
	assert.Empty(t, agent.validatedSnapshot)
}

// swap sets *p to v for one test.
func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() { *p = prev })
}
