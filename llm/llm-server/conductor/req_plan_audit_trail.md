# Requirement: ReWOO Plan Audit Trail (Append-Only Plan History)

**Status:** Draft
**Component:** `agents/core/planner_rewoo_2.go`, conversation storage
**Priority:** Medium

---

## Problem Statement

When the ReWOO planner issues a `plan_update` (via `!update_v1`, `!update_v2`, etc.), the current implementation deletes pending/running nodes from the execution graph and replaces them with corrected steps. The original, bad steps are gone from the stored plan.

**Concrete example from production:**
1. Planner generated steps using tools `gcp_cloud_monitoring` and `gcp_cloud_logging` (non-existent).
2. Parallel execution started — both steps failed immediately ("tool not found").
3. Planner issued `!update_v1` with corrected steps using the actual `gcp` tool.
4. The bad steps were deleted from `executionGraph`; only the corrected plan is stored.

**Result:** Any engineer looking at the stored conversation record sees only the final corrected plan. The original bad plan, the tool failures that triggered the correction, and the reviewer's reasoning are invisible.

This creates three concrete problems:

1. **Debugging is blind.** When investigating a slow or failed agent run, there is no record of what the original plan was, which tools were attempted, or why the plan was changed. The `plan_update` meta-step records the reviewer's `Thought` text, but the discarded steps themselves are gone.

2. **Plan quality metrics are wrong.** If we measure first-attempt tool accuracy or step success rates, the deleted steps are excluded. The planner looks better than it is.

3. **Retry cost is invisible.** The failed tool executions from the bad initial plan consumed latency and LLM tokens. Without an audit trail, we cannot quantify this waste or detect regressions in plan quality.

---

## Desired Behavior

Plan history must be **append-only**. Steps that are superseded by a `plan_update` should never be deleted. Instead, they should be marked with a terminal status (`superseded`) and preserved in the stored plan with their original iteration number.

When a `plan_update` fires:

- All pending/running steps from the current iteration that are being replaced get status `superseded` (not deleted).
- The `!update_vN` meta-step is added as today (no change needed here).
- New replacement steps are added with an incremented iteration number.
- `updateStoredPlan()` writes the full graph — including superseded nodes — to the database.

The final stored record should read as a complete timeline: initial plan → failure steps → plan update event → corrected steps → final outcomes.

---

## Key Requirements

### R1 — New step status: `superseded`

Add `StepStatusSuperseded StepStatus = "superseded"` alongside the existing statuses (`pending`, `running`, `completed`, `failed`, `skipped`, `waiting`).

### R2 — No deletion on plan update

In `reviewAndRefinePlan()`, replace the block that calls `delete(o.executionGraph, id)` on pending/running nodes with a loop that sets their status to `StepStatusSuperseded`.

```
// Before: delete(o.executionGraph, id)
// After:  node.Status = StepStatusSuperseded
```

All other logic (adding `!update_vN` meta-step, adding new steps, calling `updateStoredPlan()`) stays the same.

### R3 — Superseded steps must carry their terminal output

If a step was running and has partial output (e.g., a "tool not found" error string already in `node.Output`), that output must be preserved when the status is set to `superseded`. No data truncation.

### R4 — Storage writes the full graph

`updateStoredPlan()` already serializes the entire `executionGraph`. No change needed here as long as R2 is implemented — superseded nodes will naturally be included because they are not deleted.

The serialized JSON for a superseded node should include `"status": "superseded"` and `"iteration": <original_iteration>` so consumers can distinguish it from current-iteration nodes.

### R5 — Dependency resolution must ignore superseded steps

In the execution scheduler (lines ~709, ~1019), where the code checks whether a dependency `failed` or `skipped` in a previous iteration, it must also treat `superseded` as a terminal non-blocking state. A step in the new iteration whose dependency ID was superseded should not be blocked.

### R6 — UI / downstream consumers

The `status: "superseded"` value must be documented so the frontend and any downstream consumers know to render it distinctly (e.g., greyed-out with a strikethrough, grouped under a "Discarded steps" section). This is a contract change in the stored plan JSON.

---

## What to Store (Summary)

For each superseded node:

| Field | Value |
|---|---|
| `id` | Original step ID (e.g., `E2`) |
| `tool` | Original tool name |
| `query` | Original query |
| `reason` | Original reason/plan text |
| `status` | `"superseded"` |
| `iteration` | Iteration in which the step was originally created |
| `output` | Error or partial output from the failed execution (if any) |

No new database schema changes are required — the plan is stored as a JSON blob in the existing `SaveConversationAgentCall` / `UpdateConversationAgentResponse` path.

---

## Out of Scope

- Replaying or re-executing superseded steps.
- Separate database table for plan history; the existing JSON blob storage is sufficient for v1.
- Changes to the ReAct planner (`planner_react.go`).
- Changes to `planCritiquer` / pre-execution critique flow.
- Surfacing audit data in analytics pipelines (follow-on work).

---

## Open Questions

1. **Graph size growth.** In pathological cases (many plan updates), the execution graph grows unbounded within a single run. Is there a maximum number of `plan_update` iterations enforced today (`maxRefinementAttempts`)? Confirm this also bounds the number of superseded nodes.

2. **UI grouping.** Should the frontend collapse superseded steps by default, or show them inline in iteration order? UX decision needed before the frontend implements R6.

3. **Metrics backfill.** Existing stored plans have the deleted steps gone. Do we want to add a warning/annotation to historical records that pre-date this change, or just accept the gap?

4. **Step ID collisions.** New iteration steps can reuse the same IDs as superseded steps (e.g., a corrected `E2` replacing a superseded `E2`). The current code already handles this with a check on `StepStatusCompleted`. After this change, the check must also allow overwriting a `superseded` node with a fresh pending node of the same ID, or we need to use a different ID scheme for replacement steps (e.g., `E2_v2`). Clarify which approach is preferred.
