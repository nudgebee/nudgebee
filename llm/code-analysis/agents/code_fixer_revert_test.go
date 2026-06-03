package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/session"

	"github.com/stretchr/testify/require"
)

// initRepo creates a git repo in dir with one initial committed file (tracked.txt).
func initRepo(t *testing.T, dir string) {
	t.Helper()
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("original\n"), 0644))
	run("add", "tracked.txt")
	run("commit", "-q", "-m", "init")
}

// statusPorcelain returns `git status --porcelain` output.
func statusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git status: %s", string(out))
	return string(out)
}

// newFixerForRevert builds the minimum CodeFixerAgent needed to call performRevert.
func newFixerForRevert(t *testing.T, workspace string) *CodeFixerAgent {
	t.Helper()
	return &CodeFixerAgent{
		logger:       common.NewLogger("test-analysis", "test-repo", "test-user", nil),
		WorkspaceDir: workspace,
	}
}

func TestPerformRevert_UntrackedFiles(t *testing.T) {
	// Regression: porcelain "??" entries used to be fed into `git checkout HEAD --`,
	// which fails because the path doesn't exist in HEAD. The whole rework cycle
	// then aborted even when the conflict resolution itself was clean.
	dir := t.TempDir()
	initRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "new_file.go"), []byte("package x\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "another_new.go"), []byte("package y\n"), 0644))

	fixer := newFixerForRevert(t, dir)
	sessionCtx := &session.SessionContext{}

	require.NoError(t, fixer.performRevert(context.Background(), "review feedback", sessionCtx))
	require.Empty(t, statusPorcelain(t, dir), "untracked files should have been removed")
}

func TestPerformRevert_ModifiedFiles(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("modified\n"), 0644))

	fixer := newFixerForRevert(t, dir)
	require.NoError(t, fixer.performRevert(context.Background(), "fb", &session.SessionContext{}))

	contents, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "original\n", string(contents))
	require.Empty(t, statusPorcelain(t, dir))
}

func TestPerformRevert_StagedNewFile(t *testing.T) {
	// porcelain status "A " (staged add). Previous parser would feed this to
	// `git checkout HEAD -- <path>`, which fails because the path is new.
	dir := t.TempDir()
	initRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "staged_new.go"), []byte("package z\n"), 0644))
	stage := exec.Command("git", "add", "staged_new.go")
	stage.Dir = dir
	require.NoError(t, stage.Run())

	fixer := newFixerForRevert(t, dir)
	require.NoError(t, fixer.performRevert(context.Background(), "fb", &session.SessionContext{}))
	require.Empty(t, statusPorcelain(t, dir))
}

func TestPerformRevert_DeletedTrackedFile(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	require.NoError(t, os.Remove(filepath.Join(dir, "tracked.txt")))

	fixer := newFixerForRevert(t, dir)
	require.NoError(t, fixer.performRevert(context.Background(), "fb", &session.SessionContext{}))

	_, err := os.Stat(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err, "deleted tracked file should be restored")
	require.Empty(t, statusPorcelain(t, dir))
}

func TestPerformRevert_MixedStates(t *testing.T) {
	// The realistic case from the failing run: a fix attempt produced a mix of
	// modifications to existing files plus new untracked test files. The old
	// parser tripped on the untracked file and aborted, losing the whole
	// rework cycle.
	dir := t.TempDir()
	initRepo(t, dir)

	// Modified tracked
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("changed\n"), 0644))
	// Untracked new
	require.NoError(t, os.WriteFile(filepath.Join(dir, "event_eventbridge_eligibility_test.go"), []byte("package x\n"), 0644))
	// Staged new
	require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package y\n"), 0644))
	stage := exec.Command("git", "add", "staged.go")
	stage.Dir = dir
	require.NoError(t, stage.Run())
	// Untracked dir with a file
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "newdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "newdir", "a.go"), []byte("package q\n"), 0644))

	require.NotEmpty(t, statusPorcelain(t, dir), "precondition: workspace should be dirty")

	fixer := newFixerForRevert(t, dir)
	require.NoError(t, fixer.performRevert(context.Background(), "fb", &session.SessionContext{}))

	require.Empty(t, statusPorcelain(t, dir), "all changes should be reverted")

	tracked, err := os.ReadFile(filepath.Join(dir, "tracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "original\n", string(tracked))

	for _, p := range []string{"event_eventbridge_eligibility_test.go", "staged.go", "newdir/a.go"} {
		_, err := os.Stat(filepath.Join(dir, p))
		require.True(t, os.IsNotExist(err), "%s should be removed", p)
	}
}

func TestPerformRevert_CleanWorkspace(t *testing.T) {
	// Idempotent: revert on a clean workspace must succeed and produce no diff.
	dir := t.TempDir()
	initRepo(t, dir)

	fixer := newFixerForRevert(t, dir)
	require.NoError(t, fixer.performRevert(context.Background(), "fb", &session.SessionContext{}))
	require.Empty(t, statusPorcelain(t, dir))
}
