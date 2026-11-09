package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/internal/git"
)

const (
	deployedSHA = "d16bfe05a744909de4b27f5875fe0d4ed41ce607"
	otherSHA    = "4233af136c6786943072cce32e2371cac4e50d15"
)

// Which commit a call ends up on. In a run that writes a code change the
// request decides, and a commit the model passes on its own is reported back
// as unused; everywhere else the model's commit wins, as it always has.
func TestRepoCloneTool_ResolveCommit(t *testing.T) {
	tests := []struct {
		name        string
		requestOnly bool
		request     string
		asked       string
		wantCommit  string
		wantIgnored string
	}{
		{name: "analysis: model's commit wins over none", asked: otherSHA, wantCommit: otherSHA},
		{name: "analysis: model's commit wins over the request's", request: deployedSHA, asked: otherSHA, wantCommit: otherSHA},
		{name: "analysis: omitted falls back to the request's", request: deployedSHA, wantCommit: deployedSHA},
		{name: "analysis: nothing asked, nothing pinned"},

		{name: "change at the tip: model's commit is not used", requestOnly: true, asked: deployedSHA, wantIgnored: deployedSHA},
		{name: "change at the tip: nothing asked stays at the tip", requestOnly: true},
		{name: "change on a pinned commit: a different commit is not used", requestOnly: true, request: deployedSHA, asked: otherSHA, wantCommit: deployedSHA, wantIgnored: otherSHA},
		{name: "change on a pinned commit: the same commit is fine", requestOnly: true, request: deployedSHA, asked: deployedSHA, wantCommit: deployedSHA},
		{name: "change on a pinned commit: its abbreviation is the same commit", requestOnly: true, request: deployedSHA, asked: deployedSHA[:8], wantCommit: deployedSHA},
		{name: "change on a pinned commit: omitted falls back to it", requestOnly: true, request: deployedSHA, wantCommit: deployedSHA},
		{name: "change on a pinned commit: a prefix too short to name a commit is not the same commit", requestOnly: true, request: deployedSHA, asked: deployedSHA[:4], wantCommit: deployedSHA, wantIgnored: deployedSHA[:4]},
		{name: "change at the tip: a ref name is not used either", requestOnly: true, asked: "HEAD", wantIgnored: "HEAD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &RepoCloneTool{}
			tool.SetDefaultCommit(tt.request)
			tool.SetCommitFromRequestOnly(tt.requestOnly)

			commit, ignored := tool.resolveCommit(tt.asked)
			if commit != tt.wantCommit || ignored != tt.wantIgnored {
				t.Fatalf("resolveCommit(%q) = (%q, %q), want (%q, %q)", tt.asked, commit, ignored, tt.wantCommit, tt.wantIgnored)
			}
		})
	}
}

// The model has to be told, in the text it reads, that it is not on the commit
// it asked for — otherwise it reasons about one revision while editing another.
func TestIgnoredCommitNote(t *testing.T) {
	atTip := ignoredCommitNote(deployedSHA, otherSHA, false)
	for _, want := range []string{deployedSHA, otherSHA, "was not used", "the branch tip"} {
		if !strings.Contains(atTip, want) {
			t.Errorf("note should mention %q, got: %s", want, atTip)
		}
	}
	if pinned := ignoredCommitNote(otherSHA, deployedSHA, true); !strings.Contains(pinned, "pinned") {
		t.Errorf("note for a request-pinned run should say so, got: %s", pinned)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The whole path, on a real repository: a run that writes a change, a model
// that passes the deployed commit anyway, and a remote that has moved on.
func TestRepoCloneTool_Execute_ChangeRunLandsOnTheTipDespiteModelCommit(t *testing.T) {
	origin := t.TempDir()
	gitIn(t, origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("deployed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-qm", "deployed")
	deployed := gitIn(t, origin, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(origin, "f.txt"), []byte("newer\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "commit", "-qam", "newer")
	tip := gitIn(t, origin, "rev-parse", "HEAD")

	workspace := t.TempDir()
	tool := NewRepoCloneTool(workspace, git.NewGitClient(workspace, 60*time.Second, 0))
	input := map[string]any{"repo_url": origin, "commit": deployed, "workspace_dir": filepath.Join(workspace, "conversation")}

	headOf := func(t *testing.T) string {
		t.Helper()
		return gitIn(t, filepath.Join(workspace, "conversation", filepath.Base(origin)), "rev-parse", "HEAD")
	}

	// An analysis may pin itself: this is the earlier, read-only run of the
	// conversation, and it leaves the worktree on the deployed commit.
	if resp := tool.Execute(context.Background(), input); resp.Status != "success" {
		t.Fatalf("analysis clone failed: %s", resp.Error)
	}
	if got := headOf(t); got != deployed {
		t.Fatalf("analysis checkout is %s, want the commit it asked for %s", got, deployed)
	}

	// The same conversation now writes a change.
	tool.SetCommitFromRequestOnly(true)
	resp := tool.Execute(context.Background(), input)
	if resp.Status != "success" {
		t.Fatalf("change-run clone failed: %s", resp.Error)
	}
	if got := headOf(t); got != tip {
		t.Fatalf("change-run checkout is %s, want the branch tip %s (the model asked for %s)", got, tip, deployed)
	}
	for _, want := range []string{"was not used", deployed, tip} {
		if !strings.Contains(resp.Observation, want) {
			t.Errorf("observation should mention %q, got: %s", want, resp.Observation)
		}
	}
}
