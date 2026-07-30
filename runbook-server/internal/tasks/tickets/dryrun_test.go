package tickets

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No ticket-server endpoint is configured in unit tests, so reaching the
// external call fails. A dry run succeeding proves the task short-circuited
// before any side effect.
func TestTicketWriteTasks_DryRunSkipsSideEffects(t *testing.T) {
	integrationID := uuid.NewString()

	cases := []struct {
		name     string
		task     types.Task
		params   map[string]any
		expected map[string]any
	}{
		{
			name: "create",
			task: &TicketsCreateTask{},
			params: map[string]any{
				"integration_id": integrationID,
				"title":          "Dry run ticket",
				"description":    "should never be created",
				"project_key":    "nudgebee/nudgebee",
			},
			expected: map[string]any{"status": "dry_run", "ticket_id": "dry-run", "action": "dry_run"},
		},
		{
			name: "update",
			task: &TicketsUpdateTask{},
			params: map[string]any{
				"integration_id": integrationID,
				"ticket_id":      "PROJ-1",
				"status":         "Done",
			},
			expected: map[string]any{"ticket_id": "PROJ-1", "status": "dry_run"},
		},
		{
			name: "assign",
			task: &TicketsAssignTask{},
			params: map[string]any{
				"integration_id": integrationID,
				"ticket_id":      "PROJ-1",
				"assignee":       []any{"user-1"},
			},
			expected: map[string]any{"ticket_id": "PROJ-1", "message": "Dry run: ticket assignment skipped"},
		},
		{
			name: "transition",
			task: &TicketsTransitionTask{},
			params: map[string]any{
				"integration_id": integrationID,
				"ticket_id":      "PROJ-1",
				"status":         "In Progress",
			},
			expected: map[string]any{"ticket_id": "PROJ-1", "status": "dry_run"},
		},
		{
			name: "add_comment",
			task: &TicketsAddCommentTask{},
			params: map[string]any{
				"integration_id": integrationID,
				"ticket_id":      "PROJ-1",
				"comment":        "dry run comment",
			},
			expected: map[string]any{"ticket_id": "PROJ-1", "comment": "dry run comment"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskCtx := &testutils.MockTaskContext{Logger: slog.Default(), DryRun: true}

			result, err := tc.task.Execute(taskCtx, tc.params)
			require.NoError(t, err)

			resultMap, ok := result.(map[string]any)
			require.True(t, ok, "result must be a map")
			for key, want := range tc.expected {
				assert.Equal(t, want, resultMap[key])
			}
		})
	}

	// Contrast: the same valid params on a real run must reach the external
	// call (and fail here) — guarantees the dry-run cases above pass because
	// of the guard, not because validation rejected the params earlier.
	t.Run("create without dry run attempts the external call", func(t *testing.T) {
		taskCtx := &testutils.MockTaskContext{Logger: slog.Default(), DryRun: false}

		_, err := (&TicketsCreateTask{}).Execute(taskCtx, map[string]any{
			"integration_id": integrationID,
			"title":          "Dry run ticket",
			"description":    "should never be created",
			"project_key":    "nudgebee/nudgebee",
		})
		assert.Error(t, err)
	})
}
