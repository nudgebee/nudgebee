package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlanPatches_Rejections(t *testing.T) {
	cases := map[string]string{
		"not json":             `steps`,
		"no steps":             `{"steps":[]}`,
		"missing id":           `{"steps":[{"title":"x"}]}`,
		"unknown status":       `{"steps":[{"id":"s1","status":"finished"}]}`,
		"closing w/o finding":  `{"steps":[{"id":"h1","status":"refuted"}]}`,
		"kind/status mismatch": `{"steps":[{"id":"h1","kind":"task","status":"supported","finding":"x"}]}`,
		"bad kind":             `{"steps":[{"id":"h1","kind":"idea"}]}`,
		"bad prior":            `{"steps":[{"id":"h1","prior":"certain"}]}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlanPatches(args)
			assert.Error(t, err)
		})
	}
}

func TestParsePlanPatches_NormalizesAndAccepts(t *testing.T) {
	steps, err := ParsePlanPatches(`{"steps":[{"id":" h1 ","status":"Refuted","finding":" exit 130, not 137 ","prior":"HIGH"}]}`)
	require.NoError(t, err)
	require.Len(t, steps, 1)
	assert.Equal(t, PlanStep{ID: "h1", Status: PlanStatusRefuted, Finding: "exit 130, not 137", Prior: "high"}, steps[0])
}

func TestApplyPlanPatches(t *testing.T) {
	plan := ApplyPlanPatches(nil, []PlanStep{
		{ID: "s1", Phase: "Scope", Title: "Find pod and window", Status: PlanStatusInProgress},
		{ID: "h1", Phase: "Verify", Title: "OOMKill", Kind: PlanStepKindHypothesis, Prior: "medium"},
		{ID: "h1a", Title: "check exit code", Parent: "h1"},
		{ID: "x", Title: "nested too deep", Parent: "h1a"},
	})
	require.Len(t, plan, 4)
	assert.Equal(t, PlanStepKindTask, plan[0].Kind)
	assert.Equal(t, PlanStatusOpen, plan[1].Status, "hypothesis defaults to open")
	assert.Equal(t, PlanStatusPending, plan[2].Status, "task defaults to pending")
	assert.Equal(t, "h1", plan[2].Parent)
	assert.Empty(t, plan[3].Parent, "only one level of nesting")

	plan = ApplyPlanPatches(plan, []PlanStep{{ID: "h1", Status: PlanStatusRefuted, Finding: "exit 130"}})
	assert.Equal(t, PlanStatusRefuted, plan[1].Status)
	assert.Equal(t, "OOMKill", plan[1].Title, "unset fields keep their value")
	assert.Equal(t, "medium", plan[1].Prior)
	assert.False(t, PlanStepIsOpen(plan[1]))
	assert.True(t, PlanStepIsOpen(plan[0]))
}

func TestApplyPlanPatches_StatusSetsKind(t *testing.T) {
	plan := ApplyPlanPatches(nil, []PlanStep{{ID: "h2", Title: "Spot preemption", Status: PlanStatusTesting}})
	assert.Equal(t, PlanStepKindHypothesis, plan[0].Kind)
	assert.True(t, PlanStatusIsActive(plan[0].Status))
}

func TestApplyPlanPatches_StatusOfOtherKindKeepsKnownKind(t *testing.T) {
	plan := ApplyPlanPatches(nil, []PlanStep{{ID: "h1", Title: "OOMKill", Kind: PlanStepKindHypothesis}})
	plan = ApplyPlanPatches(plan, []PlanStep{{ID: "h1", Status: PlanStatusDone, Finding: "exit 137"}})
	require.Len(t, plan, 1)
	assert.Equal(t, PlanStepKindHypothesis, plan[0].Kind, "a task status must not turn a hypothesis into a task")
	assert.Equal(t, PlanStatusOpen, plan[0].Status, "the mismatched status is ignored, so the step stays open")
	assert.Equal(t, "exit 137", plan[0].Finding)

	plan = ApplyPlanPatches(plan, []PlanStep{{ID: "h1", Kind: PlanStepKindTask, Status: PlanStatusDone, Finding: "exit 137"}})
	assert.Equal(t, PlanStepKindTask, plan[0].Kind, "an explicit kind change is still allowed")
	assert.Equal(t, PlanStatusDone, plan[0].Status)
}

func TestApplyPlanPatches_NewStepWithoutTitleUsesID(t *testing.T) {
	plan := ApplyPlanPatches(nil, []PlanStep{{ID: "h9", Status: PlanStatusTesting}})
	require.Len(t, plan, 1)
	assert.Equal(t, "h9", plan[0].Title)
}

func TestApplyPlanPatches_NestingIndependentOfOrder(t *testing.T) {
	// Child listed before its parent, and the parent's own parent is invalid:
	// the parent becomes top-level, and the child stays nested under it only if
	// the decision is order-independent (both read the original parents).
	plan := ApplyPlanPatches(nil, []PlanStep{
		{ID: "c", Title: "child", Parent: "p"},
		{ID: "p", Title: "parent", Parent: "missing"},
	})
	assert.Equal(t, "p", plan[0].Parent, "p's parent is unknown, so p is top-level and c may nest under it")
	assert.Empty(t, plan[1].Parent)

	cycle := ApplyPlanPatches(nil, []PlanStep{
		{ID: "a", Title: "a", Parent: "b"},
		{ID: "b", Title: "b", Parent: "a"},
	})
	assert.Empty(t, cycle[0].Parent)
	assert.Empty(t, cycle[1].Parent)
}
