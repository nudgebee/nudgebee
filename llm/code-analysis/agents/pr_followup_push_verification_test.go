package agents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

func pushProbeGitCmd(t *testing.T, dir string, args ...string) string {
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

// headPushedToRemote must distinguish "HEAD moved locally" from "HEAD is
// actually on the remote" — a local-only operation (a merge, or a commit that
// was never pushed) moves HEAD too, and gets silently discarded with the rest
// of the ephemeral workspace. This is issue #36634's most serious follow-on:
// a run that locally merged origin/main (moving HEAD) without ever pushing
// was misclassified as a successful commit, and posted a PR comment
// describing a commit that only ever existed in the torn-down workspace.
func TestHeadPushedToRemote(t *testing.T) {
	root := t.TempDir()

	// origin must be bare: pushing into a non-bare repo's checked-out branch
	// is refused by git ("branch is currently checked out"). Build it from a
	// throwaway seed working tree instead.
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0755); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, seed, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, seed, "add", ".")
	pushProbeGitCmd(t, seed, "commit", "-qm", "base")

	origin := filepath.Join(root, "origin")
	pushProbeGitCmd(t, root, "clone", "-q", "--bare", seed, origin)

	clone := filepath.Join(root, "clone")
	pushProbeGitCmd(t, root, "clone", "-q", origin, clone)

	a := &PRFollowupAgent{
		workspaceDir: clone,
		logger:       common.NewLogger("test", "test/repo", "", nil),
		provider:     gitprovider.ParseProvider("github"),
	}

	t.Run("real push reaches the remote", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
		pushProbeGitCmd(t, clone, "commit", "-qam", "real change")
		pushProbeGitCmd(t, clone, "push", "-q", "origin", "main")
		head := strings.TrimSpace(pushProbeGitCmd(t, clone, "rev-parse", "HEAD"))

		if !a.headPushedToRemote(head, "main") {
			t.Fatal("expected true: this commit was actually pushed")
		}
	})

	t.Run("local-only commit never reaches the remote", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(clone, "g.txt"), []byte("local only\n"), 0644); err != nil {
			t.Fatal(err)
		}
		pushProbeGitCmd(t, clone, "add", ".")
		pushProbeGitCmd(t, clone, "commit", "-qm", "local-only change, never pushed")
		head := strings.TrimSpace(pushProbeGitCmd(t, clone, "rev-parse", "HEAD"))

		if a.headPushedToRemote(head, "main") {
			t.Fatal("expected false: HEAD moved locally but was never pushed")
		}
	})
}

// newAutoCommitFixture builds a bare origin + working clone pair, same shape
// as TestHeadPushedToRemote, and returns the agent plus preHead so callers
// can dirty the clone and exercise autoCommitOrDiscard.
func newAutoCommitFixture(t *testing.T) (a *PRFollowupAgent, clone, preHead string) {
	t.Helper()
	root := t.TempDir()

	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0755); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, seed, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, seed, "add", ".")
	pushProbeGitCmd(t, seed, "commit", "-qm", "base")

	origin := filepath.Join(root, "origin")
	pushProbeGitCmd(t, root, "clone", "-q", "--bare", seed, origin)

	clone = filepath.Join(root, "clone")
	pushProbeGitCmd(t, root, "clone", "-q", origin, clone)

	a = &PRFollowupAgent{
		workspaceDir: clone,
		logger:       common.NewLogger("test", "test/repo", "", nil),
		provider:     gitprovider.ParseProvider("github"),
	}
	preHead = strings.TrimSpace(pushProbeGitCmd(t, clone, "rev-parse", "HEAD"))
	return a, clone, preHead
}

// autoCommitOrDiscard is the deterministic replacement for asking the LLM to
// run git commit/push itself — issue #36634's core fix. A small, real edit
// must actually reach the remote.
func TestAutoCommitOrDiscard_SmallDiffCommitsAndPushes(t *testing.T) {
	a, clone, preHead := newAutoCommitFixture(t)

	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	committed, head := a.autoCommitOrDiscard(preHead, "main", "fixed the thing", "PR", "36518")
	if !committed {
		t.Fatal("expected a small, legitimate diff to be committed and pushed")
	}
	if head == preHead {
		t.Fatal("HEAD did not move")
	}
	if !a.headPushedToRemote(head, "main") {
		t.Fatal("commit did not actually reach the remote")
	}

	msg := strings.TrimSpace(pushProbeGitCmd(t, clone, "log", "-1", "--format=%s"))
	if msg != "fixed the thing" {
		t.Fatalf("commit message = %q, want the run's summary %q", msg, "fixed the thing")
	}
}

