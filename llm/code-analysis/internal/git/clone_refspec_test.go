package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRun runs a git command in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// buildOriginRepo creates a non-bare repo with `test` and `prod` branches whose
// edits to the same line conflict on merge, plus an unrelated `stale/exploration`
// branch. Returns its path.
func buildOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nBASE\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "base")
	gitRun(t, dir, "checkout", "-q", "-b", "prod")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nPROD\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-qam", "prod")
	gitRun(t, dir, "checkout", "-q", "-b", "stale/exploration")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nSTALE\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-qam", "stale")
	gitRun(t, dir, "checkout", "-q", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nTEST\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-qam", "test")
	return dir
}

// TestCloneOrReuseRepository_RemoteTrackingRefs is a regression test for the
// merge-conflict debug session: `git clone --bare` writes branches to refs/heads/*
// and configures no fetch refspec, so refs/remotes/origin/* stayed empty, the
// requested branch checkout fell back to HEAD, and `git merge origin/<base>` could
// not reproduce a PR merge conflict.
//
// The base branch is requested explicitly rather than by fetching everything — the
// bare clone is deliberately narrow so the agent's branch view does not fill with
// stale exploration branches whose names pull the LLM off-task.
func TestCloneOrReuseRepository_RemoteTrackingRefs(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	// "test" is the branch under analysis; "prod" is the base it would merge into.
	res, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "test", worktree, "prod")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// Both sides of the merge must be resolvable as remote-tracking refs.
	remotes := gitRun(t, res.LocalPath, "branch", "-r")
	for _, want := range []string{"origin/test", "origin/prod"} {
		if !strings.Contains(remotes, want) {
			t.Fatalf("%s not in remote-tracking refs; got:\n%s", want, remotes)
		}
	}

	// The requested branch must actually be checked out (not a silent HEAD fallback).
	if head := strings.TrimSpace(gitRun(t, res.LocalPath, "rev-parse", "origin/test")); head == "" {
		t.Fatal("origin/test did not resolve")
	}

	// Branches nobody asked for must stay out of the agent's view.
	if strings.Contains(remotes, "stale/exploration") {
		t.Fatalf("unrequested branch leaked into remote-tracking refs:\n%s", remotes)
	}

	// The command the agent ran must now reproduce the conflict. The identity vars are
	// required: the merge builds a commit, and with global config nulled git has no
	// user to attribute it to. On a machine where git can synthesise one from the host
	// this passes either way, but on a CI runner that cannot, the merge aborts before
	// touching the worktree and the conflict never materialises.
	cmd := exec.Command("git", "-C", res.LocalPath, "merge", "origin/prod")
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected merge conflict, merge succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "CONFLICT") {
		t.Fatalf("merge failed for a reason other than a conflict:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(res.LocalPath, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<<<<<<<") {
		t.Fatalf("conflict markers not materialized:\n%s", data)
	}
}
