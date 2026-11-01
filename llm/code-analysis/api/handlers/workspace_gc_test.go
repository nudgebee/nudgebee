package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/tools"
)

func TestExecWorkspaceBaseDir(t *testing.T) {
	tests := []struct {
		name        string
		executionWS string
		analysisWS  string
		want        string
	}{
		{"configured execution dir wins", "/data/exec", "/data/analysis", "/data/exec"},
		{"falls back under the analysis dir", "", "/data/analysis", "/data/analysis/exec_workspaces"},
		{"hard default", "", "", "/tmp/code-analysis/exec_workspaces"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Execution.WorkspaceDir = tt.executionWS
			cfg.Analysis.WorkspaceDir = tt.analysisWS
			if got := execWorkspaceBaseDir(cfg); got != tt.want {
				t.Errorf("execWorkspaceBaseDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRegisterWorkspaceLeftoversCollectsStaleLeftovers drives the
// same calls cmd/main.go makes: after registration, the periodic collection
// removes a command folder nobody has touched for a week, and a spill file and
// a run checkout past their age, and leaves the recently used ones.
func TestRegisterWorkspaceLeftoversCollectsStaleLeftovers(t *testing.T) {
	// spill files live under os.TempDir(); point it at a scratch directory so
	// the test never sweeps the real one.
	t.Setenv("TMPDIR", t.TempDir())

	cfg := &config.Config{}
	cfg.Analysis.WorkspaceDir = t.TempDir()
	cfg.Execution.WorkspaceDir = filepath.Join(t.TempDir(), "exec_workspaces")

	age := func(path string, d time.Duration) {
		t.Helper()
		when := time.Now().Add(-d)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("leftover"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	idleConv := filepath.Join(cfg.Execution.WorkspaceDir, "conv-idle")
	write(filepath.Join(idleConv, "logs_loki_1.txt"))
	age(filepath.Join(idleConv, "logs_loki_1.txt"), execWorkspaceMaxIdle+time.Hour)
	age(idleConv, execWorkspaceMaxIdle+time.Hour)

	// Two days idle: far past the spill-file age, but well inside the
	// command-folder one, so a conversation resumed after a weekend still has
	// its files.
	recentConv := filepath.Join(cfg.Execution.WorkspaceDir, "conv-recent")
	write(filepath.Join(recentConv, "logs_loki_2.txt"))
	age(filepath.Join(recentConv, "logs_loki_2.txt"), 48*time.Hour)
	age(recentConv, 48*time.Hour)

	oldSpill := filepath.Join(tools.ToolOutputSpillDir(), "cli-old.log")
	write(oldSpill)
	age(oldSpill, 3*time.Hour)
	newSpill := filepath.Join(tools.ToolOutputSpillDir(), "cli-new.log")
	write(newSpill)

	// Run checkouts in the temp directory: an abandoned one, one still in use,
	// and an old folder that is not a checkout at all.
	abandonedRun := filepath.Join(os.TempDir(), "code-analysis-conv-abandoned")
	write(filepath.Join(abandonedRun, "repo", "main.go"))
	for _, p := range []string{filepath.Join(abandonedRun, "repo", "main.go"), filepath.Join(abandonedRun, "repo"), abandonedRun} {
		age(p, 3*time.Hour)
	}
	activeRun := filepath.Join(os.TempDir(), "code-analysis-conv-active")
	write(filepath.Join(activeRun, "repo", "main.go"))
	age(activeRun, 3*time.Hour) // top folder looks old; the file inside is new
	unrelated := filepath.Join(os.TempDir(), "not-a-checkout")
	write(filepath.Join(unrelated, "file"))
	age(filepath.Join(unrelated, "file"), 3*time.Hour)
	age(unrelated, 3*time.Hour)

	RegisterWorkspaceLeftovers(cfg, 2*time.Hour)
	if _, err := CollectWorkspaceGarbage(cfg.Analysis.WorkspaceDir); err != nil {
		t.Fatalf("CollectWorkspaceGarbage: %v", err)
	}

	for _, gone := range []string{idleConv, oldSpill, abandonedRun} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still exists or returned unexpected error: %v", gone, err)
		}
	}
	for _, kept := range []string{
		filepath.Join(recentConv, "logs_loki_2.txt"),
		newSpill,
		filepath.Join(activeRun, "repo", "main.go"),
		filepath.Join(unrelated, "file"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}
