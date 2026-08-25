package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// isNoProgressStep is the unified "did this call move the investigation
// forward?" primitive. Uses only signals the executor already computes
// (status + trivial-observation) — no per-error-class parsing, no regex
// over specific denial / rate-limit / 404 shapes.
func TestIsNoProgressStep(t *testing.T) {
	step := func(status string, obs string) NBAgentPlannerToolActionStep {
		return NBAgentPlannerToolActionStep{Status: ToolStatus(status), Observation: obs}
	}

	tests := []struct {
		name string
		step NBAgentPlannerToolActionStep
		want bool
	}{
		// Explicit executor-tagged states
		{"status=failure", step(string(ToolStatusFailure), "any body — error text"), true},
		{"status=empty result", step(string(ToolStatusEmptyResult), `{"data":null}`), true},

		// Trivial observations (backup for callers that didn't tag EmptyResult)
		{"success + bare []", step(string(ToolStatusSuccess), "[]"), true},
		{"success + bare {}", step(string(ToolStatusSuccess), "{}"), true},
		{"success + bare null", step(string(ToolStatusSuccess), "null"), true},
		{"success + no-data sentinel", step(string(ToolStatusSuccess), plannerToolNoData), true},
		{"success + empty string", step(string(ToolStatusSuccess), ""), true},
		{"success + whitespace-wrapped []", step(string(ToolStatusSuccess), "  []\n"), true},

		// Success + non-trivial content = progress
		{"success + real content", step(string(ToolStatusSuccess), `{"instances":[{"id":"i-abc"}]}`), false},
		{"success + wrapped empty is NOT trivial", step(string(ToolStatusSuccess), `{"billingAccounts":[]}`), false},
		{"success + error-shaped text in body", step(string(ToolStatusSuccess), "PERMISSION_DENIED via body — but status was success"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNoProgressStep(tt.step))
		})
	}
}

