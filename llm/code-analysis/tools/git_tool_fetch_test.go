package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRewriteFetchForTracking(t *testing.T) {
	cases := []struct {
		in   []string
		out  []string
		did  bool
		name string
	}{
		{[]string{"fetch", "origin", "prod"}, []string{"fetch", "origin", "prod:refs/remotes/origin/prod"}, true, "plain branch"},
		{[]string{"fetch", "origin", "pr-prod-to-test-123"}, []string{"fetch", "origin", "pr-prod-to-test-123:refs/remotes/origin/pr-prod-to-test-123"}, true, "pr head branch"},
		{[]string{"fetch", "origin", "claude/foo"}, []string{"fetch", "origin", "claude/foo:refs/remotes/origin/claude/foo"}, true, "slashed branch"},
		{[]string{"fetch", "origin"}, []string{"fetch", "origin"}, false, "no branch"},
		{[]string{"fetch", "origin", "prod:refs/remotes/origin/prod"}, []string{"fetch", "origin", "prod:refs/remotes/origin/prod"}, false, "already refspec"},
		{[]string{"fetch", "origin", "prod", "--prune"}, []string{"fetch", "origin", "prod", "--prune"}, false, "extra flag"},
		{[]string{"fetch", "origin", "--all"}, []string{"fetch", "origin", "--all"}, false, "flag not branch"},
		{[]string{"fetch", "upstream", "prod"}, []string{"fetch", "upstream", "prod"}, false, "non-origin remote"},
		{[]string{"fetch", "origin", "../evil"}, []string{"fetch", "origin", "../evil"}, false, "unsafe dotdot"},
		{[]string{"fetch", "origin", "-x"}, []string{"fetch", "origin", "-x"}, false, "leading dash"},
		{[]string{"status"}, []string{"status"}, false, "not fetch"},
	}
	for _, c := range cases {
		got, did := rewriteFetchForTracking(append([]string(nil), c.in...))
		if did != c.did || !reflect.DeepEqual(got, c.out) {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", c.name, got, did, c.out, c.did)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestGitTool_FetchEnablesConflictMerge reproduces the debug-session scenario on
// a single-branch bare clone (main's clone shape): the agent fetches the PR head
// and merges it. Without the fetch rewrite `origin/<head>` would not resolve and
// the merge would fail with "not something we can merge".
func TestGitTool_FetchEnablesConflictMerge(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if err := os.MkdirAll(origin, 0755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, origin, "init", "-q", "-b", "test")
	writeFile(t, filepath.Join(origin, "f.txt"), "l1\nBASE\nl3\n")
	gitCmd(t, origin, "add", ".")
	gitCmd(t, origin, "commit", "-qm", "base")
	gitCmd(t, origin, "checkout", "-q", "-b", "prod")
	writeFile(t, filepath.Join(origin, "f.txt"), "l1\nPROD\nl3\n")
	gitCmd(t, origin, "commit", "-qam", "prod")
	gitCmd(t, origin, "checkout", "-q", "test")
	writeFile(t, filepath.Join(origin, "f.txt"), "l1\nTEST\nl3\n")
	gitCmd(t, origin, "commit", "-qam", "test")

	// Single-branch bare clone of test + worktree, mirroring internal/git clone.
	base := filepath.Join(root, "base.git")
	gitCmd(t, root, "clone", "--bare", "--single-branch", "--branch", "test", "-q", origin, base)
	wt := filepath.Join(root, "wt")
	gitCmd(t, base, "worktree", "add", "-q", "--detach", wt, "test")
	// A committer identity is needed for `git merge`; CI configures none globally.
	gitCmd(t, wt, "config", "user.email", "t@t")
	gitCmd(t, wt, "config", "user.name", "t")

	// Agent fetches the head branch through the git tool.
	tool := NewGitTool(wt)
	resp := tool.Execute(context.Background(), map[string]any{
		"args":              []any{"fetch", "origin", "prod"},
		"working_directory": wt,
	})
	if resp.Status != "success" {
		t.Fatalf("fetch failed: %+v", resp)
	}

	// origin/prod must now resolve, and the merge must produce conflict markers.
	if out, err := exec.Command("git", "-C", wt, "rev-parse", "--verify", "-q", "origin/prod").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("origin/prod did not resolve after tool fetch")
	}
	mergeOut, _ := exec.Command("git", "-C", wt, "merge", "origin/prod").CombinedOutput()
	data, _ := os.ReadFile(filepath.Join(wt, "f.txt"))
	if !strings.Contains(string(data), "<<<<<<<") {
		t.Fatalf("conflict markers not materialized (merge said: %s)", mergeOut)
	}
}
