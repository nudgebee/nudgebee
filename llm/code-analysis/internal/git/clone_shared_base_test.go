package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// checkoutError runs a git command that needs the checkout's bookkeeping, which
// lives inside the shared clone, and returns git's complaint when it is gone.
func checkoutError(dir string) string {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// markSharedClone leaves a file inside the shared clone of url so a test can
// tell whether a later call kept that clone or replaced it.
func markSharedClone(t *testing.T, workspace, url string) string {
	t.Helper()
	marker := filepath.Join(workspace, "repos", repoKeyFromURL(url), "left-by-the-test")
	if err := os.WriteFile(marker, []byte("x"), 0644); err != nil {
		t.Fatalf("mark shared clone: %v", err)
	}
	return marker
}

// storedRefspecs returns the branch list in a clone's config, empty when there
// is none. `git config --get-all` exits non-zero for a key that is not set.
func storedRefspecs(dir string) string {
	out, _ := exec.Command("git", "-C", dir, "config", "--get-all", "remote.origin.fetch").Output()
	return strings.TrimSpace(string(out))
}

// Every checkout made from one repository shares a single clone and keeps its
// bookkeeping inside it. A branch one run fetched and the remote later deleted
// must not cost the other runs their checkouts: here the next run asks for a
// pinned commit and names no branch at all, which is how a deployed revision is
// investigated.
func TestCloneOrReuseRepositoryAtCommit_DeletedBranchLeavesOtherCheckoutsUsable(t *testing.T) {
	origin := buildOriginRepo(t) // branches: test (default), prod, stale/exploration
	url := "file://" + origin
	pinned := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onProd, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "prod", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone prod: %v", err)
	}
	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}
	marker := markSharedClone(t, workspace, url)

	gitRun(t, origin, "branch", "-D", "prod")

	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "", pinned, filepath.Join(t.TempDir(), "wt")); err != nil {
		t.Fatalf("pinned clone after a branch was deleted on the remote: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the shared clone was replaced while checkouts were using it: %v", err)
	}
	for branch, res := range map[string]*CloneResult{"prod": onProd, "test": onTest} {
		if msg := checkoutError(res.LocalPath); msg != "" {
			t.Fatalf("checkout of %s is no longer usable: %s", branch, msg)
		}
	}
}

// A request for a branch the remote no longer has is that request's problem. It
// gets an error that names the branch; the checkouts already made from the
// shared clone keep working.
func TestCloneOrReuseRepositoryAtCommit_MissingBranchFailsAndLeavesOtherCheckoutsUsable(t *testing.T) {
	origin := buildOriginRepo(t)
	url := "file://" + origin

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}
	marker := markSharedClone(t, workspace, url)

	gitRun(t, origin, "branch", "-D", "prod")

	_, err = gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "prod", "", filepath.Join(t.TempDir(), "wt"))
	if err == nil {
		t.Fatal("expected an error for a branch the remote does not have")
	}
	if !strings.Contains(err.Error(), "prod") {
		t.Fatalf("error does not name the missing branch: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the shared clone was replaced while a checkout was using it: %v", err)
	}
	if msg := checkoutError(onTest.LocalPath); msg != "" {
		t.Fatalf("checkout of test is no longer usable: %s", msg)
	}
}

// A remote that cannot be reached fails the request that needed it. The clone is
// not what is broken, so it stays, and so do the checkouts made from it.
func TestCloneOrReuseRepositoryAtCommit_UnreachableRemoteFailsAndLeavesOtherCheckoutsUsable(t *testing.T) {
	origin := buildOriginRepo(t)
	url := "file://" + origin

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}
	marker := markSharedClone(t, workspace, url)

	if err := os.Rename(origin, origin+".moved"); err != nil {
		t.Fatalf("move the remote away: %v", err)
	}

	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "prod", "", filepath.Join(t.TempDir(), "wt")); err == nil {
		t.Fatal("expected an error while the remote cannot be reached")
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the shared clone was replaced while a checkout was using it: %v", err)
	}
	if msg := checkoutError(onTest.LocalPath); msg != "" {
		t.Fatalf("checkout of test is no longer usable: %s", msg)
	}
}

