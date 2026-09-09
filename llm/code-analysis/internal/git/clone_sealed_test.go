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

// makeRepoWithFutureFix builds an origin with two commits and returns the SHA of
// the FIRST. The second stands in for the fix that must stay invisible.
func makeRepoWithFutureFix(t *testing.T) (originDir, pinnedSHA string) {
	t.Helper()
	originDir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", originDir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--initial-branch=main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	// Allow fetching an arbitrary SHA, as GitHub and GitLab both do.
	run("config", "uploadpack.allowAnySHA1InWant", "true")

	if err := writeFile(filepath.Join(originDir, "bug.txt"), "buggy\n"); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "buggy state")
	pinnedSHA = run("rev-parse", "HEAD")

	if err := writeFile(filepath.Join(originDir, "bug.txt"), "FIXED\n"); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "THE FIX nobody should see")
	return originDir, pinnedSHA
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestCloneSealedAtCommit_CannotReachFutureCommits(t *testing.T) {
	origin, pinned := makeRepoWithFutureFix(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	dst := filepath.Join(t.TempDir(), "wt")

	res, err := gc.CloneSealedAtCommit(context.Background(), origin, nil, pinned, 50, dst)
	if err != nil {
		t.Fatalf("CloneSealedAtCommit: %v", err)
	}
	if res.CommitHash != pinned {
		t.Fatalf("checked out %s, want pinned %s", res.CommitHash, pinned)
	}

	git := func(args ...string) string {
		out, _ := exec.Command("git", append([]string{"-C", dst}, args...)...).CombinedOutput()
		return string(out)
	}

	// The whole point: the fix commit exists upstream but must be unreachable.
	if all := git("log", "--all", "--oneline"); strings.Contains(all, "THE FIX") {
		t.Errorf("git log --all exposed a future commit:\n%s", all)
	}
	if content := git("show", "HEAD:bug.txt"); !strings.Contains(content, "buggy") {
		t.Errorf("working tree is not at the pinned commit: %s", content)
	}
	// No remote means no way to widen the view later, including by the agent.
	if remotes := strings.TrimSpace(git("remote")); remotes != "" {
		t.Errorf("origin still configured: %q", remotes)
	}
	if out := git("fetch", "origin"); !strings.Contains(out, "origin") {
		t.Errorf("expected fetch to fail with no remote, got: %s", out)
	}
}

func TestCloneSealedAtCommit_KeepsAncestorHistory(t *testing.T) {
	origin, pinned := makeRepoWithFutureFix(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	dst := filepath.Join(t.TempDir(), "wt")

	if _, err := gc.CloneSealedAtCommit(context.Background(), origin, nil, pinned, 50, dst); err != nil {
		t.Fatalf("CloneSealedAtCommit: %v", err)
	}
	// blame and `log -S` are legitimate agent tools; sealing must not cost them.
	out, err := exec.Command("git", "-C", dst, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %s: %v", out, err)
	}
	if !strings.Contains(string(out), "buggy state") {
		t.Errorf("ancestor history missing:\n%s", out)
	}
}

func TestCloneSealedAtCommit_RejectsNonSHA(t *testing.T) {
	gc := NewGitClient(t.TempDir(), 30*time.Second, 0)
	for _, bad := range []string{"HEAD~3", "main", "--upload-pack=evil", ""} {
		if _, err := gc.CloneSealedAtCommit(context.Background(), "https://example.com/a/b.git", nil, bad, 10, t.TempDir()); err == nil {
			t.Errorf("accepted non-SHA revision %q", bad)
		}
	}
}
