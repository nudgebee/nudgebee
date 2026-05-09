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
