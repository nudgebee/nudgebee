package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/internal/gitprovider"
)

const (
	ciRepo = "nudgebee/nudgebee"
	ciSHA  = "e2e5b1963d"
)

type ciReply struct {
	out string
	err error
}

// fakeCIRunner replays canned CLI output and fails the test if the deleted
// `gh run list` path is ever used.
type fakeCIRunner struct {
	t       *testing.T
	calls   []string
	logged  []string
	replies map[string]ciReply
}

func (f *fakeCIRunner) LogError(msg string, err error) {
	f.logged = append(f.logged, msg+": "+err.Error())
}

func (f *fakeCIRunner) Run(name string, args ...string) (string, error) {
	k := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, k)
	if strings.HasPrefix(k, "gh run") {
		f.t.Fatalf("unexpected call: %s", k)
	}
	r, ok := f.replies[k]
	if !ok {
		return "", errors.New("unexpected command: " + k)
	}
	return r.out, r.err
}

func ciFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "ci", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkRunsKey() string {
	return fmt.Sprintf("gh api repos/%s/commits/%s/check-runs --paginate", ciRepo, ciSHA)
}
func jobLogsKey(id int64) string {
	return fmt.Sprintf("gh api repos/%s/actions/jobs/%d/logs", ciRepo, id)
}

func collectGitHub(t *testing.T, checkRuns string, logs map[int64]ciReply) (string, *fakeCIRunner) {
	t.Helper()
	f := &fakeCIRunner{t: t, replies: map[string]ciReply{checkRunsKey(): {out: checkRuns}}}
	for id, r := range logs {
		f.replies[jobLogsKey(id)] = r
	}
	got, _ := collectCIFailures(f, gitprovider.GitProviderGitHub, ciRepo, "12", ciHead{SHA: ciSHA}, nil)
	return got, f
}

// renderLog runs one failed job's log through parse + render.
func renderLog(log string) string {
	return renderCISection(ciRepo, []ciBlock{{name: "job", jobID: 7, lines: parseJobLog(log)}})
}

