package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClassifyFollowupOutcome pins the contract between the PR-lifecycle cron
// and the followup agent's response shape. Three outcomes are distinguished:
//
//	success — committed/replied; reset iteration count, mark created
//	no_op   — nothing actionable yet, or planner produced no change;
//	          iteration count must stay put so the cron remains free to retry
//	          when reviewer feedback finally arrives
//	failed  — real failure; charge an iteration toward the cap
//
// Regression history: the original implementation read agent_resp["success"],
// a key the followup handler never set. Every real run was therefore booked
// as a failure. PRs reviewed by Gemini >2h after creation hit the iteration
// cap before the reviewer ever showed up; the row was retired and the
// reviewer's comments were never addressed. Reading execution_status (the
// field the handler actually emits) closes that gap.
func TestClassifyFollowupOutcome(t *testing.T) {
	tests := []struct {
		name           string
		responses      []string
		want           followupOutcome
		failureContext string
	}{
		{
			name:           "empty response means retry",
			responses:      nil,
			want:           followupOutcomeFailed,
			failureContext: "agent returned no response",
		},
		{
			name:           "empty slice means retry",
			responses:      []string{},
			want:           followupOutcomeFailed,
			failureContext: "agent returned empty response slice",
		},
		{
			name:           "non-JSON response means retry",
			responses:      []string{"not json"},
			want:           followupOutcomeFailed,
			failureContext: "agent payload was unparseable",
		},
		{
			name:           "JSON without status field means retry",
			responses:      []string{`{"description":"did some stuff"}`},
			want:           followupOutcomeFailed,
			failureContext: "no execution_status, no success — caller cannot tell what happened",
		},
		{
			name:           "execution_status=success means created with reset",
			responses:      []string{`{"execution_status":"success","execution_summary":"Committed and pushed: abc123"}`},
			want:           followupOutcomeSuccess,
			failureContext: "real success path the followup handler emits",
		},
		{
			name:           "execution_status=partial_success also resets",
			responses:      []string{`{"execution_status":"partial_success","execution_summary":"Replied to 2 of 3 comments"}`},
			want:           followupOutcomeSuccess,
			failureContext: "partial success still represents real work; don't penalize it",
		},
		{
			name:           "execution_status=no_op leaves counter alone",
			responses:      []string{`{"execution_status":"no_op","description":"no comments yet"}`},
			want:           followupOutcomeNoOp,
			failureContext: "no actionable signal — must not consume a retry slot",
		},
		{
			name:           "execution_status=failed counts toward cap",
			responses:      []string{`{"execution_status":"failed","failure_summary":"git push rejected"}`},
			want:           followupOutcomeFailed,
			failureContext: "real failure must charge an iteration",
		},
		{
			name:           "unknown execution_status falls back to failed",
			responses:      []string{`{"execution_status":"weird_new_value"}`},
			want:           followupOutcomeFailed,
			failureContext: "unrecognized status — safer to retry than silently mark created",
		},
		{
			name:           "legacy success=true still flips to created",
			responses:      []string{`{"success":true,"execution_summary":"committed"}`},
			want:           followupOutcomeSuccess,
			failureContext: "legacy producers without execution_status must keep working",
		},
		{
			name:           "legacy success=false stays as failed",
			responses:      []string{`{"success":false,"error":"thought_signature missing"}`},
			want:           followupOutcomeFailed,
			failureContext: "legacy explicit failure",
		},
		{
			name:           "execution_status wins over legacy success when both set",
			responses:      []string{`{"execution_status":"no_op","success":true}`},
			want:           followupOutcomeNoOp,
			failureContext: "modern field is authoritative; legacy is a fallback only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFollowupOutcome(tt.responses)
			assert.Equal(t, tt.want.name, got.name, "outcome name mismatch — %s", tt.failureContext)
			assert.Equal(t, tt.want.newState, got.newState, "newState mismatch — %s", tt.failureContext)
			assert.Equal(t, tt.want.counterDelta, got.counterDelta, "counterDelta mismatch — %s", tt.failureContext)
			assert.Equal(t, tt.want.resetCounter, got.resetCounter, "resetCounter mismatch — %s", tt.failureContext)
		})
	}
}
