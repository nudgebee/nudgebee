package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeOriginRepo creates a bare-able source repo with multiple branches.
// Returns the path to use as the "remote" URL for clone tests.
func makeOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0644))
	run("add", ".")
	run("commit", "-q", "-m", "main commit")

	// Create stale branches that simulate the noise the agent shouldn't see.
	for _, br := range []string{"claude/fix-powershell-path-error-oDhMZ", "claude/explore-something", "feature/unrelated"} {
		run("checkout", "-q", "-b", br)
		fname := strings.ReplaceAll(br, "/", "-") + ".txt"
		require.NoError(t, os.WriteFile(filepath.Join(dir, fname), []byte("noise\n"), 0644))
		run("add", ".")
		run("commit", "-q", "-m", "noise on "+br)
		run("checkout", "-q", "main")
	}

	// The branch the analysis actually wants.
	run("checkout", "-q", "-b", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("test branch\n"), 0644))
	run("add", ".")
	run("commit", "-q", "-m", "test commit")
	run("checkout", "-q", "main")
	return dir
}

// allBranchRefs returns every branch ref in the bare clone — both
// refs/heads/* (the branch the bare clone was created with) and
// refs/remotes/origin/* (branches fetched on later reuse). The bare-clone
// model splits the requested branch across these two namespaces, so any
// audit of "what branches does the agent have access to" must check both.
func allBranchRefs(t *testing.T, baseDir string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", baseDir, "for-each-ref",
		"--format=%(refname:short)",
		"refs/heads/", "refs/remotes/origin/")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "for-each-ref: %s", string(out))
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs
}

func TestCloneOrReuseRepository_OnlyFetchesRequestedBranch(t *testing.T) {
	// Regression for the LLM-drift class of failures: a full clone exposes every
	// branch on origin (including ~180 stale claude/* exploration branches),
	// which `git branch -a` surfaces to the agent and pulls it off-task. The
	// bare clone must be single-branch by construction.
	origin := makeOriginRepo(t)
	workspace := t.TempDir()
	gc := NewGitClient(workspace, 30*time.Second, 0)

	wt := filepath.Join(t.TempDir(), "wt")
	res, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "test", wt)
	require.NoError(t, err)
	require.NotNil(t, res)

	// Find the bare base dir to inspect the configured refs.
	baseDir := filepath.Join(workspace, "repos")
	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly one bare clone under repos/")
	bareDir := filepath.Join(baseDir, entries[0].Name())

	refs := allBranchRefs(t, bareDir)
	require.Equal(t, []string{"test"}, refs,
		"only the requested branch should be present in the bare clone")

	// And critically — none of the stale claude/* branches should leak in.
	for _, b := range refs {
		require.NotContains(t, b, "claude/", "claude/* branches must never be fetched")
	}
}

func TestCloneOrReuseRepository_AddsBranchOnReuse(t *testing.T) {
	// Reuse with a different branch must widen the refspec and fetch only that
	// branch — never the full remote.
	origin := makeOriginRepo(t)
	workspace := t.TempDir()
	gc := NewGitClient(workspace, 30*time.Second, 0)

	// First analysis on "test"
	wt1 := filepath.Join(t.TempDir(), "wt1")
	_, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "test", wt1)
	require.NoError(t, err)

	// Second analysis on "main" — reuses the base clone
	wt2 := filepath.Join(t.TempDir(), "wt2")
	_, err = gc.CloneOrReuseRepository(context.Background(), origin, nil, "main", wt2)
	require.NoError(t, err)

	baseDir := filepath.Join(workspace, "repos")
	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	bareDir := filepath.Join(baseDir, entries[0].Name())

	// After reuse, the bare clone holds:
	//   refs/heads/test                  (from the initial single-branch clone)
	//   refs/remotes/origin/main         (added on second analysis)
	// No other branches should be present.
	refs := allBranchRefs(t, bareDir)
	require.ElementsMatch(t, []string{"test", "origin/main"}, refs,
		"only the requested branches should be present")

	// And critically — none of the stale claude/* branches should leak in.
	for _, b := range refs {
		require.NotContains(t, b, "claude/", "claude/* branches must never be fetched")
	}
}
