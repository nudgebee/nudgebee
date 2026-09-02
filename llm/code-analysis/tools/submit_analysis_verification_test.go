package tools

import (
	"context"
	"testing"
)

// TestSubmitAnalysis_VerificationPassed_TriState verifies that verification_passed
// is only present in the result when the LLM actually reported an outcome. A plain
// bool defaulted to false for runs that never ran a build/lint check (e.g. a no-op
// where the requested change was already present), which the UI then rendered as a
// spurious "Verification failed" chip. The field must be tri-state: present only
// when explicitly supplied.
func TestSubmitAnalysis_VerificationPassed_TriState(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	ctx := context.Background()

	cases := []struct {
		name        string
		input       map[string]any
		wantPresent bool
		wantValue   bool
	}{
		{
			name: "omitted -> absent (no verification ran)",
			input: map[string]any{
				"execution_status":  "success",
				"execution_summary": "The requested fallback mechanism is already implemented.",
			},
			wantPresent: false,
		},
		{
			name: "explicit true -> present true",
			input: map[string]any{
				"execution_status":    "success",
				"execution_summary":   "Applied fix and verified build.",
				"verification_passed": true,
			},
			wantPresent: true,
			wantValue:   true,
		},
		{
			name: "explicit false -> present false (verification ran and failed)",
			input: map[string]any{
				"execution_status":    "failed",
				"execution_summary":   "Applied fix but build failed.",
				"verification_passed": false,
			},
			wantPresent: true,
			wantValue:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := tool.Execute(ctx, tc.input)
			if resp.Status == "error" {
				t.Fatalf("unexpected error response: %s", resp.Error)
			}
			data, ok := resp.Data.(map[string]any)
			if !ok {
				t.Fatalf("expected Data to be map[string]any, got %T", resp.Data)
			}
			got, present := data["verification_passed"]
			if present != tc.wantPresent {
				t.Fatalf("verification_passed present=%v, want %v (data=%v)", present, tc.wantPresent, data["verification_passed"])
			}
			if tc.wantPresent {
				b, ok := got.(bool)
				if !ok {
					t.Fatalf("verification_passed should be a bool in the result map, got %T", got)
				}
				if b != tc.wantValue {
					t.Errorf("verification_passed=%v, want %v", b, tc.wantValue)
				}
			}
		})
	}
}
