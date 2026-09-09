package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

// core.approval replays as SCHEDULED until a terminal event lands; this is what stops the
// UI from reading that reconstruction as "a human still has to answer this".
func TestReconcileApprovalTaskStatuses(t *testing.T) {
	newTasks := func() []model.TaskExecutionDetails {
		return []model.TaskExecutionDetails{
			{ID: "top-approval", Type: "core.approval", Status: model.TaskStatusScheduled},
			{ID: "still-pending", Type: "core.approval", Status: model.TaskStatusScheduled},
			{ID: "answered", Type: "core.approval", Status: model.TaskStatusCompleted},
			{ID: "not-an-approval", Type: "core.print", Status: model.TaskStatusScheduled},
			{
				ID:     "group",
				Type:   "core.group",
				Status: model.TaskStatusStarted,
				Children: []model.TaskExecutionDetails{
					{ID: "nested-approval", Type: "core.approval", Status: model.TaskStatusScheduled},
				},
			},
		}
	}
	pending := map[string]struct{}{"still-pending": {}}

	byID := func(tasks []model.TaskExecutionDetails, id string) model.TaskExecutionDetails {
		for _, task := range tasks {
			if task.ID == id {
				return task
			}
		}
		t.Fatalf("task %s missing", id)
		return model.TaskExecutionDetails{}
	}

	// Abandoned gate — COMPLETED here would read as "someone approved it" (#36358).
	t.Run("terminal execution cancels approvals Temporal no longer lists", func(t *testing.T) {
		tasks := newTasks()
		reconcileApprovalTaskStatuses(tasks, pending, true)

		assert.Equal(t, model.TaskStatusCanceled, byID(tasks, "top-approval").Status)
		assert.Equal(t, model.TaskStatusCanceled, byID(tasks, "group").Children[0].Status, "nested approvals must be reconciled too")
		assert.Equal(t, model.TaskStatusScheduled, byID(tasks, "still-pending").Status, "an approval Temporal still lists stays pending")
		assert.Equal(t, model.TaskStatusCompleted, byID(tasks, "answered").Status)
		assert.Equal(t, model.TaskStatusScheduled, byID(tasks, "not-an-approval").Status, "only core.approval tasks are reconciled")
	})

	// A run still going moved past the gate, so it was answered (#32891).
	t.Run("running execution completes approvals Temporal no longer lists", func(t *testing.T) {
		tasks := newTasks()
		reconcileApprovalTaskStatuses(tasks, pending, false)

		assert.Equal(t, model.TaskStatusCompleted, byID(tasks, "top-approval").Status)
		assert.Equal(t, model.TaskStatusCompleted, byID(tasks, "group").Children[0].Status)
		assert.Equal(t, model.TaskStatusScheduled, byID(tasks, "still-pending").Status)
	})
}
