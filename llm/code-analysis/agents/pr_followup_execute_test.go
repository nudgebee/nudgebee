package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/planners"
)

// These tests run Execute end to end against a real git fixture, with `gh`
// replaced by a script and the model replaced by fakePlanner. They pin how the
// pieces are wired together, which the helper-level tests cannot: the order of
// the base merge, its undo and the planner; when a run's input is recorded as
// handled; and when the push budget stops a run.

// fakePlanner stands in for the ReAct planner: run is "the model's tool
// calls", status and submit are what the planner reports afterwards.
type fakePlanner struct {
	run    func()
	status string
	submit any
	calls  *int
}

func (f *fakePlanner) Plan(context.Context, string, string) (*planners.PlannerResult, error) {
	*f.calls++
	if f.run != nil {
		f.run()
	}
	return &planners.PlannerResult{Status: f.status}, nil
}

func (f *fakePlanner) GetSubmitAnalysisData() any { return f.submit }

// routedGH installs a `gh` on PATH that answers each call Execute makes from a
// file named after what is being asked for; a missing file means "none".
// POSTs are appended to the same `posts` file fakeGH reads back.
func routedGH(t *testing.T) *fakeGH {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d="$FAKE_GH_DIR"
echo "$*" >> "$d/calls"
case "$*" in
*"--method POST"*) cat >> "$d/posts";;
*"--jq .headRefName"*) echo feature;;
*"--jq .baseRefName"*) echo main;;
*"--jq .headRefOid"*) cat "$d/head_sha" 2>/dev/null;;
*"pr diff"*) ;;
*"pr view"*) echo '{}';;
*graphql*) echo '{}';;
*"/check-runs"*) cat "$d/checks" 2>/dev/null || echo '{"check_runs":[]}';;
*"/logs"*) echo "ERROR boom";;
*"/issues/"*"/comments"*) cat "$d/issue_comments" 2>/dev/null || echo '[]';;
*"/pulls/"*"/comments"*) echo '[]';;
*"/reviews"*) echo '[]';;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GH_DIR", dir)
	return &fakeGH{dir: dir}
}