func TestCILogGoCompileError(t *testing.T) {
	got := renderLog(ciFixture(t, "go_compile_error.txt"))
	for _, want := range []string{"agents/ci_log_summary.go:42:13: undefined: renderThing", "declared and not used", "exit code 1", "step: Run go build ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCILogGoTestFailAndPanic(t *testing.T) {
	got := renderLog(ciFixture(t, "go_test_fail_panic.txt"))
	for _, want := range []string{"panic: runtime error: index out of range", "--- FAIL: TestBoom", "boom_test.go:31", "FAIL\tnudgebee/x/agents"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCILogInterleavedOutput(t *testing.T) {
	got := renderLog(ciFixture(t, "interleaved_output.txt"))
	for _, want := range []string{"[job-b] --- FAIL: TestB3", "b_test.go:88: expected 3, got 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCILogStripsANSIAndTimestamps(t *testing.T) {
	got := renderLog(ciFixture(t, "ansi_timestamp_noise.txt"))
	if strings.ContainsAny(got, "\x1b\ufeff") || strings.Contains(got, "2026-09-30T") {
		t.Errorf("noise leaked:\n%q", got)
	}
	if !strings.Contains(got, "Error: cannot find module") {
		t.Errorf("signal lost:\n%s", got)
	}
}

func TestCILogCollapsesRepeatedLines(t *testing.T) {
	log := ciFixture(t, "repeated_500.txt")
	if n := len(parseJobLog(log)); n > 12 {
		t.Errorf("500 identical lines not collapsed, %d parsed lines", n)
	}
	got := renderLog(log)
	if !strings.Contains(got, "retrying connection to db... timeout (x 500)") || !strings.Contains(got, "giving up after 500 attempts") {
		t.Errorf("got:\n%s", got)
	}
}

func TestCILogMinifiedLineReplacedWhole(t *testing.T) {
	got := renderLog(ciFixture(t, "minified_3kb_line.txt"))
	if !strings.Contains(got, "bytes omitted]") || strings.Contains(got, "xxxxxxxxxx") {
		t.Errorf("long line not replaced whole:\n%.400s", got)
	}
	if !strings.Contains(got, "unexpected token") {
		t.Errorf("signal lost:\n%s", got)
	}
}

func TestCILogWithoutMarker(t *testing.T) {
	got := renderLog(ciFixture(t, "no_marker.txt"))
	if !strings.Contains(got, "no failure marker found") || !strings.Contains(got, "step 39 ok") || strings.Contains(got, "step 0 ok") {
		t.Errorf("got:\n%s", got)
	}
}

func TestCILogRealJobLogFormat(t *testing.T) {
	log := ciFixture(t, "real_label_prs_failure.txt")
	if !strings.HasPrefix(log, "\ufeff") {
		t.Fatal("fixture must be a raw API log with BOM")
	}
	got := renderLog(log)
	for _, want := range []string{"Failed to execute please add screenshots for UI changes", "Process completed with exit code 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "AWS_SECRET") && !strings.Contains(got, "***") {
		t.Error("secret masking lost")
	}
}

func TestCILargeLogStaysWithinBudgetWithoutTruncation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("\ufeff2026-09-30T07:00:00.0000000Z ##[group]Run go test ./...\n")
	sb.WriteString("2026-09-30T07:00:01.0000000Z === RUN   TestFirst\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&sb, "2026-09-30T07:00:02.0000000Z first_failure_line_%d: FAIL detail\n", i)
	}
	sb.WriteString("2026-09-30T07:00:03.0000000Z ##[endgroup]\n")
	for i := 0; sb.Len() < 1<<20; i++ {
		fmt.Fprintf(&sb, "2026-09-30T07:01:00.0000000Z ##[group]Step %d\n", i)
		fmt.Fprintf(&sb, "2026-09-30T07:01:00.0000000Z pkg/mod%d/file%d.go:%d:7: error: later failure %d\n", i, i, i+1, i)
		fmt.Fprintf(&sb, "2026-09-30T07:01:00.0000000Z padding line number %d with unique content\n", i)
	}
	if sb.Len() < 1<<20 {
		t.Fatalf("log is only %d bytes", sb.Len())
	}
	got := renderLog(sb.String())
	if len(got) > ciSectionMaxBytes || len(got) > ciBlockMaxBytes+512 {
		t.Errorf("len %d exceeds budget", len(got))
	}
	for i := 0; i < 8; i++ {
		if want := fmt.Sprintf("first_failure_line_%d: FAIL detail", i); !strings.Contains(got, want) {
			t.Errorf("first-failure block not intact, missing %q", want)
		}
	}
	if !strings.Contains(got, "lines omitted") {
		t.Error("no omission line")
	}
	if strings.Contains(got, "[truncated]") {
		t.Error("byte truncation marker present")
	}
}

func TestCIManyBlocksAreDroppedWhole(t *testing.T) {
	var blocks []ciBlock
	for i := 0; i < 8; i++ {
		blocks = append(blocks, ciBlock{name: fmt.Sprintf("job-%d", i), jobID: int64(100 + i), lines: parseJobLog("ERROR boom\n")})
	}
	got := renderCISection(ciRepo, blocks)
	if strings.Count(got, "\n#### ") != ciMaxBlocks {
		t.Errorf("want %d blocks:\n%s", ciMaxBlocks, got)
	}
	if !strings.Contains(got, "3 blocks omitted, fetch with `gh api repos/"+ciRepo+"/actions/jobs/<id>/logs`") {
		t.Errorf("no omission note:\n%s", got)
	}
	if !strings.Contains(got, "job-7 (job 107): block omitted for size") {
		t.Errorf("omitted block missing from index:\n%s", got)
	}
}

func TestCIRenderIsDeterministic(t *testing.T) {
	cr := ciFixture(t, "real_label_prs_checkruns.json")
	logs := map[int64]ciReply{
		109778715279: {out: ciFixture(t, "real_label_prs_failure.txt")},
		109777872286: {out: ciFixture(t, "real_label_prs_failure.txt")},
	}
	first, _ := collectGitHub(t, cr, logs)
	for i := 0; i < 5; i++ {
		if again, _ := collectGitHub(t, cr, logs); again != first {
			t.Fatal("output differs between runs")
		}
	}
	if first == "" {
		t.Fatal("expected a summary")
	}
}

func TestCIRealLabelPrsSelectsBothFailedSuites(t *testing.T) {
	got, f := collectGitHub(t, ciFixture(t, "real_label_prs_checkruns.json"), map[int64]ciReply{
		109778715279: {out: ciFixture(t, "real_label_prs_failure.txt")},
		109777872286: {out: ciFixture(t, "real_label_prs_failure.txt")},
	})
	if !strings.Contains(got, "(job 109778715279)") || !strings.Contains(got, "(job 109777872286)") {
		t.Errorf("both failed suites must be selected:\n%s", got)
	}
	if !strings.Contains(got, "level=fatal") {
		t.Errorf("fatal line lost:\n%s", got)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "109779247422") {
			t.Errorf("a successful job's logs were fetched: %s", c)
		}
	}
}

func TestCITwoSuiteSameNameFailureNotHiddenBySuccess(t *testing.T) {
	const failLog = "\ufeff2026-09-22T13:49:00.0000000Z ##[group]Run make lint\n2026-09-22T13:49:01.0000000Z ##[error]lint failed\n"
	logs := map[int64]ciReply{106770130714: {out: failLog}}
	fixture := ciFixture(t, "two_suite_lint.json")

	// As captured: the failing run finished after the green one.
	got, _ := collectGitHub(t, fixture, logs)
	if !strings.Contains(got, "lint failed") {
		t.Fatalf("failure not selected:\n%s", got)
	}
	// The dangerous order from the plan: the SUCCESS finishes later. Different
	// check suites, so it must not shadow the failure.
	swapped := strings.NewReplacer("2026-09-22T13:46:58Z", "TMP", "2026-09-22T13:49:13Z", "2026-09-22T13:46:58Z", "TMP", "2026-09-22T13:49:13Z").Replace(fixture)
	if swapped == fixture {
		t.Fatal("swap did not change the fixture")
	}
	got, _ = collectGitHub(t, swapped, logs)
	if !strings.Contains(got, "lint failed") || !strings.Contains(got, "lint (1.26.x)") {
		t.Fatalf("later-finishing green run hid the failure:\n%s", got)
	}
}

func TestCISameNameRerunInOneSuiteLatestWins(t *testing.T) {
	got, _ := collectGitHub(t, ciFixture(t, "rerun_same_suite.json"), nil)
	if got != "" {
		t.Errorf("earlier failure superseded by a green re-run must be ignored:\n%s", got)
	}
	got, _ = collectGitHub(t, ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {out: "ERROR later attempt failed\n"}})
	if !strings.Contains(got, "later attempt failed") || !strings.Contains(got, "(job 2002)") {
		t.Errorf("latest failing attempt must be selected:\n%s", got)
	}
}

func TestCIPaginatedCheckRunsAreConcatenated(t *testing.T) {
	page := ciFixture(t, "rerun_same_suite_now_failing.json") + ciFixture(t, "external_ci.json")
	got, _ := collectGitHub(t, page, map[int64]ciReply{2002: {out: "ERROR boom\n"}})
	if !strings.Contains(got, "(job 2002)") || !strings.Contains(got, "buildkite/nudgebee") {
		t.Errorf("both pages must be read:\n%s", got)
	}
}

// Each row asserts the string, and the planner decision derived from it.
// Execute sets Unresolved from the same `ciFailureLogs != ""` predicate, so the
// no-op row pair (nothingForPlanner true / Unresolved false) is one invariant.
func TestCIWorkSemanticsPerState(t *testing.T) {
	gone := errors.New("gh api failed: exit status 1\nstderr: " + strings.TrimSpace(ciFixture(t, "gone_410_stderr.txt")))
	notFound := errors.New("gh api failed: exit status 1\nstderr: gh: Not Found (HTTP 404)")
	tests := []struct {
		name      string
		checkRuns string
		logs      map[int64]ciReply
		wantWork  bool
		contains  []string
	}{
		{"failed check, logs with signal", ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {out: "ERROR boom\n"}}, true, []string{"ERROR boom"}},
		{"failed check, logs without signal", ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {out: ciFixture(t, "no_marker.txt")}}, true, []string{"no failure marker found"}},
		{"HTTP 410 expired logs", ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {err: gone}}, true, []string{"UNVERIFIED: logs expired"}},
		{"empty log body", ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {out: "  \n"}}, true, []string{"UNVERIFIED: empty log output"}},
		{"fetch error", ciFixture(t, "rerun_same_suite_now_failing.json"), map[int64]ciReply{2002: {err: errors.New("dial tcp: timeout")}}, true, []string{"UNVERIFIED: log fetch failed"}},
		{"external CI", ciFixture(t, "external_ci.json"), nil, true, []string{"buildkite/nudgebee", "UNVERIFIED: external CI"}},
		{"not_ready only", ciFixture(t, "in_progress.json"), map[int64]ciReply{4001: {err: notFound}}, false, nil},
		{"no failed checks", ciFixture(t, "rerun_same_suite.json"), nil, false, nil},
		{"check-runs body not JSON", "<html>", nil, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := collectGitHub(t, tc.checkRuns, tc.logs)
			if (got != "") != tc.wantWork {
				t.Fatalf("work=%v, got %q", got != "", got)
			}
			if nothingForPlanner("", 0, got, baseSyncResult{}) == tc.wantWork {
				t.Errorf("nothingForPlanner = %v with work=%v", !tc.wantWork, tc.wantWork)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

func TestCINotReadyMixedWithWorkIsListedInIndex(t *testing.T) {
	cr := `{"check_runs":[
	{"id":1,"name":"a","conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":"https://github.com/o/r/actions/runs/1/job/11","app":{"id":1},"check_suite":{"id":1}},
	{"id":2,"name":"b","conclusion":"failure","completed_at":"2026-01-01T00:00:00Z","details_url":"https://github.com/o/r/actions/runs/1/job/12","app":{"id":1},"check_suite":{"id":1}}]}`
	got, _ := collectGitHub(t, cr, map[int64]ciReply{
		11: {err: errors.New("gh: Not Found (HTTP 404)")},
		12: {out: "ERROR real failure\n"},
	})
	if !strings.Contains(got, "a (job 11): logs not ready yet") || !strings.Contains(got, "ERROR real failure") {
		t.Errorf("got:\n%s", got)
	}
	if strings.Contains(got, "#### a") {
		t.Errorf("not-ready check must not get a block:\n%s", got)
	}
}

// A job id containing "410" must not be mistaken for an HTTP 410.
func TestCIJobIDContaining410IsNotExpired(t *testing.T) {
	cr := `{"check_runs":[{"id":4104,"name":"a","conclusion":"failure","details_url":"https://github.com/o/r/actions/runs/1/job/4104","app":{"id":1},"check_suite":{"id":1}}]}`
	got, _ := collectGitHub(t, cr, map[int64]ciReply{4104: {err: errors.New("gh api repos/nudgebee/nudgebee/actions/jobs/4104/logs failed: exit status 1\nstderr: gh: Not Found (HTTP 404)")}})
	if got != "" {
		t.Errorf("404 is not_ready (no work), got:\n%s", got)
	}
}

func TestCIInvalidHeadSHAIsRejected(t *testing.T) {
	for _, sha := range []string{"", "main", "--flag", "abc..def", "abc"} {
		f := &fakeCIRunner{t: t}
		if got, _ := collectCIFailures(f, gitprovider.GitProviderGitHub, ciRepo, "1", ciHead{SHA: sha}, nil); got != "" || len(f.calls) != 0 {
			t.Errorf("sha %q: got %q, calls %v", sha, got, f.calls)
		}
	}
}

func TestCICheckRunsErrorMeansNoWorkButIsLogged(t *testing.T) {
	f := &fakeCIRunner{t: t, replies: map[string]ciReply{checkRunsKey(): {err: errors.New("boom")}}}
	if got, _ := collectCIFailures(f, gitprovider.GitProviderGitHub, ciRepo, "1", ciHead{SHA: ciSHA}, nil); got != "" {
		t.Errorf("got %q", got)
	}
	if len(f.logged) != 1 || !strings.Contains(f.logged[0], "boom") {
		t.Errorf("error not logged: %v", f.logged)
	}
	f = &fakeCIRunner{t: t, replies: map[string]ciReply{checkRunsKey(): {out: "{not json"}}}
	if got, _ := collectCIFailures(f, gitprovider.GitProviderGitHub, ciRepo, "1", ciHead{SHA: ciSHA}, nil); got != "" || len(f.logged) != 1 {
		t.Errorf("decode error: got %q logged %v", got, f.logged)
	}
}

func TestCIHugeStepNameStaysWithinCap(t *testing.T) {
	log := "##[group]" + strings.Repeat("x", 200000) + "\n##[error]boom\n"
	if got := renderLog(log); len(got) > ciSectionMaxBytes || !strings.Contains(got, "boom") {
		t.Errorf("len %d", len(got))
	}
	// no-marker branch too
	if got := renderLog("##[group]" + strings.Repeat("x", 200000) + "\nquiet\n"); len(got) > ciSectionMaxBytes {
		t.Errorf("no-marker len %d", len(got))
	}
}

func TestCIFailureLineSurvivesLongNeighbours(t *testing.T) {
	long := strings.Repeat("y", 1990)
	var sb strings.Builder
	sb.WriteString("##[group]Run x\n")
	fmt.Fprintf(&sb, "lead1 %s\nlead2 %s\n##[error]THE REAL ERROR %s\n", long, long, long)
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&sb, "tail%d %s\n", i, long)
	}
	got := renderLog(sb.String())
	if !strings.Contains(got, "THE REAL ERROR") {
		t.Errorf("failure line dropped:\n%.600s", got)
	}
	if len(got) > ciSectionMaxBytes {
		t.Errorf("len %d", len(got))
	}
	// A failure line over the per-line limit is replaced whole, with a note.
	big := renderLog("##[group]Run x\n##[error]" + strings.Repeat("z", 5000) + "\n")
	if !strings.Contains(big, "bytes omitted]") {
		t.Errorf("oversized failure line not noted:\n%s", big)
	}
}

