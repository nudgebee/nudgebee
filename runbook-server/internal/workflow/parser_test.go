package workflow

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParse(t *testing.T) {
	t.Run("should parse valid YAML", func(t *testing.T) {
		yamlData := `
name: "Test Workflow"
definition:
  tasks:
    - id: "task-1"
      type: "run_script"
`
		wf, err := Parse([]byte(yamlData))
		require.NoError(t, err)
		assert.Equal(t, "Test Workflow", wf.Name)
		assert.Len(t, wf.Definition.Tasks, 1)
		assert.Equal(t, "task-1", wf.Definition.Tasks[0].ID)
	})

	t.Run("should parse valid JSON", func(t *testing.T) {
		jsonData := `
{
  "name": "Test Workflow JSON",
  "definition": {
    "tasks": [
      {
        "id": "task-json",
        "type": "http_request"
      }
    ]
  }
}
`
		wf, err := Parse([]byte(jsonData))
		require.NoError(t, err)
		assert.Equal(t, "Test Workflow JSON", wf.Name)
		assert.Len(t, wf.Definition.Tasks, 1)
		assert.Equal(t, "task-json", wf.Definition.Tasks[0].ID)
	})

	t.Run("should parse group task with set_vars", func(t *testing.T) {
		yamlData := `
name: "Group Test"
definition:
  tasks:
    - id: "my-group"
      tasks:
        - id: "child-task"
          type: "run_script"
      set_vars:
        final_output: "{{ tasks.child-task.output.result }}"
`
		wf, err := Parse([]byte(yamlData))
		require.NoError(t, err)
		require.Len(t, wf.Definition.Tasks, 1)
		groupTask := wf.Definition.Tasks[0]
		assert.Equal(t, "my-group", groupTask.ID)
		require.Len(t, groupTask.Tasks, 1)
		assert.Equal(t, "child-task", groupTask.Tasks[0].ID)
		require.NotNil(t, groupTask.SetVars)
		assert.Equal(t, "{{ tasks.child-task.output.result }}", groupTask.SetVars["final_output"])
	})

	t.Run("should return error for malformed input", func(t *testing.T) {
		invalidData := `
name: "Test Workflow"
  tasks: - id: "task-1"
`
		_, err := Parse([]byte(invalidData))
		assert.Error(t, err)
	})
}

// TestParseAIMetadata covers the AI-invocation metadata added alongside the
// per-workflow "Allow Nubi to invoke" toggle, including the top-level
// `description:` key. That key has appeared in workflow YAML (and in this
// repo's own test fixtures) all along, but model.Workflow had no field for it
// so yaml.v3 silently discarded it — this locks in that it now survives.
func TestParseAIMetadata(t *testing.T) {
	t.Run("parses description and AI metadata", func(t *testing.T) {
		yamlData := `
name: "Restart payment consumers"
description: "Restarts consumers and drains the stuck queue."
ai_invocable: true
definition:
  llm_description: "Use when payment-service pods are crashlooping with RabbitMQ timeouts."
  triggers:
    - type: manual
  tasks:
    - id: "restart"
      type: "run_script"
`
		wf, err := Parse([]byte(yamlData))
		require.NoError(t, err)
		require.NotNil(t, wf.Description)
		assert.Equal(t, "Restarts consumers and drains the stuck queue.", *wf.Description)
		assert.True(t, wf.AIInvocable)
		assert.Equal(t, "Use when payment-service pods are crashlooping with RabbitMQ timeouts.", wf.Definition.LLMDescription)
		assert.True(t, wf.Definition.HasManualTrigger())
	})

	t.Run("omitting AI metadata leaves the workflow closed to the AI", func(t *testing.T) {
		yamlData := `
name: "Nightly cleanup"
definition:
  triggers:
    - type: schedule
  tasks:
    - id: "cleanup"
      type: "run_script"
`
		wf, err := Parse([]byte(yamlData))
		require.NoError(t, err)
		assert.Nil(t, wf.Description)
		assert.False(t, wf.AIInvocable)
		assert.Empty(t, wf.Definition.LLMDescription)
		assert.False(t, wf.Definition.HasManualTrigger())
	})
}
