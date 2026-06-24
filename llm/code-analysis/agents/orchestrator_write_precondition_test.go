package agents

import (
	"path/filepath"
	"testing"
)

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

// repoRelativeFilePath strips a single leading "<repoName>/" segment so the
// orchestrator's file-exists gate joins ripgrep-relative paths against the repo
// root without doubling the segment. A doubled path made os.Stat report the file
// missing, silently skipping CodeFixer and forcing an expensive re-invocation.
func TestRepoRelativeFilePath(t *testing.T) {
	repo := filepath.Join("tmp", "code-analysis-abc", "nudgebee-enterprise")
	cases := []struct {
		name     string
		workDir  string
		filePath string
		want     string
	}{
		{
			name:     "strips matching repo-name prefix (the bug)",
			workDir:  repo,
			filePath: "nudgebee-enterprise/api-server/services/relay/service.go",
			want:     "api-server/services/relay/service.go",
		},
		{
			name:     "already repo-relative is unchanged",
			workDir:  repo,
			filePath: "api-server/services/relay/service.go",
			want:     "api-server/services/relay/service.go",
		},
		{
			name:     "only the leading occurrence is stripped",
			workDir:  repo,
			filePath: "nudgebee-enterprise/x/nudgebee-enterprise/y.go",
			want:     "x/nudgebee-enterprise/y.go",
		},
		{
			name:     "non-matching prefix is left intact",
			workDir:  repo,
			filePath: "some-other-repo/api-server/main.go",
			want:     "some-other-repo/api-server/main.go",
		},
		{
			name:     "a directory merely sharing the repo-name prefix is not stripped",
			workDir:  repo,
			filePath: "nudgebee-enterprise-extras/main.go",
			want:     "nudgebee-enterprise-extras/main.go",
		},
		{
			name:     "empty workDir returns input unchanged",
			workDir:  "",
			filePath: "nudgebee-enterprise/api-server/main.go",
			want:     "nudgebee-enterprise/api-server/main.go",
		},
		{
			name:     "empty filePath returns empty",
			workDir:  repo,
			filePath: "",
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := repoRelativeFilePath(c.workDir, c.filePath); got != c.want {
				t.Errorf("repoRelativeFilePath(%q, %q) = %q, want %q", c.workDir, c.filePath, got, c.want)
			}
		})
	}
}
