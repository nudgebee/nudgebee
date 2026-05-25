package workflow

import (
	"fmt"
	"nudgebee/runbook/internal/model"
)

// collectSwitchTargetTaskIDs returns the set of task IDs that are reachable
// EXCLUSIVELY via case routing (the modern `cases[].next` / `default_next`
// shape). The executor filters these out of its main scheduling loop —
// they only run as the inline child workflow of the matched case.
//
// Detection keys off the params shape (`params["cases"]` present) rather than
// `t.Type == "core.switch"` so any future task adopting the same routing
// convention is handled uniformly.
func collectSwitchTargetTaskIDs(tasks []model.Task) map[string]bool {
	targets := make(map[string]bool)
	for _, t := range tasks {
		if cases, ok := t.Params["cases"].([]any); ok {
			for _, c := range cases {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if next, _ := cm["next"].(string); next != "" {
					targets[next] = true
				}
			}
		}
		if defaultNext, _ := t.Params["default_next"].(string); defaultNext != "" {
			targets[defaultNext] = true
		}
	}
	return targets
}

// propagateSwitchBranchState mirrors the outcome of a `core.switch` task into
// the parent execution state after its inline child workflow settles. It
// hoists the SELECTED branch's task data from `childCtx` into `parentCtx`
// under the branch's ORIGINAL ID (so user templates like
// `{{ Tasks['leaf-a'].output.data }}` resolve), and stamps UNSELECTED
// branches as `SKIPPED` so the convergence task's dep-check can read their
// `.status`. Resolution is recorded in `switchBranchDone` (NOT
// `completedTasks`) so the parent loop's `len(completedTasks) <
// len(sortedTasks)` stop condition stays intact.
//
// Caller must ensure `childOutput` is the rendered switch output (which
// `newSwitchResult` shapes as `{"selected_case": <case value or "default">}`)
// and `originalToRenamedID` is the rename map produced when the parent
// hydrated the switch's child workflow.
//
// Scoped to the modern `cases[].next` / `default_next` shape. Legacy
// `cases[].tasks` array branches are out of scope (their multi-task DAGs
// would need transitive marking).
func propagateSwitchBranchState(
	switchTask model.Task,
	childOutput any,
	originalToRenamedID map[string]string,
	childCtx, parentCtx *TemplateContext,
	switchBranchDone map[string]bool,
) {
	selectedCase := ""
	if om, ok := childOutput.(map[string]any); ok {
		selectedCase, _ = om["selected_case"].(string)
	}

	resolve := func(branchID, caseValue string) {
		if branchID == "" {
			return
		}
		if caseValue == selectedCase {
			if val, ok := childCtx.Tasks[originalToRenamedID[branchID]]; ok {
				parentCtx.Tasks[branchID] = val
			}
		} else {
			parentCtx.Tasks[branchID] = map[string]any{
				"status": string(model.TaskStatusSkipped),
			}
		}
		switchBranchDone[branchID] = true
	}

	if cases, ok := switchTask.Params["cases"].([]any); ok {
		for _, c := range cases {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			next, _ := cm["next"].(string)
			caseValue := fmt.Sprintf("%v", cm["value"])
			resolve(next, caseValue)
		}
	}
	if defaultNext, _ := switchTask.Params["default_next"].(string); defaultNext != "" {
		resolve(defaultNext, "default")
	}
}
