package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNextLifecycleState pins down the bug-fix invariant: a successful
// followup must move state to "created" and any other outcome (no response,
// non-JSON, success=false, missing field, wrong type) must move it to
// "needs_followup" so the cron retries instead of marking the PR healthy.
//
// Regression: previously both branches set state to "created", so a Gemini
// 400 mid-followup was indistinguishable from a real fix landing — the row
// looked healthy and the iteration counter quietly burned the retry budget.
func TestNextLifecycleState(t *testing.T) {
	tests := []struct {
		name           string
		responses      []string
		wantState      string
		wantSuccess    bool
		failureContext string
	}{
		{
			name:           "empty response means retry",
			responses:      nil,
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "agent returned no response",
		},
		{
			name:           "empty slice means retry",
			responses:      []string{},
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "agent returned empty response slice",
		},
		{
			name:           "non-JSON response means retry",
			responses:      []string{"not json"},
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "agent payload was unparseable",
		},
		{
			name:           "JSON without success field means retry",
			responses:      []string{`{"description":"did some stuff"}`},
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "agent didn't claim success",
		},
		{
			name:           "success=false means retry",
			responses:      []string{`{"success":false,"error":"thought_signature missing"}`},
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "agent reported failure (e.g. Gemini 400 mid-iteration)",
		},
		{
			name:           "success not a bool means retry",
			responses:      []string{`{"success":"true"}`},
			wantState:      "needs_followup",
			wantSuccess:    false,
			failureContext: "type-mismatched success field shouldn't satisfy success",
		},
		{
			name:           "success=true means created",
			responses:      []string{`{"success":true,"execution_summary":"committed and pushed: abc123"}`},
			wantState:      "created",
			wantSuccess:    true,
			failureContext: "real success path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotSuccess := nextLifecycleState(tt.responses)
			assert.Equal(t, tt.wantState, gotState, "state mismatch — %s", tt.failureContext)
			assert.Equal(t, tt.wantSuccess, gotSuccess, "success mismatch — %s", tt.failureContext)
		})
	}
}
