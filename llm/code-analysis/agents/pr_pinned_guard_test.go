package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/planners"
)

// A request-pinned revision must never reach PR creation. This is the cheap,
// deterministic half of the guard — it needs no repo on disk and no remote ref,
// which is why it exists separately from the branch-tip comparison.
func TestRefusePRFromPinnedRevision_RequestPinned(t *testing.T) {
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{
		RepoContext: &planners.RepositoryContext{
			Branch: "main",
			Commit: "d16bfe05a744909de4b27f5875fe0d4ed41ce607",
		},
	}

	err := a.refusePRFromPinnedRevision(context.Background(), sc, t.TempDir(), "main", "abc123")
	if err == nil {
		t.Fatal("expected a refusal when the request pinned a commit, got nil")
	}
	// The message has to name the commit and the way out, or the caller cannot
	// act on it.
	for _, want := range []string{"d16bfe05", "raise_pr=false", "main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

// The ordinary path must stay open: no pinned commit, and a base ref that does
// not resolve locally (the normal case for these single-branch clones) is not
// evidence of staleness.
func TestRefusePRFromPinnedRevision_UnpinnedProceeds(t *testing.T) {
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{
		RepoContext: &planners.RepositoryContext{Branch: "main"},
	}

	if err := a.refusePRFromPinnedRevision(context.Background(), sc, t.TempDir(), "main", "abc123"); err != nil {
		t.Fatalf("unpinned analysis should be allowed to open a PR, got: %v", err)
	}
}

// A nil session must not panic on the PR path.
func TestRefusePRFromPinnedRevision_NilSession(t *testing.T) {
	a := &OrchestratorAgent{}
	if err := a.refusePRFromPinnedRevision(context.Background(), nil, t.TempDir(), "main", ""); err != nil {
		t.Fatalf("nil session should not refuse, got: %v", err)
	}
}

func guardGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func guardCommit(t *testing.T, dir, file, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	guardGit(t, dir, "add", ".")
	guardGit(t, dir, "commit", "-qm", message)
	return guardGit(t, dir, "rev-parse", "HEAD")
}

// guardRepo returns a clone whose origin/main is two commits long, and the
// SHAs of main's first commit, its tip, and a commit on a branch that was never
// merged into main.
func guardRepo(t *testing.T) (clone, first, tip, offMain string) {
	t.Helper()
	origin := t.TempDir()
	guardGit(t, origin, "init", "-q", "-b", "main")
	first = guardCommit(t, origin, "f.txt", "one\n", "one")
	guardGit(t, origin, "checkout", "-q", "-b", "release")
	offMain = guardCommit(t, origin, "g.txt", "release only\n", "release only")
	guardGit(t, origin, "checkout", "-q", "main")
	tip = guardCommit(t, origin, "f.txt", "two\n", "two")

	clone = filepath.Join(t.TempDir(), "clone")
	guardGit(t, t.TempDir(), "clone", "-q", origin, clone)
	return clone, first, tip, offMain
}

// The base branch gaining a commit while the run worked is not staleness. The
// fix branch is cut from a commit that is on the base branch, and such a branch
// merges like any other pull request. This used to be refused: origin/<base> is
// shared by every conversation on the repository and moves whenever one of them
// fetches.
func TestRefusePRFromPinnedRevision_BehindTheTipProceeds(t *testing.T) {
	clone, first, tip, _ := guardRepo(t)
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{RepoContext: &planners.RepositoryContext{Branch: "main"}}

	if err := a.refusePRFromPinnedRevision(context.Background(), sc, clone, "main", tip); err != nil {
		t.Fatalf("a branch point at the tip should proceed, got: %v", err)
	}
	if err := a.refusePRFromPinnedRevision(context.Background(), sc, clone, "main", first); err != nil {
		t.Fatalf("a branch point on main, behind its tip, should proceed, got: %v", err)
	}
}

// A commit that is not on the base branch still must not become a PR: the
// branch would carry everything that other branch holds.
func TestRefusePRFromPinnedRevision_OffTheBaseBranchRefused(t *testing.T) {
	clone, _, tip, offMain := guardRepo(t)
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{RepoContext: &planners.RepositoryContext{Branch: "main"}}

	err := a.refusePRFromPinnedRevision(context.Background(), sc, clone, "main", offMain)
	if err == nil {
		t.Fatal("expected a refusal for a branch point that is not on main, got nil")
	}
	for _, want := range []string{offMain, tip, "not on main", "raise_pr=false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}

	// A commit git cannot find at all is refused the same way, not waved through.
	if err := a.refusePRFromPinnedRevision(context.Background(), sc, clone, "main", "4233af136c6786943072cce32e2371cac4e50d15"); err == nil {
		t.Fatal("expected a refusal for a branch point git does not know, got nil")
	}
}
