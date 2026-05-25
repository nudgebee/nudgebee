package agents

import "testing"

// instructionsRequireWrite gates the orchestrator's "file must exist before
// running CodeFixer" precondition. It must say true if and only if at least
// one implementation_instruction uses action="write", because that's the
// only legitimate way to bypass the file-exists check without re-opening
// the hallucinated-path failure mode the gate was originally designed to
// catch.
func TestInstructionsRequireWrite_NoInstructions(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"implementation_instructions": nil},
		{"implementation_instructions": []any{}},
		{"implementation_instructions": "not a list"}, // malformed
	}
	for i, c := range cases {
		if instructionsRequireWrite(c) {
			t.Errorf("case %d: expected false for %+v", i, c)
		}
	}
}

func TestInstructionsRequireWrite_OnlyReplace(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			map[string]any{"action": "replace", "file_path": "a.go"},
			map[string]any{"action": "verify", "file_path": "b.go"},
		},
	}
	if instructionsRequireWrite(facts) {
		t.Error("replace+verify should not bypass the file-exists check")
	}
}

func TestInstructionsRequireWrite_AnyWriteWins(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			map[string]any{"action": "replace", "file_path": "existing.go"},
			map[string]any{"action": "write", "file_path": "new.yaml"},
		},
	}
	if !instructionsRequireWrite(facts) {
		t.Error("a single action=write should trigger the bypass")
	}
}

func TestInstructionsRequireWrite_TolerantToBadEntries(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			"not a map",
			map[string]any{"step": 1},    // missing action
			map[string]any{"action": 42}, // non-string action
			map[string]any{"action": "write"},
		},
	}
	if !instructionsRequireWrite(facts) {
		t.Error("malformed siblings should not mask a real write action")
	}
}