func (g *fakeGH) set(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func ghIssueComment(id int, body string, at time.Time) string {
	return fmt.Sprintf(`{"id":%d,"body":%q,"user":{"login":"u","type":"User"},"created_at":%q}`, id, body, at.UTC().Format(time.RFC3339))
}

var executeRequest = PRFollowupRequest{
	RepoURL:  "https://github.com/o/r",
	Branch:   "feature",
	PRNumber: 12,
	PRURL:    "https://github.com/o/r/pull/12",
	Provider: "github",
}

// executeAgent returns an agent on clone whose planner is planner, and a
// counter of how many times the planner ran.
func executeAgent(clone string, planner *fakePlanner) (*PRFollowupAgent, *int) {
	calls := 0
	planner.calls = &calls
	a := newBaseSyncAgent(clone)
	a.newPlanner = func() followupPlanner { return planner }
	return a, &calls
}

// The headline invariant of the base merge: it happens in the workspace, and
// neither the planner nor the remote ever sees it. Before #39995 the merge was
// pushed on every run, and on a repository that allows one commit per PR the
// next run squashed it away again — every cron tick, for as long as the PR
// stayed open.
func TestExecutePlannerWorksOnThePRHeadNotTheBaseMerge(t *testing.T) {
	gh := routedGH(t)
	origin, clone := baseSyncFixture(t)
	advanceBase(t, origin, "untouched.txt", "stable\nplus base work\n", "base moves on")
	gh.set(t, "issue_comments", "["+ghIssueComment(601, "Please reword the line in feature.txt.", time.Now())+"]")
	prHead := headOf(t, clone)

	var sawHead, sawStatus string
	planner := &fakePlanner{status: "completed", submit: map[string]any{"execution_status": "success", "execution_summary": "Reworded the line."}}
	planner.run = func() {
		sawHead = headOf(t, clone)
		sawStatus = strings.TrimSpace(baseSyncGit(t, clone, "status", "--porcelain"))
		baseSyncWrite(t, clone, "feature.txt", "pr work, reworded\n")
	}
	a, calls := executeAgent(clone, planner)

	result, err := a.Execute(context.Background(), executeRequest)
	if err != nil {
		t.Fatal(err)
	}

	if *calls != 1 {
		t.Fatalf("the planner ran %d times, want 1", *calls)
	}
	if sawHead != prHead || sawStatus != "" {
		t.Fatalf("the planner was handed HEAD %s (status %q), want the PR's own head %s and a clean tree", sawHead, sawStatus, prHead)
	}
	if result.CommitHash == "" {
		t.Fatalf("expected the planner's edit to be committed and pushed, got %+v", result)
	}
	if result.BaseSynced {
		t.Fatal("no base merge reached the PR, so the run must not report one")
	}
	// What reached the remote is the PR's own history plus one commit: no merge
	// commit, and none of base's content.
	if merges := strings.TrimSpace(baseSyncGit(t, origin, "rev-list", "--merges", "main..feature")); merges != "" {
		t.Fatalf("a merge commit was pushed to the PR branch: %s", merges)
	}
	if parent := strings.TrimSpace(baseSyncGit(t, origin, "rev-parse", "feature^")); parent != prHead {
		t.Fatalf("the pushed commit sits on %s, want the PR head %s", parent, prHead)
	}
	if got := baseSyncGit(t, origin, "show", "feature:untouched.txt"); strings.Contains(got, "plus base work") {
		t.Fatal("base content was pushed to the PR branch")
	}
	// A regular run's push is what the push budget counts.
	if !strings.Contains(gh.posts(), followupPushMarker) {
		t.Fatalf("the push summary carries no push marker:\n%s", gh.posts())
	}
}

// A PR whose change is already in base is still reported, though the branch is
// behind and the merge that proves it is never pushed.
func TestExecuteReportsASupersededPRThatIsBehindBase(t *testing.T) {
	gh := routedGH(t)
	origin, clone, _ := supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "c1")
	advanceBase(t, origin, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "the same change lands on base")
	remoteBefore := strings.TrimSpace(baseSyncGit(t, origin, "rev-parse", "feature"))

	a, calls := executeAgent(clone, &fakePlanner{status: "completed"})
	result, err := a.Execute(context.Background(), executeRequest)
	if err != nil {
		t.Fatal(err)
	}

	if !result.Superseded || !strings.Contains(gh.posts(), supersededNoticeMarker) {
		t.Fatalf("a PR already contained in base was not reported: %+v\nposts:\n%s", result, gh.posts())
	}
	if *calls != 0 {
		t.Fatal("the planner ran on a PR with nothing left to do")
	}
	if after := strings.TrimSpace(baseSyncGit(t, origin, "rev-parse", "feature")); after != remoteBefore {
		t.Fatalf("the remote PR branch moved from %s to %s", remoteBefore, after)
	}
}

// The behaviour #39995 asks for: once a run has looked at a PR's feedback and
// settled it, the next run on the same feedback does not use the model again.
func TestExecuteDoesNotRedoInputAnEarlierRunSettled(t *testing.T) {
	const check = `{"check_runs":[{"id":900,"name":"lint","conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":"https://github.com/o/r/actions/runs/1/job/9001","app":{"id":1},"check_suite":{"id":1}}]}`
	// More files than the auto-commit guard accepts, so Execute discards them:
	// the planner did something, and none of it reached the PR.
	strayFiles := func(clone string) func() {
		return func() {
			for i := range autoCommitMaxFiles + 5 {
				baseSyncWrite(t, clone, fmt.Sprintf("stray_%02d.txt", i), "x\n")
			}
		}
	}
	tests := []struct {
		name        string
		status      string
		submit      any
		run         func(clone string) func()
		wantSettled bool
	}{
		{"the planner concluded nothing needs changing", "completed", map[string]any{"execution_status": "no_op", "execution_summary": "Already in place."}, nil, true},
		{"the planner ran out of steps", "max_iterations", nil, nil, false},
		{"the planner reported failure", "completed", map[string]any{"execution_status": "failed", "execution_summary": "Could not build."}, nil, false},
		{"the planner's edits never reached the PR", "completed", map[string]any{"execution_status": "success", "execution_summary": "Fixed."}, strayFiles, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gh := routedGH(t)
			_, clone := baseSyncFixture(t)
			gh.set(t, "head_sha", headOf(t, clone))
			gh.set(t, "checks", check)
			// A labeler-style comment: actionable input that never gets a reply.
			gh.set(t, "issue_comments", "["+ghIssueComment(501, toolCommentBody, time.Now())+"]")

			planner := &fakePlanner{status: tc.status, submit: tc.submit}
			if tc.run != nil {
				planner.run = tc.run(clone)
			}
			a, calls := executeAgent(clone, planner)

			first, err := a.Execute(context.Background(), executeRequest)
			if err != nil {
				t.Fatal(err)
			}
			if *calls != 1 || !first.NoOp || !first.Unresolved {
				t.Fatalf("first run: planner ran %d times, result %+v; want one planner run that changed nothing", *calls, first)
			}
			keys := addressedCommentKeys(first.AddressedComments)
			settled := keys[answeredCommentKey("issue_comment", 501)] && keys[answeredCommentKey(addressedSourceCheckRun, 900)]
			if settled != tc.wantSettled {
				t.Fatalf("recorded as handled = %v, want %v (%+v)", settled, tc.wantSettled, first.AddressedComments)
			}
			if !tc.wantSettled && len(first.AddressedComments) != 0 {
				t.Fatalf("a run that settled nothing recorded %+v", first.AddressedComments)
			}

			// The next run gets the record back, exactly as api-server returns it.
			next := executeRequest
			next.AddressedComments = first.AddressedComments
			second, err := a.Execute(context.Background(), next)
			if err != nil {
				t.Fatal(err)
			}
			if ranAgain := *calls == 2; ranAgain == tc.wantSettled {
				t.Fatalf("second run: planner ran again = %v, want %v", ranAgain, !tc.wantSettled)
			}
			if tc.wantSettled && (!second.NoOp || second.Unresolved) {
				t.Fatalf("second run should be the plain nothing-to-do exit, got %+v", second)
			}
		})
	}
}

