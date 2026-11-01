package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

const toolCommentBody = config.DefaultAutomationCommentMarkers + "\nPR validation failed"

func TestDecodeJSONPages(t *testing.T) {
	type row struct {
		ID int `json:"id"`
	}
	tests := []struct {
		name    string
		out     string
		want    []int
		wantErr bool
	}{
		{"one array", `[{"id":1},{"id":2}]`, []int{1, 2}, false},
		{"one array per page, back to back", `[{"id":1},{"id":2}][{"id":3}]`, []int{1, 2, 3}, false},
		{"pages separated by newlines", "[{\"id\":1}]\n[{\"id\":2}]\n", []int{1, 2}, false},
		{"empty output", "", nil, false},
		{"empty list", "[]", nil, false},
		{"not a list", `{"message":"Not Found"}`, nil, true},
		{"second page malformed", `[{"id":1}]<html>`, nil, true},
		// Anything after the last page is an error, never silently dropped.
		{"stray closing bracket", `[{"id":1}]]`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rows []row
			err := decodeJSONPages(tc.out, &rows)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			var got []int
			for _, r := range rows {
				got = append(got, r.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// A provider hands back a list one page at a time. Reading only the first page
// meant a comment past it did not exist as far as the agent was concerned: on
// one PR a human review at position 34 of 92 was never seen (#39995).
func TestGatherIssueCommentsReadsEveryPage(t *testing.T) {
	gh := installFakeGH(t)
	comment := func(id int, body string) string {
		return fmt.Sprintf(`{"id":%d,"body":%q,"user":{"login":"u","type":"User"},"created_at":"2026-01-01T00:00:00Z"}`, id, body)
	}
	var first, second []string
	for id := 1; id <= 30; id++ {
		first = append(first, comment(id, toolCommentBody))
	}
	for id := 31; id <= 40; id++ {
		body := toolCommentBody
		if id == 34 {
			body = "Please rename this helper before merge."
		}
		second = append(second, comment(id, body))
	}
	gh.setComments(t, "["+strings.Join(first, ",")+"]["+strings.Join(second, ",")+"]")

	a := newBaseSyncAgent(t.TempDir())
	pending, _, _ := a.gatherIssueComments(context.Background(), supersededRepo, "12", nil, &prActivity{})

	if len(pending) != 40 {
		t.Fatalf("expected all 40 comments across both pages, got %d", len(pending))
	}
	if !slices.ContainsFunc(pending, func(c reviewComment) bool { return c.ID == 34 }) {
		t.Fatal("the comment on the second page was not read")
	}
	gets, _ := os.ReadFile(filepath.Join(gh.dir, "gets"))
	if !strings.Contains(string(gets), "--paginate") || !strings.Contains(string(gets), "per_page=100") {
		t.Fatalf("the listing must ask for every page, got: %s", gets)
	}
}

func TestSettledWithoutReply(t *testing.T) {
	a := &PRFollowupAgent{}
	pending := []reviewComment{
		{ID: 1, Source: "issue_comment", AuthorType: "User", Body: toolCommentBody},
		{ID: 2, Source: "issue_comment", AuthorType: "User", Body: "Please handle the nil case."},
		{ID: 3, Source: "inline", AuthorType: "User", Body: "Is this needed?"},
		{ID: 4, Source: "inline", AuthorType: "User", Body: "This leaks the handle."},
		{ID: 5, Source: "review_body", AuthorType: "Bot", Body: "## Summary of changes"},
		{ID: 6, Source: "inline", AuthorType: "User", Body: "Typo."},
		{ID: 7, Source: "inline", AuthorType: "Bot", Body: "Return the error instead."},
		{ID: 8, Source: "issue_comment", AuthorType: "User", Body: "Not for this PR."},
	}
	responses := []commentResponse{
		{CommentID: 3, Action: "wont_fix", ShouldReply: false},
		// A reply that Step 6 withheld (unverified "fixed") or failed to post.
		{CommentID: 4, Action: "fixed", ShouldReply: true, Reply: "Fixed."},
		{CommentID: 5, Action: "fixed", ShouldReply: true, Reply: "Done."},
		{CommentID: 8, ShouldReply: false},
	}
	// Comment 6 already has a landed reply, recorded by Step 6.
	recorded := []AddressedComment{{Source: "inline", CommentID: 6, Action: "fixed"}}

	tests := []struct {
		name    string
		changed bool
		want    []string
	}{
		{"nothing changed on the PR", false, []string{
			"issue_comment:1:acknowledged", // a tool's top-level comment: never replied to
			"inline:3:wont_fix",            // the agent declined to reply
			"review_body:5:acknowledged",   // "fixed" is not recorded for a run that changed nothing
			"issue_comment:8:acknowledged", // declined, no action given
			"check_run:900:acknowledged",
			"check_run:901:acknowledged",
		}},
		{"the run changed the PR", true, []string{
			"issue_comment:1:acknowledged",
			"inline:3:wont_fix",
			"review_body:5:fixed",
			"issue_comment:8:acknowledged",
			"check_run:900:fixed",
			"check_run:901:fixed",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range a.settledWithoutReply(pending, responses, recorded, []int64{900, 901}, tc.changed) {
				if c.AddressedAt.IsZero() {
					t.Errorf("%s:%d has no addressed_at", c.Source, c.CommentID)
				}
				got = append(got, fmt.Sprintf("%s:%d:%s", c.Source, c.CommentID, c.Action))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got  %v\nwant %v", got, tc.want)
			}
		})
	}

	// An answer names a comment by id alone. Two pending comments sharing an id
	// make it ambiguous, so it settles neither of them.
	clash := []reviewComment{
		{ID: 9, Source: "inline", AuthorType: "User", Body: "Is this needed?"},
		{ID: 9, Source: "issue_comment", AuthorType: "User", Body: "Please add a test."},
	}
	if got := a.settledWithoutReply(clash, []commentResponse{{CommentID: 9, Action: "wont_fix"}}, nil, nil, false); len(got) != 0 {
		t.Errorf("an ambiguous answer settled a comment: %+v", got)
	}

	// What must stay open, because a reviewer can still see it unanswered:
	// a person's comment the agent gave no answer for (2), a reply that never
	// landed (4), an AI reviewer's inline comment with no answer (7).
	for _, c := range a.settledWithoutReply(pending, responses, recorded, nil, true) {
		if c.CommentID == 2 || c.CommentID == 4 || c.CommentID == 7 {
			t.Errorf("comment %d was recorded as handled but is still unanswered on the PR", c.CommentID)
		}
	}
}

// What settledWithoutReply records has to be skipped by the next run's
// gatherers, and a recorded check run must not shadow a comment with the same
// number.
func TestSettledRecordIsReadBackByTheNextRun(t *testing.T) {
	a := &PRFollowupAgent{}
	pending := []reviewComment{{ID: 77, Source: "issue_comment", AuthorType: "User", Body: toolCommentBody}}
	keys := addressedCommentKeys(a.settledWithoutReply(pending, nil, nil, []int64{77}, false))

	if !keys[answeredCommentKey("issue_comment", 77)] || !keys[answeredCommentKey(addressedSourceCheckRun, 77)] {
		t.Fatalf("recorded items are not in the next run's skip set: %v", keys)
	}
	if keys[answeredCommentKey("inline", 77)] {
		t.Fatal("a check run or issue comment must not mark an inline comment with the same id as handled")
	}
}

func TestCIHandledCheckRunIsNotWork(t *testing.T) {
	const twoFailing = `{"check_runs":[
	{"id":1,"name":"a","conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":"https://github.com/o/r/actions/runs/1/job/11","app":{"id":1},"check_suite":{"id":1}},
	{"id":2,"name":"b","conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":"https://github.com/o/r/actions/runs/1/job/12","app":{"id":1},"check_suite":{"id":1}}]}`
	notReady := ciReply{err: fmt.Errorf("gh: Not Found (HTTP 404)")}
	handled := func(ids ...int64) map[string]bool {
		m := map[string]bool{}
		for _, id := range ids {
			m[answeredCommentKey(addressedSourceCheckRun, id)] = true
		}
		return m
	}
	tests := []struct {
		name     string
		logs     map[int64]ciReply
		handled  map[string]bool
		wantIDs  []int64
		contains []string
		absent   []string
		logCalls int
	}{
		{"nothing handled yet", map[int64]ciReply{11: {out: "ERROR a\n"}, 12: {out: "ERROR b\n"}}, nil, []int64{1, 2}, []string{"ERROR a", "ERROR b"}, nil, 2},
		{"one already handled", map[int64]ciReply{12: {out: "ERROR b\n"}}, handled(1), []int64{2}, []string{"ERROR b"}, []string{"a (job 11)"}, 1},
		{"all already handled", nil, handled(1, 2), nil, nil, nil, 0},
		// Logs that are not ready yet were not shown, so the check is not recorded.
		{"not-ready check is not recorded", map[int64]ciReply{11: notReady, 12: {out: "ERROR b\n"}}, nil, []int64{2}, []string{"logs not ready yet", "ERROR b"}, nil, 2},
		// A comment id that happens to equal a check-run id is a different thing.
		{"a handled comment with the same id", map[int64]ciReply{11: {out: "ERROR a\n"}, 12: {out: "ERROR b\n"}}, map[string]bool{answeredCommentKey("issue_comment", 1): true}, []int64{1, 2}, []string{"ERROR a"}, nil, 2},
	}
	const listing = "gh api repos/o/r/commits/" + ciSHA + "/check-runs --paginate"
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCIRunner{t: t, replies: map[string]ciReply{listing: {out: twoFailing}}}
			for id, r := range tc.logs {
				f.replies[fmt.Sprintf("gh api repos/o/r/actions/jobs/%d/logs", id)] = r
			}
			got, ids := collectCIFailures(f, gitprovider.GitProviderGitHub, "o/r", "12", ciHead{SHA: ciSHA}, tc.handled)

			if !slices.Equal(ids, tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
			if (got == "") != (len(tc.wantIDs) == 0) {
				t.Fatalf("text and ids disagree about whether there is work: %q / %v", got, ids)
			}
			// The cheap exit depends on exactly this: nothing new means no CI text.
			if nothingForPlanner("", 0, got, baseSyncResult{}) != (len(tc.wantIDs) == 0) {
				t.Errorf("nothingForPlanner disagrees with ids %v", ids)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("a handled check is still shown (%q):\n%s", unwanted, got)
				}
			}
			if logFetches := len(f.calls) - 1; logFetches != tc.logCalls {
				t.Errorf("fetched %d job logs, want %d: %v", logFetches, tc.logCalls, f.calls)
			}
		})
	}
}

// A check is reported, and so later recorded as seen, only when the planner got
// all there is to get for it.
func TestCIReportsOnlyChecksThePlannerFullySaw(t *testing.T) {
	check := func(id int64, name, details string) string {
		return fmt.Sprintf(`{"id":%d,"name":%q,"conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":%q,"app":{"id":1},"check_suite":{"id":1}}`, id, name, details)
	}
	job := func(id int64) string { return fmt.Sprintf("https://github.com/o/r/actions/runs/1/job/%d", id) }
	const listing = "gh api repos/o/r/commits/" + ciSHA + "/check-runs --paginate"
	collect := func(checks []string, logs map[int64]ciReply) (string, []int64) {
		f := &fakeCIRunner{t: t, replies: map[string]ciReply{listing: {out: `{"check_runs":[` + strings.Join(checks, ",") + `]}`}}}
		for id, r := range logs {
			f.replies[fmt.Sprintf("gh api repos/o/r/actions/jobs/%d/logs", id)] = r
		}
		return collectCIFailures(f, gitprovider.GitProviderGitHub, "o/r", "12", ciHead{SHA: ciSHA}, nil)
	}

	t.Run("logs that could not be read this time are not reported", func(t *testing.T) {
		got, ids := collect(
			[]string{check(1, "a", job(11)), check(2, "b", job(12)), check(3, "c", job(13))},
			map[int64]ciReply{11: {err: fmt.Errorf("dial tcp: timeout")}, 12: {out: "  \n"}, 13: {out: "ERROR c\n"}})
		if !slices.Equal(ids, []int64{3}) {
			t.Fatalf("ids = %v, want only the check whose logs were read; section:\n%s", ids, got)
		}
	})

	// Nothing more will ever be available for these, so showing them again on
	// every run would only repeat the same run.
	t.Run("external CI and expired logs are reported", func(t *testing.T) {
		_, ids := collect(
			[]string{check(1, "ext/ci", "https://ci.example.com/builds/12"), check(2, "b", job(12))},
			map[int64]ciReply{12: {err: fmt.Errorf("gh: Gone (HTTP 410)")}})
		if !slices.Equal(ids, []int64{2, 1}) && !slices.Equal(ids, []int64{1, 2}) {
			t.Fatalf("ids = %v, want both checks", ids)
		}
	})

	t.Run("a block dropped for size is not reported", func(t *testing.T) {
		var checks []string
		logs := map[int64]ciReply{}
		for i := int64(1); i <= ciMaxBlocks+2; i++ {
			checks = append(checks, check(i, fmt.Sprintf("job-%d", i), job(100+i)))
			logs[100+i] = ciReply{out: fmt.Sprintf("ERROR from job %d\n", i)}
		}
		got, ids := collect(checks, logs)
		if len(ids) != ciMaxBlocks {
			t.Fatalf("reported %d checks, want the %d that got a block: %v", len(ids), ciMaxBlocks, ids)
		}
		for _, id := range ids {
			if !strings.Contains(got, fmt.Sprintf("ERROR from job %d\n", id)) {
				t.Errorf("check %d was reported but its block is not in the section", id)
			}
		}
	})
}

func TestGitLabHandledPipelineIsNotWork(t *testing.T) {
	head := ciHead{SHA: "aaaaaaa1111", PipelineID: 77, PipelineStatus: "failed"}
	f := &fakeCIRunner{t: t, replies: map[string]ciReply{}}

	got, ids := collectCIFailures(f, gitprovider.GitProviderGitLab, "g/p", "5", head, nil)
	if got == "" || !slices.Equal(ids, []int64{77}) {
		t.Fatalf("a failed pipeline is work and reports its id, got %q / %v", got, ids)
	}
	handled := map[string]bool{answeredCommentKey(addressedSourceCheckRun, 77): true}
	if got, ids := collectCIFailures(f, gitprovider.GitProviderGitLab, "g/p", "5", head, handled); got != "" || ids != nil {
		t.Fatalf("a pipeline already handled must not be work again, got %q / %v", got, ids)
	}
}

func TestPRActivityCountsPushesSinceAPersonActed(t *testing.T) {
	at := func(minute int) string {
		return time.Date(2026, 1, 1, 10, minute, 0, 0, time.UTC).Format(time.RFC3339)
	}
	push := followupPushMarker + "\n### Nudgebee Automated Followup\n\nFixed lint.\n\n**Commit:** `abc`\n"
	paused := followupPausedMarker + "\n### Nudgebee Automated Followup\n\nI am stopping here."

	p := &prActivity{}
	p.note("Looks wrong, see line 40.", at(0), false) // a person
	p.note(push, at(1), false)
	p.note(push, at(2), false)
	p.note(toolCommentBody, at(3), true)                         // a tool: does not reset the count
	p.note("**Automated Followup**\n\nAddressed.", at(4), false) // our own reply
	p.note("<!-- nb-followup-notice:1 -->\n### Nudgebee Automated Followup\n\nNo change.", at(5), false)
	p.note(push, at(6), false)
	p.note("garbled time", "yesterday", false) // unreadable timestamp: ignored, not treated as a person

	if got := p.pushesSinceHuman(); got != 3 {
		t.Fatalf("pushesSinceHuman = %d, want 3", got)
	}
	if p.pausedSinceHuman() {
		t.Fatal("no pause notice has been posted yet")
	}
	p.note(paused, at(7), false)
	if !p.pausedSinceHuman() {
		t.Fatal("the pause notice for this run of pushes was not seen")
	}

	// A person commenting starts the count again, and the old notice no longer
	// stands in for a new one.
	p.note("Try the other approach.", at(8), false)
	if got := p.pushesSinceHuman(); got != 0 {
		t.Fatalf("pushesSinceHuman = %d after a person commented, want 0", got)
	}
	if p.pausedSinceHuman() {
		t.Fatal("a notice older than the person's comment must not suppress the next one")
	}
	p.note(push, at(9), false)
	if got := p.pushesSinceHuman(); got != 1 {
		t.Fatalf("pushesSinceHuman = %d, want 1", got)
	}
}

// The budget counts marked summary comments. Nothing else this agent posts may
// count as a push, and none of it may count as a person.
func TestOnlyTheMarkedPushSummaryCountsAsAPush(t *testing.T) {
	a := &PRFollowupAgent{}
	now := time.Now().UTC().Format(time.RFC3339)
	summary := a.buildSummaryComment(&PRFollowupResult{Summary: "Fixed lint.", CommitHash: "abc123"}, nil)

	p := &prActivity{}
	p.note(followupPushMarker+"\n"+summary, now, false)
	if p.pushesSinceHuman() != 1 {
		t.Fatal("a marked push summary comment was not counted as a push")
	}

	p = &prActivity{}
	p.note(summary, now, false) // an instruction run's summary carries no marker
	p.note(a.buildNonConvergenceNotice("looked", []reviewComment{{ID: 1}}), now, false)
	p.note(supersededNoticeMarker+"\n### Nudgebee Automated Followup\n\nAlready contained.", now, false)
	if p.pushesSinceHuman() != 0 || !p.lastHuman.IsZero() {
		t.Fatalf("a notice was counted as a push or as a person: %+v", p)
	}
}

func TestNoteHumanCommits(t *testing.T) {
	// The fixture's PR commit is authored by a person.
	_, clone := baseSyncFixture(t)
	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")

	p := &prActivity{}
	a.noteHumanCommits(p, "main")
	if p.lastHuman.IsZero() {
		t.Fatal("a commit by a person on the PR branch was not counted")
	}

	// The agent amending that person's commit is the agent's push, not theirs:
	// the author is unchanged, the committer is now the agent.
	supersededBotGit(t, clone, "commit", "-q", "--amend", "--no-edit")
	if author := strings.TrimSpace(baseSyncGit(t, clone, "log", "-1", "--format=%ae")); author != "t@t" {
		t.Fatalf("fixture: the amend should keep the original author, got %q", author)
	}
	p = &prActivity{}
	a.noteHumanCommits(p, "main")
	if !p.lastHuman.IsZero() {
		t.Fatalf("a commit amended by the agent was counted as a person acting: %v", p.lastHuman)
	}

	// A branch carrying only this agent's commits says nothing about a person.
	_, clone, a = supersededFixture(t)
	supersededCommit(t, clone, "f.txt", "a\nb\nC\nd\ne\nf\ng\nh\n", "bot change")
	p = &prActivity{}
	a.noteHumanCommits(p, "main")
	if !p.lastHuman.IsZero() {
		t.Fatalf("the agent's own commit was counted as a person acting: %v", p.lastHuman)
	}
}

func TestPauseForPushBudgetTellsThePROnce(t *testing.T) {
	gh := installFakeGH(t)
	a := newBaseSyncAgent(t.TempDir())
	pushedAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	activity := &prActivity{}
	for i := range followupPushBudget {
		activity.pushes = append(activity.pushes, pushedAt.Add(time.Duration(i)*time.Minute))
	}

	result := a.pauseForPushBudget(supersededRepo, "12", "PR", followupPushBudget, activity, baseSyncResult{})

	if !result.Success || !result.NoOp || !result.Unresolved {
		t.Fatalf("a paused run is a no-op that still had work, got %+v", result)
	}
	if len(result.AddressedComments) != 0 {
		t.Fatal("a paused run looked at nothing and must record nothing as handled")
	}
	posted := gh.posts()
	if strings.Count(posted, followupPausedMarker) != 1 || !strings.Contains(posted, "Comment on this PR") {
		t.Fatalf("expected one pause notice telling the reader how to resume, got:\n%s", posted)
	}

	// The next run reads that notice back from the PR and stays quiet.
	activity.paused = append(activity.paused, pushedAt.Add(time.Hour))
	a.pauseForPushBudget(supersededRepo, "12", "PR", followupPushBudget, activity, baseSyncResult{})
	if got := strings.Count(gh.posts(), followupPausedMarker); got != 1 {
		t.Fatalf("the pause notice was posted %d times, want 1", got)
	}
}

// A run that pauses with a conflicted base merge open must not leave the
// workspace half-merged.
func TestPauseForPushBudgetAbortsAnOpenMerge(t *testing.T) {
	installFakeGH(t)
	origin, clone := baseSyncFixture(t)
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")
	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")
	sync := a.syncWithBase("main", "feature")
	if !sync.conflicted {
		t.Fatalf("fixture did not conflict: %+v", sync)
	}

	a.pauseForPushBudget(supersededRepo, "12", "PR", followupPushBudget, &prActivity{paused: []time.Time{time.Now()}}, sync)

	if a.mergeInProgress() {
		t.Fatal("the base merge was left open")
	}
}
