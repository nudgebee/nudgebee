package ai

import (
	"log/slog"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRouterTask_GetChildWorkflowDefinition(t *testing.T) {
	task := &RouterTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"prompt": "I need help with database",
		"branches": []any{
			map[string]any{
				"name":        "db_issues",
				"description": "Handle database related issues",
				"tasks": []model.Task{
					{
						ID:   "check_db_status",
						Type: "db.check",
					},
				},
			},
			map[string]any{
				"name":        "network_issues",
				"description": "Handle network related issues",
				"tasks": []model.Task{
					{
						ID:   "ping_server",
						Type: "net.ping",
					},
				},
			},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 2)

	// Check Decision Task
	decisionTask := wfDef.Tasks[0]
	assert.Equal(t, "decision", decisionTask.ID)
	assert.Equal(t, "llm.classify", decisionTask.Type)
	assert.Equal(t, "I need help with database", decisionTask.Params["prompt"])
	assert.Equal(t, "{{ Self.output.selected_branch }}", decisionTask.SetVars["router_selected_branch"])

	options, ok := decisionTask.Params["options"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, options, 2)
	assert.Equal(t, "db_issues", options[0]["name"])
	assert.Equal(t, "network_issues", options[1]["name"])

	// Check Dispatch Task. The dispatcher uses the expression-based switch
	// format (core.switch's preferred mode): an `expression` evaluated against
	// `cases`, each case matching by `value` and embedding its `tasks`.
	dispatchTask := wfDef.Tasks[1]
	assert.Equal(t, "dispatch", dispatchTask.ID)
	assert.Equal(t, "core.switch", dispatchTask.Type)
	assert.Equal(t, []string{"decision"}, dispatchTask.DependsOn)
	assert.Equal(t, "{{ router_selected_branch }}", dispatchTask.Params["expression"])

	cases, ok := dispatchTask.Params["cases"].([]map[string]any)
	assert.True(t, ok)
	assert.Len(t, cases, 2)

	// Branch 1
	assert.Equal(t, "db_issues", cases[0]["value"])
	tasks1, ok := cases[0]["tasks"].([]model.Task)
	assert.True(t, ok)
	assert.Len(t, tasks1, 1)
	assert.Equal(t, "check_db_status", tasks1[0].ID)

	// Branch 2
	assert.Equal(t, "network_issues", cases[1]["value"])
	tasks2, ok := cases[1]["tasks"].([]model.Task)
	assert.True(t, ok)
	assert.Len(t, tasks2, 1)
	assert.Equal(t, "ping_server", tasks2[0].ID)

	// Check Output
	assert.Equal(t, "{{ Tasks['decision'].output['selected_branch'] }}", wfDef.Output["selected_branch"])
}

func TestRouterTask_GetChildWorkflowDefinition_MissingParams(t *testing.T) {
	task := &RouterTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	// Missing prompt
	_, err := task.GetChildWorkflowDefinition(ctx, map[string]any{
		"branches": []any{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prompt is required")

	// Missing branches
	_, err = task.GetChildWorkflowDefinition(ctx, map[string]any{
		"prompt": "hello",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "branches are required")
}

func TestRouterTask_GetChildWorkflowDefinition_InvalidBranches(t *testing.T) {
	task := &RouterTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	testCases := []struct {
		name          string
		branches      []any
		expectedError string
	}{
		{
			name: "Branch Missing Name",
			branches: []any{
				map[string]any{
					"description": "desc",
					"tasks":       []model.Task{},
				},
			},
			expectedError: "branch at index 0 is missing a valid name",
		},
		{
			name: "Branch Missing Description",
			branches: []any{
				map[string]any{
					"name":  "branch1",
					"tasks": []model.Task{},
				},
			},
			expectedError: "branch at index 0 is missing a valid description",
		},
		{
			name: "Branch Invalid Structure",
			branches: []any{
				"invalid-string",
			},
			expectedError: "branch at index 0 is invalid",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{
				"prompt":   "test",
				"branches": tc.branches,
			}
			_, err := task.GetChildWorkflowDefinition(ctx, params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedError)
		})
	}
}