// A clone made by an earlier version carries a branch list in its config. Once a
// branch on that list is deleted on the remote, a plain `git fetch` in any
// checkout fails. The list is removed the next time the clone is used.
func TestCloneOrReuseRepositoryAtCommit_ClearsABranchListLeftByAnEarlierVersion(t *testing.T) {
	origin := buildOriginRepo(t)
	url := "file://" + origin
	pinned := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}

	// What the earlier version left behind: prod on the list and fetched.
	base := filepath.Join(workspace, "repos", repoKeyFromURL(url))
	gitRun(t, base, "config", "--add", "remote.origin.fetch", "+refs/heads/prod:refs/remotes/origin/prod")
	gitRun(t, base, "fetch", "origin")
	gitRun(t, origin, "branch", "-D", "prod")
	if out, err := exec.Command("git", "-C", onTest.LocalPath, "fetch", "origin").CombinedOutput(); err == nil {
		t.Fatalf("fixture: a fetch over a list naming a deleted branch should fail, got:\n%s", out)
	}

	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "", pinned, filepath.Join(t.TempDir(), "wt")); err != nil {
		t.Fatalf("pinned clone over a list naming a deleted branch: %v", err)
	}

	if stored := storedRefspecs(base); stored != "" {
		t.Fatalf("the branch list is still in the clone's config:\n%s", stored)
	}
	gitRun(t, onTest.LocalPath, "fetch", "origin")

	// The deleted branch's remote-tracking ref is still there. It gets in
	// nobody's way: a branch the remote does have is fetched as usual.
	gitRun(t, base, "rev-parse", "--verify", "refs/remotes/origin/prod")
	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "stale/exploration", "", filepath.Join(t.TempDir(), "wt")); err != nil {
		t.Fatalf("clone of a live branch next to a deleted branch's old ref: %v", err)
	}
}

// A branch name is written into a fetch refspec, where "*" would turn it into a
// pattern and bring in branches nobody asked for. Such a name is refused before
// it reaches git, as the branch under analysis and as the base it merges into.
func TestCloneOrReuseRepositoryAtCommit_BranchPatternIsRefused(t *testing.T) {
	origin := buildOriginRepo(t)
	url := "file://" + origin

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}

	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "stale/*", "", filepath.Join(t.TempDir(), "wt")); err == nil {
		t.Fatal("expected a branch pattern to be refused")
	}
	if _, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"), "stale/*"); err == nil {
		t.Fatal("expected a base branch pattern to be refused")
	}

	if remotes := gitRun(t, onTest.LocalPath, "branch", "-r"); strings.Contains(remotes, "stale/exploration") {
		t.Fatalf("a branch nobody asked for was fetched:\n%s", remotes)
	}
}

// Calls for the same repository arrive together. None of them may cost another
// its checkout, whatever the remote has deleted in the meantime.
func TestCloneOrReuseRepositoryAtCommit_ConcurrentCallsLeaveCheckoutsUsable(t *testing.T) {
	origin := buildOriginRepo(t)
	url := "file://" + origin
	pinned := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))

	workspace := t.TempDir()
	gc := NewGitClient(workspace, 60*time.Second, 0)
	ctx := context.Background()

	onProd, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "prod", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone prod: %v", err)
	}
	onTest, err := gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, "test", "", filepath.Join(t.TempDir(), "wt"))
	if err != nil {
		t.Fatalf("clone test: %v", err)
	}
	marker := markSharedClone(t, workspace, url)

	gitRun(t, origin, "branch", "-D", "prod")

	calls := []struct{ branch, commit string }{
		{"", pinned}, {"test", ""}, {"prod", ""}, {"stale/exploration", ""}, {"", pinned}, {"prod", ""}, {"test", ""},
	}
	results := make([]*CloneResult, len(calls))
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		dir := filepath.Join(t.TempDir(), "wt")
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = gc.CloneOrReuseRepositoryAtCommit(ctx, url, nil, c.branch, c.commit, dir)
		}()
	}
	wg.Wait()

	for i, c := range calls {
		if c.branch == "prod" {
			if errs[i] == nil {
				t.Fatalf("call %d: expected an error for the deleted branch", i)
			}
			continue
		}
		if errs[i] != nil {
			t.Fatalf("call %d (branch %q, commit %q): %v", i, c.branch, c.commit, errs[i])
		}
		if msg := checkoutError(results[i].LocalPath); msg != "" {
			t.Fatalf("call %d: its own checkout is not usable: %s", i, msg)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the shared clone was replaced while checkouts were using it: %v", err)
	}
	for branch, res := range map[string]*CloneResult{"prod": onProd, "test": onTest} {
		if msg := checkoutError(res.LocalPath); msg != "" {
			t.Fatalf("checkout of %s is no longer usable: %s", branch, msg)
		}
	}
}
