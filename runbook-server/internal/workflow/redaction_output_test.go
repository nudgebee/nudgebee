package workflow

import (
	"testing"

	"nudgebee/runbook/internal/logsafe"
	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Outputs are the author's: printing a credential there is their choice, so a
// step that uses one keeps a readable output while its input is masked.
func TestRedactSecretsFromTasks_LeavesOutputOfSecretStepReadable(t *testing.T) {
	wfDef := model.WorkflowDefinition{Tasks: []model.Task{
		{ID: "call", Type: "integrations.http", Params: map[string]any{"token": "{{ Secrets.k }}", "url": "https://x"}},
	}}
	tasks := []model.TaskExecutionDetails{{
		ID:     "call",
		Input:  map[string]any{"token": "Zx9-canary", "url": "https://x"},
		Output: map[string]any{"status": 200},
	}}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Input["token"])
	assert.Equal(t, "https://x", tasks[0].Input["url"])
	assert.Equal(t, map[string]any{"status": 200}, tasks[0].Output)
}

func TestRedactSecretsFromTasks_LeavesChildOutputReadable(t *testing.T) {
	wfDef := model.WorkflowDefinition{Tasks: []model.Task{
		{ID: "grp", Type: "core.group", Tasks: []model.Task{
			{ID: "inner", Type: "core.print", Params: map[string]any{"message": "{{ Secrets.k }}"}},
		}},
	}}
	tasks := []model.TaskExecutionDetails{{ID: "grp", Children: []model.TaskExecutionDetails{
		{ID: "inner", Input: map[string]any{"message": "Zx9-canary"}, Output: "done"},
	}}}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Equal(t, RedactedValue, tasks[0].Children[0].Input["message"])
	assert.Equal(t, "done", tasks[0].Children[0].Output)
}

// Libraries put the failing request in an error — a URL carrying an api_key —
// without the author printing anything, so every error is scrubbed.
func TestRedactExecutionSecrets_ScrubsTaskChildAndRunErrors(t *testing.T) {
	values := logsafe.SecretValues(map[string]any{"k": "Zx9-canary"})
	details := &model.WorkflowExecutionDetails{
		Error: "run failed: api_key=Zx9-canary",
		Tasks: []model.TaskExecutionDetails{{
			ID:    "call",
			Error: `Get "https://api.example.com?api_key=Zx9-canary": no such host`,
			Children: []model.TaskExecutionDetails{
				{ID: "child", Error: "auth failed for Zx9-canary"},
			},
		}},
	}

	RedactExecutionSecrets(details, model.WorkflowDefinition{}, values)

	assert.Equal(t, "run failed: api_key=***", details.Error)
	assert.Equal(t, `Get "https://api.example.com?api_key=***": no such host`, details.Tasks[0].Error)
	assert.Equal(t, "auth failed for ***", details.Tasks[0].Children[0].Error)
}

// A step that renders a credential without naming Secrets — "{{ Vars.pw }}"
// after an earlier step stashed one — is caught by its value.
func TestRedactExecutionSecrets_ScrubsIndirectCredentialInInputs(t *testing.T) {
	wfDef := model.WorkflowDefinition{Tasks: []model.Task{
		{ID: "stash", SetVars: map[string]any{"pw": "{{ Secrets.k }}"}},
		{ID: "useIt", Params: map[string]any{"message": "value is {{ Vars.pw }}", "url": "https://x"}},
	}}
	rendered := map[string]any{"message": "value is Zx9-canary", "url": "https://x", "list": []any{"Zx9-canary"}}
	details := &model.WorkflowExecutionDetails{Tasks: []model.TaskExecutionDetails{
		{ID: "useIt", Input: rendered, RenderedParams: rendered, Output: "value is Zx9-canary"},
	}}

	RedactExecutionSecrets(details, wfDef, logsafe.SecretValues(map[string]any{"k": "Zx9-canary"}))

	task := details.Tasks[0]
	assert.Equal(t, "value is ***", task.Input["message"])
	assert.Equal(t, "value is ***", task.RenderedParams["message"])
	assert.Equal(t, []any{"***"}, task.RenderedParams["list"])
	assert.Equal(t, "https://x", task.RenderedParams["url"])
	assert.Equal(t, "value is Zx9-canary", task.Output, "outputs stay the author's")
	assert.Equal(t, "value is Zx9-canary", rendered["message"], "the caller's map is not mutated")
}

func TestRedactExecutionSecrets_HandlesNil(t *testing.T) {
	assert.NotPanics(t, func() { RedactExecutionSecrets(nil, model.WorkflowDefinition{}, nil) })
}

func TestRedactSecretsFromTasks_LeavesEmptyOutputAlone(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "run", Type: "core.script", Params: map[string]any{"script": "{{ Secrets.k }}"}},
		},
	}
	tasks := []model.TaskExecutionDetails{{ID: "run"}}

	RedactSecretsFromTasks(tasks, wfDef)

	assert.Nil(t, tasks[0].Output, "a step that produced nothing must not gain a placeholder")
	assert.Empty(t, tasks[0].Error)
}

// The switch router reads Output["routed_to"] during virtualization, which runs
// before redaction. This pins that a switch task carrying no credential keeps a
// readable output so that ordering stays visible if either side moves.
func TestRedactSecretsFromTasks_CleanSwitchOutputStaysReadable(t *testing.T) {
	wfDef := model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "dispatch", Type: "core.switch", Params: map[string]any{"expression": "{{ Tasks.probe.output.kind }}"}},
			{ID: "probe", Type: "core.script", Params: map[string]any{"script": "uname"}},
		},
	}
	tasks := []model.TaskExecutionDetails{
		{ID: "dispatch", Output: map[string]any{"routed_to": []any{"a"}}},
	}

	RedactSecretsFromTasks(tasks, wfDef)

	out, ok := tasks[0].Output.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"a"}, out["routed_to"])
}