// A line that is both a marker/cite and one of the context lines right after
// the first-failure block must be rendered once, not twice.
func TestCIContextLineThatIsAMarkerIsNotDuplicated(t *testing.T) {
	log := "##[group]Run x\n##[error]first failure\n##[group]Run y\n##[error]second failure\n"
	got := renderLog(log)
	if n := strings.Count(got, "second failure"); n != 1 {
		t.Errorf("second failure rendered %d times, want 1:\n%s", n, got)
	}
}

func TestCINoMarkerLogKeepsTailAndNotesOmission(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "line%d %s\n", i, strings.Repeat("q", 1000))
	}
	got := renderLog(sb.String())
	if !strings.Contains(got, "line29 ") || !strings.Contains(got, "lines omitted") || len(got) > ciBlockMaxBytes+512 {
		t.Errorf("len %d:\n%.300s", len(got), got)
	}
}

func TestCIHugeLogRendersQuickly(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("##[group]Run x\n")
	for i := 0; i < 300000; i++ {
		fmt.Fprintf(&sb, "padding %d\n", i)
	}
	sb.WriteString("ERROR the one marker\n")
	start := time.Now()
	got := renderLog(sb.String())
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("rendering took %s", d)
	}
	if !strings.Contains(got, "the one marker") {
		t.Error("marker lost")
	}
}

