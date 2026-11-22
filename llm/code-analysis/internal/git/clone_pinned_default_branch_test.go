package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A commit-pinned clone leaves HEAD detached at that commit. An agent asking
// "is this already fixed on the default branch?" naturally reaches for
// `git diff <commit> HEAD` — which compares the commit with itself and returns
// nothing, reading as "unchanged" when nothing was compared. That is exactly
// how an event investigation concluded a retry was missing while the default
// branch had carried it for three weeks.
//
// The default branch must therefore be resolvable under its remote-tracking
// name in a pinned checkout.
func TestCloneAtCommit_DefaultBranchIsComparable(t *testing.T) {
	origin := buildOriginRepo(t) // default branch "test": BASE -> TEST
	baseSHA := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))
	tipSHA := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test"))
	if baseSHA == "" || baseSHA == tipSHA {
		t.Fatalf("fixture produced no distinct ancestor (base=%q tip=%q)", baseSHA, tipSHA)
	}

	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	// No branch: the caller only knows the deployed commit, which is the shape
	// the event investigation uses.
	res, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "", baseSHA, worktree)
	if err != nil {
		t.Fatalf("clone at commit: %v", err)
	}
	if res.CommitHash != baseSHA {
		t.Fatalf("checked out %s, want pinned %s", res.CommitHash, baseSHA)
	}
	if res.DefaultBranch != "test" {
		t.Fatalf("DefaultBranch = %q, want %q", res.DefaultBranch, "test")
	}

	// The trap, pinned as behaviour so it cannot be mistaken for a fix.
	if d := gitRun(t, res.LocalPath, "diff", baseSHA, "HEAD"); strings.TrimSpace(d) != "" {
		t.Fatalf("diff against HEAD should be empty for a pinned checkout, got:\n%s", d)
	}

	// The fix: the default branch is reachable, so the real comparison works.
	against := "origin/" + res.DefaultBranch
	diff := gitRun(t, res.LocalPath, "diff", baseSHA, against)
	if strings.TrimSpace(diff) == "" {
		t.Fatalf("diff %s..%s is empty; the default branch is not fetched in a pinned checkout", baseSHA, against)
	}
	if !strings.Contains(diff, "TEST") {
		t.Fatalf("diff does not show the default-branch change:\n%s", diff)
	}
}

// A bare clone is cached and reused across runs. Nothing a request asks for is
// written into its config: a stored branch list outlives the branches on it, and
// a fetch that walks a list naming a branch the remote has deleted fails as a
// whole. Each fetch names its branch instead, so reuse leaves no list behind.
func TestCloneAtCommit_KeepsNoBranchListInTheClone(t *testing.T) {
	origin := buildOriginRepo(t)
	baseSHA := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)

	for i := 0; i < 3; i++ {
		if _, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "", baseSHA,
			filepath.Join(t.TempDir(), "wt")); err != nil {
			t.Fatalf("pinned clone %d: %v", i, err)
		}
	}
	// A branch under analysis together with the base it would merge into.
	if _, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "prod",
		filepath.Join(t.TempDir(), "wt"), "test"); err != nil {
		t.Fatalf("clone prod: %v", err)
	}

	repoDir := filepath.Join(workspace, "repos", repoKeyFromURL(origin))
	if stored := storedRefspecs(repoDir); stored != "" {
		t.Fatalf("the clone's config names branches after reuse:\n%s", stored)
	}
}
