package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/internal/gitprovider"
)

func supersededBotGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := supersededBotGitAllowFail(t, dir, args...); err != nil {
		t.Fatalf("bot git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func supersededBotGitAllowFail(t *testing.T, dir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=nudgebee-bot", "GIT_AUTHOR_EMAIL=bot@nudgebee.com",
		"GIT_COMMITTER_NAME=nudgebee-bot", "GIT_COMMITTER_EMAIL=bot@nudgebee.com")
	return cmd.CombinedOutput()
}

// supersededFixture uses the base-sync fixture, then gives the base the two
// files used by the regression matrix before creating bot-authored PR commits.
func supersededFixture(t *testing.T) (string, string, *PRFollowupAgent) {
	t.Helper()
	origin, clone := baseSyncFixture(t)
	advanceBase(t, origin, "f.txt", "a\nb\nc\nd\ne\nf\ng\nh\n", "add f")
	advanceBase(t, origin, "o.txt", "1\n2\n3\n4\n5\n6\n", "add o")
	baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
	baseSyncGit(t, clone, "reset", "--hard", "origin/main")
	return origin, clone, newBaseSyncAgent(clone)
}

func supersededCommit(t *testing.T, clone, path, content, message string) {
	t.Helper()
	baseSyncWrite(t, clone, path, content)
	supersededBotGit(t, clone, "add", path)
	supersededBotGit(t, clone, "commit", "-qm", message)
}

func supersededMergeBase(t *testing.T, clone string) {
	t.Helper()
	baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
	supersededBotGit(t, clone, "merge", "-q", "origin/main", "-m", "merge base")
}

