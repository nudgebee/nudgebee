package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildEmptyOriginRepo creates a repository with no commits — the state GitHub
// leaves a repo in until its first push.
func buildEmptyOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	return dir
}

// TestCloneOrReuseRepository_EmptyRemote_PlainError pins the diagnosis for a repo
// with no commits. `git clone --bare` of an empty repo exits 0 with an unborn
// HEAD, so the failure used to surface from `git worktree add` as git's --orphan
// advice plus "fatal: invalid reference: HEAD" — which reads like an agent bug
// and steered runs into re-cloning by hand for the rest of their budget.
func TestCloneOrReuseRepository_EmptyRemote_PlainError(t *testing.T) {
	origin := buildEmptyOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)

	_, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "", filepath.Join(t.TempDir(), "wt"))
	if err == nil {
		t.Fatal("expected an error cloning a repository with no commits")
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Fatalf("error does not name the cause:\n%v", err)
	}
	if strings.Contains(err.Error(), "--orphan") {
		t.Fatalf("git's unborn-branch advice leaked into the error:\n%v", err)
	}
}

// TestCloneOrReuseRepository_EmptyBaseSelfHeals is the recovery case. A base
// cloned while the remote was still empty can never be repaired by the reuse
// path — `git clone --bare` configures no fetch refspec, so `git fetch origin`
// only writes FETCH_HEAD and refs/heads/* stays empty. In production that
// poisoned the workspace pod: every later analysis of that repo failed on
// `worktree add`, including after the repo received its first commit.
func TestCloneOrReuseRepository_EmptyBaseSelfHeals(t *testing.T) {
	origin := buildEmptyOriginRepo(t)
	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	// First analysis: repo is empty, and the empty base is left cached.
	if _, err := gc.CloneOrReuseRepository(ctx, origin, nil, "", filepath.Join(t.TempDir(), "wt1")); err == nil {
		t.Fatal("expected the first clone to fail while the remote had no commits")
	}

	// The repo gets its first commit.
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "add", ".")
	gitRun(t, origin, "commit", "-qm", "first")

	// Second analysis must recover on its own, not keep serving the stale base.
	res, err := gc.CloneOrReuseRepository(ctx, origin, nil, "", filepath.Join(t.TempDir(), "wt2"))
	if err != nil {
		t.Fatalf("cached empty base did not self-heal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(res.LocalPath, "f.txt"))
	if err != nil {
		t.Fatalf("worktree does not carry the new commit's content: %v", err)
	}
	if strings.TrimSpace(string(data)) != "hello" {
		t.Fatalf("unexpected worktree content: %q", data)
	}
}