func TestResolveCIHeadGitHubUsesHeadRefOid(t *testing.T) {
	f := &fakeCIRunner{t: t, replies: map[string]ciReply{
		"gh pr view 12 --repo o/r --json headRefOid --jq .headRefOid": {out: ciSHA + "\n"},
	}}
	head, err := resolveCIHead(f, gitprovider.GitProviderGitHub, "o/r", "12")
	if err != nil || head.SHA != ciSHA {
		t.Fatalf("head=%+v err=%v", head, err)
	}
	f = &fakeCIRunner{t: t, replies: map[string]ciReply{"gh pr view 12 --repo o/r --json headRefOid --jq .headRefOid": {err: errors.New("nope")}}}
	if head, err := resolveCIHead(f, gitprovider.GitProviderGitHub, "o/r", "12"); err == nil || head != (ciHead{}) {
		t.Fatalf("lookup failure must yield zero head, got %+v %v", head, err)
	}
}

const glMRKey = "glab api projects/g%2Fp/merge_requests/5"

func TestGitLabHeadPipelineFromSameMRResponse(t *testing.T) {
	const sha = "aaaaaaa1111"
	mr := func(pipe string) string {
		return `{"iid":5,"sha":"` + sha + `","diff_refs":{"head_sha":"` + sha + `"},"head_pipeline":` + pipe + `}`
	}
	tests := []struct {
		name     string
		mr       string
		pipes    string
		wantWork bool
		want     string
	}{
		{"failed", mr(`{"id":77,"status":"failed","sha":"` + sha + `"}`), "", true, "pipeline #77 failed, UNVERIFIED: job logs not fetched"},
		{"success", mr(`{"id":77,"status":"success"}`), "", false, ""},
		{"running", mr(`{"id":77,"status":"running"}`), "", false, ""},
		{"canceled", mr(`{"id":77,"status":"canceled"}`), "", false, ""},
		// merged-results pipeline runs on a temporary merge commit: sha != MR head.
		{"merged-results pipeline sha differs from MR head", mr(`{"id":78,"status":"failed","sha":"bbbbbbb2222"}`), "", true, "pipeline #78 failed"},
		{"head_pipeline null, SHA fallback failed", mr(`null`), `[{"id":5,"status":"success","sha":"other000"},{"id":6,"status":"failed","sha":"` + sha + `"}]`, true, "pipeline #6 failed"},
		{"head_pipeline null, SHA fallback latest is green", mr(`null`), `[{"id":6,"status":"failed","sha":"` + sha + `"},{"id":9,"status":"success","sha":"` + sha + `"}]`, false, ""},
		{"head_pipeline null, failed pipeline is for another sha", mr(`null`), `[{"id":6,"status":"failed","sha":"other000"}]`, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCIRunner{t: t, replies: map[string]ciReply{
				glMRKey: {out: tc.mr},
				"glab api projects/g%2Fp/merge_requests/5/pipelines": {out: tc.pipes},
			}}
			// Head is resolved once, from the pre-sync MR response.
			head, err := resolveCIHead(f, gitprovider.GitProviderGitLab, "g/p", "5")
			if err != nil || head.SHA != sha {
				t.Fatalf("head=%+v err=%v", head, err)
			}
			mrFetches := len(f.calls)
			got, _ := collectCIFailures(f, gitprovider.GitProviderGitLab, "g/p", "5", head, nil)
			if (got != "") != tc.wantWork || !strings.Contains(got, tc.want) {
				t.Fatalf("got %q", got)
			}
			if nothingForPlanner("", 0, got, baseSyncResult{}) == tc.wantWork {
				t.Errorf("nothingForPlanner disagrees with work=%v", tc.wantWork)
			}
			for _, c := range f.calls[mrFetches:] {
				if c == glMRKey {
					t.Error("collect must not re-fetch the MR after the sync; head_pipeline comes from the pre-sync response")
				}
			}
		})
	}
}