// This ports the six cases from the reference experiment. In particular case
// two fails if assessContained is replaced with a single net-patch comparison.
func TestAssessContainedRegressionMatrix(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, origin, clone string)
		want  bool
	}{
		{"contained", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "up")
			supersededMergeBase(t, clone)
		}, true},
		{"dropped_by_conflict_then_later_commit", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			advanceBase(t, origin, "f.txt", "a\nb\nZ\nd\ne\nf\ng\nh\n", "up")
			baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
			if _, err := supersededBotGitAllowFail(t, clone, "merge", "origin/main", "-m", "merge"); err == nil {
				t.Fatal("expected merge conflict")
			}
			baseSyncGit(t, clone, "checkout", "--theirs", "f.txt")
			baseSyncGit(t, clone, "add", "f.txt")
			supersededBotGit(t, clone, "commit", "-qm", "resolve")
			supersededCommit(t, clone, "o.txt", "1\n2\nTHREE\n4\n5\n6\n", "c2")
			advanceBase(t, origin, "o.txt", "1\n2\nTHREE\n4\n5\n6\n", "up2")
			supersededMergeBase(t, clone)
		}, false},
		{"dropped_without_later_commit", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			advanceBase(t, origin, "f.txt", "a\nb\nZ\nd\ne\nf\ng\nh\n", "up")
			baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
			if _, err := supersededBotGitAllowFail(t, clone, "merge", "origin/main", "-m", "merge"); err == nil {
				t.Fatal("expected merge conflict")
			}
			baseSyncGit(t, clone, "checkout", "--theirs", "f.txt")
			baseSyncGit(t, clone, "add", "f.txt")
			supersededBotGit(t, clone, "commit", "-qm", "resolve")
		}, false},
		{"chained_contained", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			supersededCommit(t, clone, "f.txt", "a\nb\nCC\nD\ne\nf\ng\nh\n", "c2")
			advanceBase(t, origin, "f.txt", "a\nb\nCC\nD\ne\nf\ng\nh\n", "up")
			supersededMergeBase(t, clone)
		}, true},
		{"merge_between_c2_not_in_base", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\nG\nh\n", "up")
			supersededMergeBase(t, clone)
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\nE\nf\nG\nh\n", "c2")
		}, false},
		{"merge_between_both_contained", func(t *testing.T, origin, clone string) {
			supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
			advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\nG\nh\n", "up")
			supersededMergeBase(t, clone)
			supersededCommit(t, clone, "f.txt", "A\nb\nC\nd\ne\nf\nG\nh\n", "c2")
			advanceBase(t, origin, "f.txt", "A\nb\nC\nd\ne\nf\nG\nh\n", "up2")
			supersededMergeBase(t, clone)
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, clone, a := supersededFixture(t)
			tt.setup(t, origin, clone)
			got, _, _ := a.assessContained(context.Background(), "main")
			if got != tt.want {
				t.Fatalf("assessContained() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeGH installs a `gh` on PATH. GET calls print the comments file; POST
// calls append their stdin to the posts file. Either can be made to fail.
type fakeGH struct{ dir string }

func installFakeGH(t *testing.T) *fakeGH {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"--method POST"*)
  if [ -n "$FAKE_GH_POST_FAIL" ]; then exit 1; fi
  cat >> "$FAKE_GH_DIR/posts"
  exit 0;;
*)
  echo "$*" >> "$FAKE_GH_DIR/gets"
  if [ -n "$FAKE_GH_GET_FAIL" ]; then exit 1; fi
  cat "$FAKE_GH_DIR/comments" 2>/dev/null
  exit 0;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GH_DIR", dir)
	t.Setenv("FAKE_GH_POST_FAIL", "")
	t.Setenv("FAKE_GH_GET_FAIL", "")
	return &fakeGH{dir: dir}
}

func (g *fakeGH) setComments(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, "comments"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// posts returns the decoded bodies of every comment posted so far.
func (g *fakeGH) posts() string {
	b, _ := os.ReadFile(filepath.Join(g.dir, "posts"))
	dec := json.NewDecoder(bytes.NewReader(b))
	var out strings.Builder
	for {
		var m map[string]string
		if err := dec.Decode(&m); err != nil {
			break
		}
		out.WriteString(m["body"])
		out.WriteString("\n---\n")
	}
	return out.String()
}

var supersededRepo = &gitprovider.RepoInfo{FullPath: "o/r"}

// containedScenario leaves a clone whose head tree equals base and whose only
// own commit is bot-authored: the shape that qualifies for retirement.
func containedScenario(t *testing.T) (origin, clone string, a *PRFollowupAgent, gh *fakeGH) {
	t.Helper()
	gh = installFakeGH(t)
	origin, clone, a = supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
	advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "up")
	supersededMergeBase(t, clone)
	return
}

func headOf(t *testing.T, clone string) string {
	return strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD"))
}

func TestShouldAssessSuperseded(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		pending     int
		sync        baseSyncResult
		want        bool
	}{
		{"idle run", "", 0, baseSyncResult{}, true},
		{"clean merge sync", "", 0, baseSyncResult{preMergeHead: "abc123"}, true},
		{"conflicted sync", "", 0, baseSyncResult{conflicted: true}, false},
		{"pending comments", "", 2, baseSyncResult{}, false},
		{"instruction", "do a thing", 0, baseSyncResult{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAssessSuperseded(tt.instruction, tt.pending, tt.sync); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetireIfSupersededContainedPostsMarkerWithoutPromisingClosure(t *testing.T) {
	_, clone, a, gh := containedScenario(t)
	res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main")
	if res == nil || !res.Superseded || !res.Success || !res.NoOp {
		t.Fatalf("expected a superseded no-op success, got %+v", res)
	}
	if res.AssessedHeadSHA != headOf(t, clone) {
		t.Fatalf("AssessedHeadSHA = %q, want %q", res.AssessedHeadSHA, headOf(t, clone))
	}
	posts := gh.posts()
	if !strings.Contains(posts, supersededNoticeMarker) {
		t.Fatalf("marker comment not posted: %q", posts)
	}
	if strings.Contains(strings.ToLower(posts), "clos") {
		t.Fatalf("comment must not promise closure: %q", posts)
	}
}

func TestRetireIfSupersededMarkerPresentDoesNotRepostButStillReports(t *testing.T) {
	_, clone, a, gh := containedScenario(t)
	gh.setComments(t, `[{"body":"`+supersededNoticeMarker+` earlier"}]`)
	res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main")
	if res == nil || !res.Superseded || res.AssessedHeadSHA != headOf(t, clone) {
		t.Fatalf("expected Superseded to be reported again, got %+v", res)
	}
	if p := gh.posts(); p != "" {
		t.Fatalf("must not repost, got %q", p)
	}
}

func TestHasSupersededNoticeSeesEscapedMarker(t *testing.T) {
	gh := installFakeGH(t)
	a := newBaseSyncAgent(t.TempDir())
	gh.setComments(t, `[{"body":"\u003c!-- nudgebee-superseded --\u003e"}]`)
	if found, known := a.hasSupersededNotice(supersededRepo, "7", supersededNoticeMarker); !found || !known {
		t.Fatal("escaped marker not detected")
	}
}

func TestRetireIfSupersededLookupFailureGivesNoVerdictAndNoPost(t *testing.T) {
	_, _, a, gh := containedScenario(t)
	t.Setenv("FAKE_GH_GET_FAIL", "1")
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("unknown comment state must not yield a verdict, got %+v", res)
	}
	if p := gh.posts(); p != "" {
		t.Fatalf("lookup failure must not post, got %q", p)
	}
}

func TestRetireIfSupersededReviewNoteLookupFailureDoesNotPost(t *testing.T) {
	gh := installFakeGH(t)
	origin, clone, a := supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
	advanceBase(t, origin, "f.txt", "a\nb\nZ\nd\ne\nf\ng\nh\n", "up")
	baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
	_, _ = supersededBotGitAllowFail(t, clone, "merge", "origin/main", "-m", "merge")
	baseSyncGit(t, clone, "checkout", "--theirs", "f.txt")
	baseSyncGit(t, clone, "add", "f.txt")
	supersededBotGit(t, clone, "commit", "-qm", "resolve")
	t.Setenv("FAKE_GH_GET_FAIL", "1")
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("got %+v", res)
	}
	if p := gh.posts(); p != "" {
		t.Fatalf("lookup failure must not post the review note, got %q", p)
	}
}

// gh --paginate prints the pages back to back; a marker on a later page must
// be found, and the lookup must actually ask for pagination.
func TestHasSupersededNoticeFindsMarkerOnLaterPage(t *testing.T) {
	gh := installFakeGH(t)
	a := newBaseSyncAgent(t.TempDir())
	gh.setComments(t, `[{"body":"page one"}][{"body":"`+supersededNoticeMarker+`"}]`)
	found, known := a.hasSupersededNotice(supersededRepo, "7", supersededNoticeMarker)
	if !found || !known {
		t.Fatalf("found=%v known=%v", found, known)
	}
	gets, _ := os.ReadFile(filepath.Join(gh.dir, "gets"))
	if !strings.Contains(string(gets), "--paginate") {
		t.Fatalf("lookup must paginate, ran: %q", gets)
	}
}

func TestAssessContainedEmptyCommits(t *testing.T) {
	t.Run("empty plus contained", func(t *testing.T) {
		installFakeGH(t)
		origin, clone, a := supersededFixture(t)
		supersededBotGit(t, clone, "commit", "-q", "--allow-empty", "-m", "empty")
		supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
		advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "up")
		supersededMergeBase(t, clone)
		if got, _, _ := a.assessContained(context.Background(), "main"); !got {
			t.Fatal("expected contained")
		}
	})
	t.Run("only empty", func(t *testing.T) {
		installFakeGH(t)
		_, clone, a := supersededFixture(t)
		supersededBotGit(t, clone, "commit", "-q", "--allow-empty", "-m", "empty")
		got, _, absorbed := a.assessContained(context.Background(), "main")
		if got || !absorbed {
			t.Fatalf("contained=%v absorbed=%v, want false/true", got, absorbed)
		}
	})
}

func TestRetireIfSupersededCommentFailureReportsNothing(t *testing.T) {
	_, _, a, _ := containedScenario(t)
	t.Setenv("FAKE_GH_POST_FAIL", "1")
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("a failed marker comment must not report Superseded, got %+v", res)
	}
}

func TestRetireIfSupersededStaleFetchReportsNothing(t *testing.T) {
	_, clone, a, gh := containedScenario(t)
	baseSyncGit(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("stale/failed fetch must not retire, got %+v", res)
	}
	if p := gh.posts(); p != "" {
		t.Fatalf("no comment expected on a failed fetch, got %q", p)
	}
}

func TestRetireIfSupersededDirtyWorktreeReportsNothing(t *testing.T) {
	for name, dirty := range map[string]func(t *testing.T, clone string){
		"untracked": func(t *testing.T, clone string) { baseSyncWrite(t, clone, "stray.txt", "x\n") },
		"modified":  func(t *testing.T, clone string) { baseSyncWrite(t, clone, "f.txt", "changed\n") },
	} {
		t.Run(name, func(t *testing.T) {
			_, clone, a, gh := containedScenario(t)
			dirty(t, clone)
			if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
				t.Fatalf("dirty worktree must not retire, got %+v", res)
			}
			if p := gh.posts(); p != "" {
				t.Fatalf("dirty worktree is transient, no note expected, got %q", p)
			}
		})
	}
}

