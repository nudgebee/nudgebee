package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// commitOnOrigin adds one commit to the branch origin has checked out and
// returns its SHA.
func commitOnOrigin(t *testing.T, origin, marker string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("line1\n"+marker+"\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "commit", "-qam", marker)
	return strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
}

func cloneNoBranch(t *testing.T, gc *GitClient, origin, commit, worktree string) *CloneResult {
	t.Helper()
	result, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "", commit, worktree)
	if err != nil {
		t.Fatalf("clone (commit=%q): %v", commit, err)
	}
	return result
}

// A checkout that asks for no branch and no commit is the default branch as it
// is on the remote now. It used to be the commit the pod first cloned: the bare
// repo's own branch never moves, so in a long-lived pod every later
// conversation worked on that commit however far the remote had moved, and a
// fix written there was refused at PR time as stale.
func TestCloneOrReuseRepositoryAtCommit_NoBranchIsTheRemoteTip(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)

	first := cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "conversation-1"))
	tip := commitOnOrigin(t, origin, "NEWER")
	if first.CommitHash == tip {
		t.Fatal("setup: the remote did not move")
	}

	second := cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "conversation-2"))
	if second.CommitHash != tip {
		t.Fatalf("a later conversation's checkout is %s, the remote default branch is at %s", second.CommitHash, tip)
	}
	if second.Branch != "test" {
		t.Errorf("Branch = %q, want the default branch name %q", second.Branch, "test")
	}
}

// The first clone of a repository in a pod must end up tracking the default
// branch, or the next fetch has nowhere to record the new tip.
func TestCloneOrReuseRepositoryAtCommit_NoBranchFirstCloneTracksDefaultBranch(t *testing.T) {
	origin := buildOriginRepo(t)
	tip := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)

	result := cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "wt"))
	if result.CommitHash != tip {
		t.Fatalf("checkout is %s, the remote default branch is at %s", result.CommitHash, tip)
	}
	baseDir := filepath.Join(workspace, "repos", repoKeyFromURL(origin))
	if tracked := strings.TrimSpace(gitRun(t, baseDir, "rev-parse", "origin/test")); tracked != tip {
		t.Fatalf("origin/test in the base is %s, want %s", tracked, tip)
	}
}

// An analysis pinned to the deployed commit leaves the conversation's worktree
// detached there. The next call in that conversation that asks for nothing — a
// run that will write a fix and open a PR — must not inherit it.
func TestCloneOrReuseRepositoryAtCommit_NoBranchLeavesAPinnedCheckout(t *testing.T) {
	origin := buildOriginRepo(t)
	deployed := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	tip := commitOnOrigin(t, origin, "NEWER")
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	if pinned := cloneNoBranch(t, gc, origin, deployed, worktree); pinned.CommitHash != deployed {
		t.Fatalf("setup: pinned checkout is %s, want %s", pinned.CommitHash, deployed)
	}

	next := cloneNoBranch(t, gc, origin, "", worktree)
	if next.CommitHash != tip {
		t.Fatalf("the call that asked for no commit got %s (the earlier pin), the remote tip is %s", next.CommitHash, tip)
	}
}

// Reuse is still the rule while the worktree is where the remote is: a second
// call in the same conversation keeps what the first one left in the tree.
func TestCloneOrReuseRepositoryAtCommit_NoBranchReusesWhileTipUnchanged(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	first := cloneNoBranch(t, gc, origin, "", worktree)
	marker := filepath.Join(worktree, "applied-fix-marker.txt")
	if err := os.WriteFile(marker, []byte("fix already here"), 0644); err != nil {
		t.Fatal(err)
	}

	second := cloneNoBranch(t, gc, origin, "", worktree)
	if second.CommitHash != first.CommitHash {
		t.Fatalf("reuse changed HEAD: first=%s second=%s", first.CommitHash, second.CommitHash)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the worktree was recreated although the remote had not moved: %v", err)
	}
}

// Once the remote has moved, the same call gets the new tip rather than the
// tree it was handed last time — the same answer an explicitly named branch
// has always got.
func TestCloneOrReuseRepositoryAtCommit_NoBranchFollowsTheTip(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	cloneNoBranch(t, gc, origin, "", worktree)
	tip := commitOnOrigin(t, origin, "NEWER")

	second := cloneNoBranch(t, gc, origin, "", worktree)
	if second.CommitHash != tip {
		t.Fatalf("checkout is %s after the remote moved to %s", second.CommitHash, tip)
	}
}

