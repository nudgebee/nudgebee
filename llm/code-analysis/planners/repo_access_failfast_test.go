package planners

import (
	"os"
	"path/filepath"
	"testing"
)

// clonedRepoContext returns a RepositoryContext whose LocalPath is a real
// directory containing .git — what isRepositoryActuallyCloned checks for.
func clonedRepoContext(t *testing.T) *RepositoryContext {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return &RepositoryContext{URL: "https://example.com/o/r.git", LocalPath: dir}
}

// TestRepoStillUnavailable is the regression test for the 2026-08 empty-repository
// runs: repo_clone failed with git's unborn-HEAD advice ("invalid reference: HEAD"),
// which matched none of the error strings the old classifier knew, so the run kept
// re-cloning, fell through to `cli git clone`, and answered from no code at all.
//
// The gate is state-based on purpose — these cases assert that the ERROR TEXT never
// decides the outcome, so failure modes nobody has seen yet are covered too.
func TestRepoStillUnavailable(t *testing.T) {
	withRepo := &RepositoryContext{URL: "https://example.com/o/r.git"}

	cases := []struct {
		name   string
		action string
		rc     *RepositoryContext
		cloned bool
		want   bool
	}{
		{"empty repo — unborn HEAD, unknown to any error list", "repo_clone", withRepo, false, true},
		{"repository not found", "repo_clone", withRepo, false, true},
		{"clone timeout with no working tree", "repo_clone", withRepo, false, true},
		{"novel git failure nobody has classified", "cli", withRepo, false, true},
		{"gh failure before any clone", "gh", withRepo, false, true},
		// Non-repo tools never trip the gate, whatever they print.
		{"ripgrep failure", "rg", withRepo, false, false},
		{"file_view failure", "file_view", withRepo, false, false},
		// Once a working tree exists these are ordinary tool failures and belong
		// to the per-tool circuit breaker, not to the repo-availability gate.
		{"git failure after a successful clone", "git", withRepo, true, false},
		{"cli failure after a successful clone", "cli", withRepo, true, false},
		// A log-only run never expected a repository.
		{"no repository context", "repo_clone", nil, false, false},
		{"repository context without a URL", "cli", &RepositoryContext{}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &ReActPlanner{repositoryContext: c.rc, repoCloneSucceeded: c.cloned}
			if got := p.repoStillUnavailable(c.action); got != c.want {
				t.Errorf("repoStillUnavailable(%q) = %v, want %v", c.action, got, c.want)
			}
		})
	}
}

// TestRepoStillUnavailable_LocalPathCountsAsAvailable covers LOCAL_REPO_PATH runs:
// the repo is on disk and no clone step ever runs, so a failing git/cli step must
// not be read as "no repository".
func TestRepoStillUnavailable_LocalPathCountsAsAvailable(t *testing.T) {
	p := &ReActPlanner{repositoryContext: clonedRepoContext(t)}
	if p.repoStillUnavailable("git") {
		t.Error("a run whose LocalPath holds a real checkout must not be treated as repo-unavailable")
	}
}