func TestRetireIfSupersededHumanCommitIsNotRetired(t *testing.T) {
	gh := installFakeGH(t)
	origin, clone, a := supersededFixture(t)
	// baseSyncGit authors as t@t, i.e. not the bot.
	baseSyncWrite(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n")
	baseSyncGit(t, clone, "commit", "-qam", "human edit")
	advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "up")
	supersededMergeBase(t, clone)
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("human-authored commit must not retire, got %+v", res)
	}
	if !strings.Contains(gh.posts(), supersededReviewNoticeMarker) {
		t.Fatalf("expected the human review note, got %q", gh.posts())
	}
}

func TestAssessContainedCommitCountLimit(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want bool
	}{{20, true}, {21, false}} {
		t.Run(fmt.Sprintf("%d_commits", tt.n), func(t *testing.T) {
			installFakeGH(t)
			origin, clone, a := supersededFixture(t)
			for i := 1; i <= tt.n; i++ {
				supersededCommit(t, clone, "g.txt", fmt.Sprintf("%d\n", i), fmt.Sprintf("c%d", i))
			}
			advanceBase(t, origin, "g.txt", fmt.Sprintf("%d\n", tt.n), "up")
			supersededMergeBase(t, clone)
			got, _, _ := a.assessContained(context.Background(), "main")
			if got != tt.want {
				t.Fatalf("assessContained() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The scratch index must never leak into the real one or the worktree.
func TestAssessContainedLeavesRealIndexAndWorktreeUntouched(t *testing.T) {
	_, clone, a, _ := containedScenario(t)
	before := baseSyncGit(t, clone, "status", "--porcelain", "-uall") + baseSyncGit(t, clone, "diff", "--cached")
	if got, _, _ := a.assessContained(context.Background(), "main"); !got {
		t.Fatal("expected contained")
	}
	after := baseSyncGit(t, clone, "status", "--porcelain", "-uall") + baseSyncGit(t, clone, "diff", "--cached")
	if before != after || after != "" {
		t.Fatalf("real index/worktree changed: %q -> %q", before, after)
	}
}

// Absorbed-but-unprovable (the LLM merge dropped the change) gets one deduped
// human note and the PR is left open.
func TestRetireIfSupersededNotContainedPostsDedupedReviewNote(t *testing.T) {
	gh := installFakeGH(t)
	origin, clone, a := supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
	advanceBase(t, origin, "f.txt", "a\nb\nZ\nd\ne\nf\ng\nh\n", "up")
	baseSyncGit(t, clone, "fetch", "-q", "origin", "main")
	if _, err := supersededBotGitAllowFail(t, clone, "merge", "origin/main", "-m", "merge"); err == nil {
		t.Fatal("expected merge conflict")
	}
	baseSyncGit(t, clone, "checkout", "--theirs", "f.txt")
	baseSyncGit(t, clone, "add", "f.txt")
	supersededBotGit(t, clone, "commit", "-qm", "resolve")

	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("dropped change must not be reported superseded, got %+v", res)
	}
	posts := gh.posts()
	if !strings.Contains(posts, supersededReviewNoticeMarker) || strings.Contains(posts, supersededNoticeMarker) {
		t.Fatalf("expected only the review note, got %q", posts)
	}

	gh.setComments(t, `[{"body":"`+supersededReviewNoticeMarker+`"}]`)
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("got %+v", res)
	}
	if gh.posts() != posts {
		t.Fatalf("review note must be deduped, got %q", gh.posts())
	}
}

// An ordinary PR that still differs from base is not "absorbed": no note, no
// report. Otherwise every idle PR would be told it was checked.
func TestRetireIfSupersededDivergedPRStaysSilent(t *testing.T) {
	gh := installFakeGH(t)
	_, clone, a := supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
	if res := a.retireIfSuperseded(context.Background(), supersededRepo, "7", "main"); res != nil {
		t.Fatalf("got %+v", res)
	}
	if p := gh.posts(); p != "" {
		t.Fatalf("expected silence, got %q", p)
	}
}