// countConsecutiveNoProgressForTool walks steps back-to-front counting
// consecutive same-tool no-progress outcomes. Other tools' steps are skipped
// (don't reset). Same-tool progress step ends the chain.
func TestCountConsecutiveNoProgressForTool(t *testing.T) {
	step := func(tool, input string, status ToolStatus, obs string) NBAgentPlannerToolActionStep {
		return NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: tool, ToolInput: input},
			Status:      status,
			Observation: obs,
		}
	}

	tests := []struct {
		name    string
		steps   []NBAgentPlannerToolActionStep
		action  NBAgentPlannerToolAction
		current NBAgentPlannerToolActionStep
		want    int
	}{
		{
			name:    "no prior + current progresses = 0",
			action:  NBAgentPlannerToolAction{Tool: "gcloud_execute", ToolInput: "a"},
			current: step("gcloud_execute", "a", ToolStatusSuccess, `{"result":"ok"}`),
			want:    0,
		},
		{
			name:    "no prior + current failure = 1",
			action:  NBAgentPlannerToolAction{Tool: "gcloud_execute", ToolInput: "a"},
			current: step("gcloud_execute", "a", ToolStatusFailure, "PERMISSION_DENIED"),
			want:    1,
		},
		{
			name: "consecutive failures across projects, other tools ignored",
			steps: []NBAgentPlannerToolActionStep{
				step("gcloud_execute", "project A", ToolStatusFailure, "PERMISSION_DENIED on A"),
				step("shell_execute", "unrelated", ToolStatusSuccess, "diagnostic output"),
				step("gcloud_execute", "project B", ToolStatusFailure, "PERMISSION_DENIED on B"),
			},
			action:  NBAgentPlannerToolAction{Tool: "gcloud_execute", ToolInput: "project C"},
			current: step("gcloud_execute", "project C", ToolStatusFailure, "PERMISSION_DENIED on C"),
			want:    3,
		},
		{
			name: "same-tool success ENDS the chain",
			steps: []NBAgentPlannerToolActionStep{
				step("gcloud_execute", "a", ToolStatusFailure, "denied a"),
				step("gcloud_execute", "b", ToolStatusSuccess, `{"clusters":["prod"]}`), // ← breaks chain
				step("gcloud_execute", "c", ToolStatusFailure, "denied c"),
			},
			action:  NBAgentPlannerToolAction{Tool: "gcloud_execute", ToolInput: "d"},
			current: step("gcloud_execute", "d", ToolStatusFailure, "denied d"),
			want:    2, // current + one prior denied c, then chain broken by b's success
		},
		{
			name: "mixed shapes all count as no-progress — 403 + 503 + [] + empty",
			steps: []NBAgentPlannerToolActionStep{
				step("gcloud_execute", "a", ToolStatusFailure, "PERMISSION_DENIED"),
				step("gcloud_execute", "b", ToolStatusFailure, "503 Service Unavailable"),
				step("gcloud_execute", "c", ToolStatusSuccess, "[]"),
				step("gcloud_execute", "d", ToolStatusEmptyResult, `{"data":null}`),
			},
			action:  NBAgentPlannerToolAction{Tool: "gcloud_execute", ToolInput: "e"},
			current: step("gcloud_execute", "e", ToolStatusFailure, "429 Too Many Requests"),
			want:    5, // all 4 prior + current
		},
		{
			name: "byte-identical non-trivial observations count (DBInstanceIdentifier subclass)",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "jmespath variant", ToolStatusSuccess, "null\tavailable\tpg\n"),
				step("aws_execute", "jq variant", ToolStatusSuccess, "null\tavailable\tpg\n"),
			},
			action:  NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "output-text variant"},
			current: step("aws_execute", "output-text variant", ToolStatusSuccess, "null\tavailable\tpg\n"),
			want:    3, // all three succeed but produce byte-identical non-trivial output
		},
		{
			name: "byte-identical only counts against DIFFERENT inputs (turn cache's job otherwise)",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "a", ToolStatusSuccess, "same output"),
			},
			action:  NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "a"},
			current: step("aws_execute", "a", ToolStatusSuccess, "same output"),
			want:    0, // same input — turn cache handles this, not us
		},
		{
			// Regression guard: without the onlyMatchObservation flag we'd walk past
			// the successful step at `b` and absorb the older 503 failure into the
			// count, inflating it to 3 and firing the loop-breaker on what is really
			// just the second repeat of a good answer.
			name: "prior failure before real progress does NOT get retroactively counted on duplicate success",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "a", ToolStatusFailure, "503 error"),
				step("aws_execute", "b", ToolStatusSuccess, "success output"),
			},
			action:  NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "c"},
			current: step("aws_execute", "c", ToolStatusSuccess, "success output"),
			want:    2, // current + b (duplicate). a's failure predates b's progress — excluded.
		},
		{
			// Trivial empty observations shouldn't trigger byte-identical matching:
			// they're already handled by isNoProgressStep in the "trivial observation"
			// branch, and matching on them here would over-fire on unrelated tools
			// that legitimately return `[]` / `{}` / bare `null`.
			name: "trivial empty observations are not matched byte-identically",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "a", ToolStatusSuccess, "[]"),
			},
			action:  NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			current: step("aws_execute", "b", ToolStatusSuccess, "[]"),
			// current is a no-progress step (trivial obs), and prior a is too — so both count
			// via the isNoProgressStep branch, not the byte-identical branch.
			want: 2,
		},
		{
			// Multi-line nulls are NOT trivial (isTrivialObservation only matches
			// single-token empties) — they should still get flagged as byte-identical
			// repeats. Guards against a would-be "trim & bulk-classify as trivial"
			// regression that would let these slip past the loop-breaker.
			name: "multi-line nulls are still flagged as byte-identical repeats",
			steps: []NBAgentPlannerToolActionStep{
				step("aws_execute", "a", ToolStatusSuccess, "null\nnull\nnull\n"),
			},
			action:  NBAgentPlannerToolAction{Tool: "aws_execute", ToolInput: "b"},
			current: step("aws_execute", "b", ToolStatusSuccess, "null\nnull\nnull\n"),
			want:    2, // current + a via byte-identical match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &plannerExecutor{steps: tt.steps}
			assert.Equal(t, tt.want, e.countConsecutiveNoProgressForTool(tt.action, tt.current))
		})
	}
}

// noProgressNotice must name (a) the count so the model reads repetition as
// signal not noise, (b) at least one concrete recovery path, (c) the tool
// name so the model can attribute — and (d) since it replaces THREE prior
// specific notices, its recovery guidance must cover the common failure
// causes at a general level (typo / IAM / no matches / transient / just
// stuck) without needing per-class parsing.
func TestNoProgressNotice_ContainsRequiredSignals(t *testing.T) {
	n := noProgressNotice("gcloud_execute", 4)

	assert.Contains(t, n, "4", "occurrence count must be stated")
	assert.Contains(t, n, "gcloud_execute", "tool name must be in the notice for attribution")
	assert.Contains(t, n, "STOP", "must be a stop directive, not advisory prose")

	// Consolidated notice covers three prior causes at a general level:
	assert.Contains(t, n, "typo", "must mention the arg-typo class (was the byte-identical case)")
	assert.Contains(t, n, "permission", "must mention the IAM class (was the access-denied case)")
	assert.Contains(t, n, "no matches", "must name empty-as-valid so legit sweeps terminate correctly (was the trivial-result case)")

	// And must offer a non-retry recovery path:
	assert.Contains(t, n, "different", "must offer switching approach as a recovery path")
}