func TestGitLabLookupFailureMeansNoWork(t *testing.T) {
	f := &fakeCIRunner{t: t, replies: map[string]ciReply{glMRKey: {err: errors.New("boom")}}}
	head, err := resolveCIHead(f, gitprovider.GitProviderGitLab, "g/p", "5")
	if err == nil {
		t.Fatal("want error")
	}
	if got, _ := collectCIFailures(f, gitprovider.GitProviderGitLab, "g/p", "5", head, nil); got != "" {
		t.Errorf("got %q", got)
	}
}

// The CI section must reach the model whole: buildSystemPrompt used to cap it
// with truncateIfNeeded(…, 10000).
func TestBuildSystemPromptPassesCISectionThrough(t *testing.T) {
	a := newBaseSyncAgent(t.TempDir())
	var blocks []ciBlock
	for i := 0; i < 5; i++ {
		var sb strings.Builder
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&sb, "pkg/file%d.go:%d: error: distinct failure %d in job %d\n", j, j+1, j, i)
		}
		blocks = append(blocks, ciBlock{name: fmt.Sprintf("job-%d", i), jobID: int64(i + 1), lines: parseJobLog(sb.String())})
	}
	section := renderCISection(ciRepo, blocks)
	if len(section) <= 10000 || len(section) > ciSectionMaxBytes {
		t.Fatalf("test needs a section between the old 10000 cap and the new cap, got %d", len(section))
	}
	prompt := a.buildSystemPrompt("o/r", "12", "details", "", "", section, nil, "main", baseSyncResult{}, "")
	if !strings.Contains(prompt, "## CI/CD Failure Logs\n"+section+"\n\n") {
		t.Error("CI section was altered by buildSystemPrompt")
	}
	if strings.Contains(prompt, "[truncated]") {
		t.Error("prompt contains [truncated]")
	}
}
