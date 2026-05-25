package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestCollectSwitchTargetTaskIDs_SingleSwitch(t *testing.T) {
	tasks := []model.Task{
		{
			ID:   "router",
			Type: "core.switch",
			Params: map[string]any{
				"cases": []any{
					map[string]any{"value": "a", "next": "leaf-a"},
					map[string]any{"value": "b", "next": "leaf-b"},
				},
				"default_next": "leaf-default",
			},
		},
		{ID: "leaf-a"}, {ID: "leaf-b"}, {ID: "leaf-default"}, {ID: "after"},
	}
	got := collectSwitchTargetTaskIDs(tasks)
	assert.Equal(t, map[string]bool{
		"leaf-a":       true,
		"leaf-b":       true,
		"leaf-default": true,
	}, got)
}

func TestCollectSwitchTargetTaskIDs_MultipleSwitches(t *testing.T) {
	tasks := []model.Task{
		{ID: "r1", Params: map[string]any{
			"cases":        []any{map[string]any{"value": "x", "next": "a"}},
			"default_next": "b",
		}},
		{ID: "r2", Params: map[string]any{
			"cases": []any{map[string]any{"value": "y", "next": "c"}},
		}},
	}
	got := collectSwitchTargetTaskIDs(tasks)
	assert.Equal(t, map[string]bool{"a": true, "b": true, "c": true}, got)
}

func TestCollectSwitchTargetTaskIDs_NoSwitch(t *testing.T) {
	tasks := []model.Task{
		{ID: "t1", Type: "core.print"},
		{ID: "t2", Type: "data.transform"},
	}
	assert.Empty(t, collectSwitchTargetTaskIDs(tasks))
}

// Legacy `cases[].tasks` is intentionally NOT collected — its multi-task DAGs
// would need transitive marking. Documented as out of scope.
func TestCollectSwitchTargetTaskIDs_LegacyTasksShapeIgnored(t *testing.T) {
	tasks := []model.Task{
		{ID: "router", Params: map[string]any{
			"cases": []any{
				map[string]any{"value": "x", "tasks": []any{
					map[string]any{"id": "embedded-a"},
				}},
			},
		}},
		{ID: "embedded-a"}, // would NOT be filtered — legacy path runs differently
	}
	assert.Empty(t, collectSwitchTargetTaskIDs(tasks))
}

func TestCollectSwitchTargetTaskIDs_MalformedCasesIgnored(t *testing.T) {
	tasks := []model.Task{
		{ID: "router", Params: map[string]any{
			"cases": []any{
				"not-a-map", // skipped
				map[string]any{"value": "x", "next": ""}, // empty next skipped
				map[string]any{"value": "y", "next": "real-target"},
			},
		}},
	}
	got := collectSwitchTargetTaskIDs(tasks)
	assert.Equal(t, map[string]bool{"real-target": true}, got)
}

// --- propagateSwitchBranchState ---

func newSwitchTaskFixture() model.Task {
	return model.Task{
		ID:   "router",
		Type: "core.switch",
		Params: map[string]any{
			"cases": []any{
				map[string]any{"value": "a", "next": "leaf-a"},
				map[string]any{"value": "b", "next": "leaf-b"},
			},
			"default_next": "leaf-default",
		},
	}
}

func TestPropagateSwitchBranchState_SelectedBranchHoisted(t *testing.T) {
	parentCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	childCtx := &TemplateContext{Tasks: map[string]map[string]any{
		"router-leaf-a": {"status": "COMPLETED", "output": map[string]any{"data": "ran-leaf-a"}},
	}}
	rename := map[string]string{"leaf-a": "router-leaf-a", "leaf-b": "router-leaf-b", "leaf-default": "router-leaf-default"}
	done := map[string]bool{}

	propagateSwitchBranchState(
		newSwitchTaskFixture(),
		map[string]any{"selected_case": "a"},
		rename, childCtx, parentCtx, done,
	)

	assert.Equal(t, map[string]any{"status": "COMPLETED", "output": map[string]any{"data": "ran-leaf-a"}},
		parentCtx.Tasks["leaf-a"], "selected branch task data hoisted under original ID")
	assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)},
		parentCtx.Tasks["leaf-b"], "unselected branch stamped SKIPPED")
	assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)},
		parentCtx.Tasks["leaf-default"], "unselected default also stamped SKIPPED")
	assert.Equal(t, map[string]bool{"leaf-a": true, "leaf-b": true, "leaf-default": true}, done)
}

func TestPropagateSwitchBranchState_DefaultBranchSelected(t *testing.T) {
	parentCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	childCtx := &TemplateContext{Tasks: map[string]map[string]any{
		"router-leaf-default": {"status": "COMPLETED", "output": map[string]any{"data": "default-ran"}},
	}}
	rename := map[string]string{"leaf-a": "router-leaf-a", "leaf-b": "router-leaf-b", "leaf-default": "router-leaf-default"}
	done := map[string]bool{}

	propagateSwitchBranchState(
		newSwitchTaskFixture(),
		map[string]any{"selected_case": "default"},
		rename, childCtx, parentCtx, done,
	)

	assert.Equal(t, map[string]any{"status": "COMPLETED", "output": map[string]any{"data": "default-ran"}},
		parentCtx.Tasks["leaf-default"])
	assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)}, parentCtx.Tasks["leaf-a"])
	assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)}, parentCtx.Tasks["leaf-b"])
}

// When `originalToRenamedID` doesn't carry the selected branch (shouldn't
// happen in practice because the executor builds it from the same childWfDef
// the switch task returned), the parent should still mark the branch
// resolved — just without hoisted task data. The convergence task can fall
// back to `default('-')` filters in templates.
func TestPropagateSwitchBranchState_MissingRenamedEntry(t *testing.T) {
	parentCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	childCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	done := map[string]bool{}

	propagateSwitchBranchState(
		newSwitchTaskFixture(),
		map[string]any{"selected_case": "a"},
		map[string]string{}, // empty rename map
		childCtx, parentCtx, done,
	)

	// Selected branch: rename map is empty → child lookup fails → parent
	// gets no entry, but switchBranchDone is still recorded so the
	// downstream dep-check can proceed.
	_, hasSelected := parentCtx.Tasks["leaf-a"]
	assert.False(t, hasSelected, "no parent entry when child data is missing")
	assert.True(t, done["leaf-a"])
	// Unselected branches still stamped SKIPPED.
	assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)}, parentCtx.Tasks["leaf-b"])
}

// Empty / malformed childOutput should not panic and should treat every
// branch as unselected (caller signals selectedCase via that map).
func TestPropagateSwitchBranchState_EmptyChildOutput(t *testing.T) {
	parentCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	childCtx := &TemplateContext{Tasks: map[string]map[string]any{}}
	rename := map[string]string{}
	done := map[string]bool{}

	propagateSwitchBranchState(
		newSwitchTaskFixture(),
		nil, // simulate caller passing through a nil output
		rename, childCtx, parentCtx, done,
	)

	for _, branch := range []string{"leaf-a", "leaf-b", "leaf-default"} {
		assert.Equal(t, map[string]any{"status": string(model.TaskStatusSkipped)}, parentCtx.Tasks[branch])
		assert.True(t, done[branch])
	}
}