// A pinned call is untouched by any of this: it gets its commit whether the
// worktree was at the tip or not.
func TestCloneOrReuseRepositoryAtCommit_PinnedCallAfterATipCheckout(t *testing.T) {
	origin := buildOriginRepo(t)
	deployed := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	commitOnOrigin(t, origin, "NEWER")
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	cloneNoBranch(t, gc, origin, "", worktree)
	if pinned := cloneNoBranch(t, gc, origin, deployed, worktree); pinned.CommitHash != deployed {
		t.Fatalf("pinned checkout is %s, want %s", pinned.CommitHash, deployed)
	}
}

// The default branch is whatever the remote says it is now, not what it was
// when the base was cloned: a rename on the remote is followed.
func TestCloneOrReuseRepositoryAtCommit_NoBranchSurvivesADefaultBranchRename(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)

	cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "conversation-1"))
	gitRun(t, origin, "branch", "-m", "test", "trunk")
	tip := commitOnOrigin(t, origin, "NEWER")

	second := cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "conversation-2"))
	if second.CommitHash != tip {
		t.Fatalf("checkout is %s, the renamed default branch is at %s", second.CommitHash, tip)
	}
}

// The base repo is shared by every call for the repository, and its own HEAD
// names the branch of whichever call created it. A base first cloned for a
// release branch must not turn "no branch asked" into that release branch.
func TestCloneOrReuseRepositoryAtCommit_NoBranchIsTheDefaultBranchNotTheBasesFirstBranch(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)

	// buildOriginRepo leaves "test" checked out (the default) and "prod" on a
	// different commit.
	first, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "prod", "", filepath.Join(t.TempDir(), "conversation-1"))
	if err != nil {
		t.Fatalf("clone of prod: %v", err)
	}
	tip := commitOnOrigin(t, origin, "NEWER")

	second := cloneNoBranch(t, gc, origin, "", filepath.Join(t.TempDir(), "conversation-2"))
	if second.CommitHash != tip {
		t.Fatalf("checkout is %s (prod is at %s), the default branch is at %s", second.CommitHash, first.CommitHash, tip)
	}
	if second.Branch != "test" {
		t.Errorf("Branch = %q, want the default branch %q", second.Branch, "test")
	}
}

// Whether a worktree is still at the tip is decided from what the remote just
// answered, not by fetching through the base's stored remote URL — that URL
// carries the token of whichever call set it last, which may have expired. A
// worktree that is at the tip keeps what is in it either way.
func TestCloneOrReuseRepositoryAtCommit_NoBranchReuseDoesNotNeedTheStoredRemote(t *testing.T) {
	origin := buildOriginRepo(t)
	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	first := cloneNoBranch(t, gc, origin, "", worktree)
	marker := filepath.Join(worktree, "applied-fix-marker.txt")
	if err := os.WriteFile(marker, []byte("fix already here"), 0644); err != nil {
		t.Fatal(err)
	}
	baseDir := filepath.Join(workspace, "repos", repoKeyFromURL(origin))
	gitRun(t, baseDir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-longer-valid"))

	second := cloneNoBranch(t, gc, origin, "", worktree)
	if second.CommitHash != first.CommitHash {
		t.Fatalf("reuse changed HEAD: first=%s second=%s", first.CommitHash, second.CommitHash)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the worktree was recreated although it was at the tip: %v", err)
	}
}

// When the remote cannot be asked, nothing is inferred and an existing worktree
// is reused as it always was — an unreachable remote is not a reason to throw
// away what a previous call left there.
func TestCloneOrReuseRepositoryAtCommit_NoBranchUnreachableRemoteKeepsTheWorktree(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	first := cloneNoBranch(t, gc, origin, "", worktree)
	marker := filepath.Join(worktree, "applied-fix-marker.txt")
	if err := os.WriteFile(marker, []byte("fix already here"), 0644); err != nil {
		t.Fatal(err)
	}
	// Same URL (so the same base and worktree), nothing there to answer.
	if err := os.Rename(origin, origin+".gone"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(origin+".gone", origin) })

	second := cloneNoBranch(t, gc, origin, "", worktree)
	if second.CommitHash != first.CommitHash {
		t.Fatalf("reuse changed HEAD: first=%s second=%s", first.CommitHash, second.CommitHash)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the worktree was removed although the remote could not be asked: %v", err)
	}
}