// A working tree with far more changed files than a real PR-followup fix
// would ever touch must be discarded, not committed — this is the exact
// shape of the live incident that motivated this function: the agent left
// hundreds of unrelated files deleted after an apparent bad git operation.
// Auto-committing that verbatim would have pushed it straight to the PR.
func TestAutoCommitOrDiscard_OversizedDiffIsDiscarded(t *testing.T) {
	a, clone, preHead := newAutoCommitFixture(t)

	for i := range autoCommitMaxFiles + 5 {
		path := filepath.Join(clone, fmt.Sprintf("unexpected_%d.txt", i))
		if err := os.WriteFile(path, []byte("unrelated\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	committed, head := a.autoCommitOrDiscard(preHead, "main", "", "PR", "36518")
	if committed {
		t.Fatal("expected an oversized diff to be refused, not committed")
	}
	if head != preHead {
		t.Fatalf("HEAD moved to %q despite refusing to commit — should stay at preHead %q", head, preHead)
	}
	status := strings.TrimSpace(pushProbeGitCmd(t, clone, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("workspace was not cleaned up after refusing the diff: %q", status)
	}
}

// sanitizeCommitMessage is the fix for the exact live incident: the LLM
// committed on its own (before autoCommitOrDiscard ever ran) with the raw
// submit_analysis JSON payload as the message. Simulates that by committing
// with a JSON-shaped message directly, then checking it gets rewritten and
// re-pushed to the remote.
func TestSanitizeCommitMessage_RewritesAndRepushesBadMessage(t *testing.T) {
	a, clone, preHead := newAutoCommitFixture(t)

	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, clone, "commit", "-qam", `{"title": "raw json dump", "answer": ""}`)
	pushProbeGitCmd(t, clone, "push", "-q", "origin", "main")
	badHead := strings.TrimSpace(pushProbeGitCmd(t, clone, "rev-parse", "HEAD"))
	if badHead == preHead {
		t.Fatal("setup failed: commit did not move HEAD")
	}

	newHead := a.sanitizeCommitMessage(badHead, "fixed the nil check", "PR", "36518")

	if newHead == badHead {
		t.Fatal("expected the malformed message to be rewritten (new HEAD), got the same HEAD back")
	}
	msg := strings.TrimSpace(pushProbeGitCmd(t, clone, "log", "-1", "--format=%B"))
	if msg != "fixed the nil check" {
		t.Fatalf("commit message = %q, want the clean summary", msg)
	}
	if !a.headPushedToRemote(newHead, "main") {
		t.Fatal("amended commit was not pushed to the remote")
	}
}

// A normal, human-written message must be left completely untouched — no
// unnecessary amend, no unnecessary push.
func TestSanitizeCommitMessage_LeavesGoodMessageAlone(t *testing.T) {
	a, clone, _ := newAutoCommitFixture(t)

	if err := os.WriteFile(filepath.Join(clone, "f.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pushProbeGitCmd(t, clone, "commit", "-qam", "fix: a completely normal commit message")
	pushProbeGitCmd(t, clone, "push", "-q", "origin", "main")
	goodHead := strings.TrimSpace(pushProbeGitCmd(t, clone, "rev-parse", "HEAD"))

	newHead := a.sanitizeCommitMessage(goodHead, "some other summary", "PR", "36518")

	if newHead != goodHead {
		t.Fatalf("a good commit message was rewritten: %q -> %q", goodHead, newHead)
	}
	msg := strings.TrimSpace(pushProbeGitCmd(t, clone, "log", "-1", "--format=%B"))
	if msg != "fix: a completely normal commit message" {
		t.Fatalf("commit message was altered: %q", msg)
	}
}
