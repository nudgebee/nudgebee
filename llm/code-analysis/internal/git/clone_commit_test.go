package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCloneOrReuseRepositoryAtCommit_PinsHistoricalCommit covers the case the
// branch-only clone path could not express: analysing the code as it was at a
// specific revision rather than at a branch tip.
//
// The pinned commit is deliberately an *ancestor* that is not the tip of any
// branch, because that is the shape that fails when the checkout silently falls
// back to HEAD — the tip of a branch would coincidentally produce the right
// content and hide the bug.
func TestCloneOrReuseRepositoryAtCommit_PinsHistoricalCommit(t *testing.T) {
	origin := buildOriginRepo(t)

	// The first commit on "test" holds BASE; the branch tip holds TEST.
	baseSHA := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test~1"))
	tipSHA := strings.TrimSpace(gitRun(t, origin, "rev-parse", "test"))
	if baseSHA == "" || baseSHA == tipSHA {
		t.Fatalf("fixture did not produce a distinct ancestor commit (base=%q tip=%q)", baseSHA, tipSHA)
	}

	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	res, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "test", baseSHA, worktree)
	if err != nil {
		t.Fatalf("clone at commit: %v", err)
	}

	if res.CommitHash != baseSHA {
		t.Fatalf("checked out %s, want pinned commit %s", res.CommitHash, baseSHA)
	}

	// Content, not just the SHA — proves the worktree really is at that revision.
	content := gitRun(t, res.LocalPath, "show", "HEAD:f.txt")
	if !strings.Contains(content, "BASE") {
		t.Fatalf("worktree content is not the pinned revision:\n%s", content)
	}
	if strings.Contains(content, "TEST") {
		t.Fatalf("worktree fell through to the branch tip:\n%s", content)
	}
}

// TestCloneOrReuseRepositoryAtCommit_UnknownCommitFails is the important half:
// a commit that does not exist must fail loudly. The pre-existing worktree
// fallback checks out HEAD when a ref will not resolve, which for a pinned
// commit would hand back a confident analysis of the wrong code.
func TestCloneOrReuseRepositoryAtCommit_UnknownCommitFails(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	missing := "0123456789abcdef0123456789abcdef01234567"
	_, err := gc.CloneOrReuseRepositoryAtCommit(context.Background(), origin, nil, "test", missing, worktree)
	if err == nil {
		t.Fatal("expected an error for an unknown commit, got a successful clone")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should name the missing commit, got: %v", err)
	}
}

func TestValidateCommitSHA(t *testing.T) {
	valid := []string{
		"d16bfe05a744909de4b27f5875fe0d4ed41ce607",
		"d16bfe0",
	}
	for _, c := range valid {
		if err := ValidateCommitSHA(c); err != nil {
			t.Errorf("ValidateCommitSHA(%q) = %v, want nil", c, err)
		}
	}

	// Rev expressions and ref names are refused on purpose: the value is used
	// both as a checkout target and as a fetch argument, so anything that is not
	// plainly an object name should not be interpreted.
	invalid := []string{
		"",
		"HEAD",
		"HEAD~3",
		"main",
		"d16bfe", // shorter than git's 7-char minimum
		"D16BFE05A744909DE4B27F5875FE0D4ED41CE607", // uppercase
		"--upload-pack=evil",
		"d16bfe05 --force",
		"d16bfe05a744909de4b27f5875fe0d4ed41ce607a", // 41 chars
	}
	for _, c := range invalid {
		if err := ValidateCommitSHA(c); err == nil {
			t.Errorf("ValidateCommitSHA(%q) = nil, want an error", c)
		}
	}
}
