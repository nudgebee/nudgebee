package tools

import (
	"context"
	"strings"
	"testing"
)

func TestIsEnvUnavailable(t *testing.T) {
	cases := map[string]bool{
		`exec: "atlas": executable file not found in $PATH`: true,
		"sh: docker: not found":                             true,
		"bash: kubectl: command not found":                  true,
		"Command blocked for security reasons":              true,
		"go build failed: syntax error":                     false,
		"exit status 2":                                     false,
		// Application-level "not found" messages are real command errors, not
		// environment gaps — they must NOT trigger the pivot.
		"pq: database \"foo\": not found": false,
		"error: user alice: not found":    false,
		"zsh: terraform: not found":       true,
	}
	for input, want := range cases {
		if got := isEnvUnavailable(input); got != want {
			t.Errorf("isEnvUnavailable(%q) = %v, want %v", input, got, want)
		}
	}
}

// Environment-capability failures must carry the pivot hint so the model stops
// burning its failure budget on variants of the same unavailable tooling
// (observed live: atlas → docker → install, all absent from the workspace pod).
func TestEnvFailuresCarryPivotHint(t *testing.T) {
	tool := NewCLITool(t.TempDir())

	// Missing binary → hint present.
	resp := tool.Execute(context.Background(), map[string]any{
		"command": "definitely-not-a-real-binary-xyz --version",
	})
	if resp.Status != "error" {
		t.Fatalf("missing binary must fail, got %q", resp.Status)
	}
	if !strings.Contains(resp.Observation, "[environment]") {
		t.Errorf("missing-binary failure must carry the pivot hint, got: %q", resp.Observation)
	}

	// Blocked command → hint present.
	resp = tool.Execute(context.Background(), map[string]any{
		"command": "apt-get install atlas",
	})
	if !strings.Contains(resp.Observation, "[environment]") {
		t.Errorf("blocked command must carry the pivot hint, got: %q", resp.Observation)
	}

	// Ordinary command failure → NO hint (a real error deserves a real retry/fix).
	resp = tool.Execute(context.Background(), map[string]any{
		"command": "ls /definitely/not/a/path",
	})
	if resp.Status != "error" {
		t.Fatalf("expected failure, got %q", resp.Status)
	}
	if strings.Contains(resp.Observation, "[environment]") && !strings.Contains(resp.Observation, "not found") {
		t.Errorf("ordinary failures must not carry the pivot hint: %q", resp.Observation)
	}
}