// Five pushes with nobody acting on the PR in between, and the run stops before
// the model. An instruction is an explicit request and is never held back.
func TestExecutePausesOnceThePushBudgetIsSpent(t *testing.T) {
	gh := routedGH(t)
	_, clone := baseSyncFixture(t)
	// Later than the fixture's own commit, which a person made.
	later := time.Now().Add(time.Hour)
	comments := []string{ghIssueComment(700, toolCommentBody, later)}
	for i := range followupPushBudget {
		comments = append(comments, ghIssueComment(800+i, followupPushMarker+"\n### Nudgebee Automated Followup\n\nFixed lint.", later.Add(time.Duration(i+1)*time.Minute)))
	}
	gh.set(t, "issue_comments", "["+strings.Join(comments, ",")+"]")

	planner := &fakePlanner{status: "completed", submit: map[string]any{"execution_status": "success", "execution_summary": "Set the value."}}
	a, calls := executeAgent(clone, planner)

	result, err := a.Execute(context.Background(), executeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Fatal("the planner ran although the push budget was spent")
	}
	if !result.NoOp || !result.Unresolved || !strings.Contains(result.Summary, "Paused") {
		t.Fatalf("expected a paused run, got %+v", result)
	}
	if got := strings.Count(gh.posts(), followupPausedMarker); got != 1 {
		t.Fatalf("expected one pause notice, got %d:\n%s", got, gh.posts())
	}

	// One push short of the budget, the same PR is still worked on.
	gh.set(t, "issue_comments", "["+strings.Join(comments[:followupPushBudget], ",")+"]")
	if _, err := a.Execute(context.Background(), executeRequest); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("with budget left the planner ran %d times, want 1", *calls)
	}

	// An instruction runs regardless, and its push does not count toward the
	// budget: the summary it posts carries no push marker.
	gh.set(t, "issue_comments", "["+strings.Join(comments, ",")+"]")
	planner.run = func() { baseSyncWrite(t, clone, "feature.txt", "pr work\ncpu: 200m\n") }
	instructed := executeRequest
	instructed.Instruction = "set cpu to 200m"
	postsBefore := gh.posts()
	result, err = a.Execute(context.Background(), instructed)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 || result.CommitHash == "" {
		t.Fatalf("an instruction must run and push whatever the budget says: planner runs %d, result %+v", *calls, result)
	}
	posted := strings.TrimPrefix(gh.posts(), postsBefore)
	if !strings.Contains(posted, "Automated Followup") || strings.Contains(posted, followupPushMarker) {
		t.Fatalf("an instruction's push summary must be posted without the push marker, got:\n%s", posted)
	}
}
