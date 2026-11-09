package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"

	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

func TestBuildExecutionQueryAlwaysScopesTenantAndAccount(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: []string{"acct-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "nb_tenant_id='tenant-1' AND nb_account_id='acct-1'"
	if query != want {
		t.Fatalf("got %q, want %q", query, want)
	}
}

// The SQL visibility store rejects ORDER BY. If anyone adds sorting, this test
// is the tripwire — see the disabled ORDER BY cases in the integration suite.
func TestBuildExecutionQueryNeverEmitsOrderBy(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs:   []string{"acct-1"},
		StartDate:    &start,
		EndDate:      &end,
		WorkflowIDs:  []string{"wf-1", "wf-2"},
		TriggeredBy:  []string{"user-1"},
		Statuses:     []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusFailed},
		TriggerTypes: []string{"schedule"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToUpper(query), "ORDER BY") {
		t.Fatalf("query must not contain ORDER BY, got %q", query)
	}
}

func TestBuildExecutionQueryMultiValueFiltersUseOrGroups(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs:  []string{"acct-1"},
		WorkflowIDs: []string{"wf-1", "wf-2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "(nb_workflow_id='wf-1' OR nb_workflow_id='wf-2')") {
		t.Fatalf("expected OR group, got %q", query)
	}
}

func TestBuildExecutionQuerySingleValueFilterSkipsParentheses(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs:  []string{"acct-1"},
		WorkflowIDs: []string{"wf-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "AND nb_workflow_id='wf-1'") || strings.Contains(query, "(nb_workflow_id") {
		t.Fatalf("expected bare equality clause, got %q", query)
	}
}

func TestBuildExecutionQueryEscapesQuotes(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs:   []string{"acct-1"},
		TriggerTypes: []string{"o'brien"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "nb_workflow_trigger='o''brien'") {
		t.Fatalf("expected doubled quote, got %q", query)
	}
}

func TestBuildExecutionQueryFormatsDatesAsUTCRFC3339(t *testing.T) {
	// Deliberately non-UTC: the query must still render in UTC so a browser in
	// another zone can't shift the window.
	zone := time.FixedZone("IST", 5*3600+1800)
	start := time.Date(2026, 7, 27, 5, 30, 0, 0, zone)
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs: []string{"acct-1"},
		StartDate:  &start,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "StartTime >= '2026-07-27T00:00:00Z'") {
		t.Fatalf("expected UTC RFC3339 start bound, got %q", query)
	}
}

func TestBuildExecutionQueryDropsUnmappedStatuses(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountIDs: []string{"acct-1"},
		Statuses:   []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusUnspecified},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(query, "ExecutionStatus") {
		t.Fatalf("unspecified status must not produce a clause, got %q", query)
	}
}

func TestBuildExecutionQueryRejectsOversizedFilters(t *testing.T) {
	ids := make([]string, model.MaxExecutionFilterValues+1)
	for i := range ids {
		ids[i] = "wf"
	}
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: []string{"acct-1"}, WorkflowIDs: ids}); err == nil {
		t.Fatal("expected an error for an oversized filter")
	}
}

// The Executions tab is tenant-level with an account filter, so a page can span
// several accounts. The account clause has to be an OR group like the other
// multi-value filters -- ANDing them together would match nothing.
func TestBuildExecutionQueryOrGroupsMultipleAccounts(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: []string{"acct-1", "acct-2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "nb_tenant_id='tenant-1' AND (nb_account_id='acct-1' OR nb_account_id='acct-2')"
	if query != want {
		t.Fatalf("got %q, want %q", query, want)
	}
}

// An all-blank account list must not silently degrade to a tenant-wide query --
// that would return runs from accounts the caller may not read.
func TestBuildExecutionQueryRejectsBlankAccounts(t *testing.T) {
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: []string{"", ""}}); err == nil {
		t.Fatal("expected an error when every account id is blank")
	}
}

func TestBuildExecutionQueryRejectsOversizedAccountFilter(t *testing.T) {
	ids := make([]string, model.MaxExecutionFilterValues+1)
	for i := range ids {
		ids[i] = "acct"
	}
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: ids}); err == nil {
		t.Fatal("expected an error for an oversized account filter")
	}
}

// A tenant-wide caller is already scoped by the tenant clause, so the account
// group is redundant. Regression test: enumerating it instead meant a tenant
// with more accounts than MaxExecutionFilterValues could not load the
// unfiltered dashboard at all -- the cap is meant to bound what a caller may
// ASK for, not the resolved "every account I can read" set.
func TestBuildExecutionQueryOmitsAccountClauseWhenTenantWide(t *testing.T) {
	ids := make([]string, model.MaxExecutionFilterValues+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("acct-%d", i)
	}

	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: ids, TenantWide: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "nb_tenant_id='tenant-1'" {
		t.Fatalf("got %q, want the tenant clause alone", query)
	}
}

// The same caller narrowing to a subset must still be pinned to it.
func TestBuildExecutionQueryKeepsAccountClauseWhenFiltered(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountIDs: []string{"acct-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "nb_tenant_id='tenant-1' AND nb_account_id='acct-1'" {
		t.Fatalf("got %q, want the account clause retained", query)
	}
}

func TestBuildExecutionQueryRequiresTenantAndAccount(t *testing.T) {
	if _, err := buildExecutionQuery("", model.ExecutionDashboardFilter{AccountIDs: []string{"acct-1"}}); err == nil {
		t.Fatal("expected an error when tenantID is empty")
	}
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{}); err == nil {
		t.Fatal("expected an error when accountID is empty")
	}
}

func TestIntersectStatuses(t *testing.T) {
	failed := model.FailedExecutionStatuses

	// No user filter → the bucket passes through untouched.
	if got := intersectStatuses(nil, failed); len(got) != len(failed) {
		t.Fatalf("expected pass-through, got %v", got)
	}

	// User filtered to COMPLETED → the failed bucket is empty, which is the
	// honest answer (the table shows no failures either).
	if got := intersectStatuses([]model.WorkflowExecutionStatus{model.WorkflowExecutionStatusCompleted}, failed); len(got) != 0 {
		t.Fatalf("expected empty intersection, got %v", got)
	}

	// Partial overlap keeps only the shared statuses.
	got := intersectStatuses([]model.WorkflowExecutionStatus{
		model.WorkflowExecutionStatusFailed,
		model.WorkflowExecutionStatusRunning,
	}, failed)
	if len(got) != 1 || got[0] != model.WorkflowExecutionStatusFailed {
		t.Fatalf("expected [FAILED], got %v", got)
	}
}

func TestTemporalOrGroupIgnoresEmptyValues(t *testing.T) {
	if got := temporalOrGroup("attr", []string{"", ""}); got != "" {
		t.Fatalf("expected no clause, got %q", got)
	}
	if got := temporalOrGroup("attr", []string{"", "a"}); got != "attr='a'" {
		t.Fatalf("expected single clause, got %q", got)
	}
}

func TestDistinctNonEmpty(t *testing.T) {
	values := []string{"a", "", "b", "a", ""}
	got := distinctNonEmpty(len(values), func(i int) string { return values[i] })
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b] in first-seen order, got %v", got)
	}
}

// closeTimeStore is a stand-in for the visibility store's ordering: a set of
// close times plus the open runs that sort ahead of all of them. rankBefore
// answers the same question the real CountWorkflow pair does.
type closeTimeStore struct {
	closeTimes []time.Time // any order; ranked by value, newest first
	openRows   int64
	probes     int
}

func (s *closeTimeStore) rankBefore(boundary time.Time) (int64, error) {
	s.probes++
	rank := s.openRows
	for _, closeTime := range s.closeTimes {
		if closeTime.After(boundary) {
			rank++
		}
	}
	return rank, nil
}

// evenlySpacedStore lays `rows` executions one minute apart, oldest first.
func evenlySpacedStore(rows int, openRows int64) (*closeTimeStore, time.Time, time.Time) {
	oldest := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	store := &closeTimeStore{openRows: openRows}
	for i := 0; i < rows; i++ {
		store.closeTimes = append(store.closeTimes, oldest.Add(time.Duration(i+1)*time.Minute))
	}
	newest := oldest.Add(time.Duration(rows+1) * time.Minute)
	return store, oldest, newest
}

func TestSeekRankByCloseTimeLandsWithinTolerance(t *testing.T) {
	const rows, limit = 11138, 20
	store, oldest, newest := evenlySpacedStore(rows, 0)
	total := int64(rows)

	// Page 1, a middle page, and the last page: the ends are where an
	// interpolation search is most likely to overshoot its bracket.
	for _, page := range []int64{2, 300, 557} {
		store.probes = 0
		target := (page - 1) * limit
		boundary, rank, err := seekRankByCloseTime(target, limit, oldest, total, newest, 0, store.rankBefore, model.MaxExecutionSeekProbes)
		if err != nil {
			t.Fatalf("page %d: unexpected error: %v", page, err)
		}
		if rank > target || target-rank > limit {
			t.Fatalf("page %d: rank %d not within %d of target %d", page, rank, limit, target)
		}
		// The boundary must be usable as the caller uses it: the rows the list
		// call would return start exactly at `rank`.
		got, _ := store.rankBefore(boundary)
		if got != rank {
			t.Fatalf("page %d: boundary re-ranks to %d, want %d", page, got, rank)
		}
	}
}

func TestSeekRankByCloseTimeCountsOpenRunsAhead(t *testing.T) {
	const rows, limit = 5000, 20
	// 40 open runs occupy the first two pages, so page 3 starts at the first
	// closed row: the boundary must be the newest close time, rank 40.
	store, oldest, newest := evenlySpacedStore(rows, 40)
	target := int64(2 * limit)

	_, rank, err := seekRankByCloseTime(target, limit, oldest, int64(rows)+40, newest, 40, store.rankBefore, model.MaxExecutionSeekProbes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rank != 40 {
		t.Fatalf("expected the seek to stop at the open-run offset 40, got %d", rank)
	}
}

func TestSeekRankByCloseTimeStaysUnderProbeBudget(t *testing.T) {
	const rows, limit = 11138, 20
	store, oldest, newest := evenlySpacedStore(rows, 0)
	store.probes = 0

	if _, _, err := seekRankByCloseTime(9000, limit, oldest, int64(rows), newest, 0, store.rankBefore, model.MaxExecutionSeekProbes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.probes > model.MaxExecutionSeekProbes {
		t.Fatalf("used %d probes, budget is %d", store.probes, model.MaxExecutionSeekProbes)
	}
}

// A bursty window is the case interpolation handles worst: every row closes
// inside one minute of a ten-day range, so the first guesses land nowhere near.
// The midpoint fallback must still make progress rather than stalling, and the
// result must stay on the safe side of the target.
func TestSeekRankByCloseTimeSurvivesBurstyDistribution(t *testing.T) {
	const rows, limit = 4000, 20
	oldest := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	newest := oldest.Add(10 * 24 * time.Hour)
	burst := oldest.Add(9 * 24 * time.Hour)
	store := &closeTimeStore{}
	for i := 0; i < rows; i++ {
		store.closeTimes = append(store.closeTimes, burst.Add(time.Duration(i)*15*time.Millisecond))
	}

	target := int64(2000)
	_, rank, err := seekRankByCloseTime(target, limit, oldest, int64(rows), newest, 0, store.rankBefore, model.MaxExecutionSeekProbes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rank > target {
		t.Fatalf("rank %d overshot target %d — the caller cannot slice backwards", rank, target)
	}
}

func TestSeekRankByCloseTimeReturnsBestEffortWhenBudgetRunsOut(t *testing.T) {
	const rows, limit = 11138, 20
	store, oldest, newest := evenlySpacedStore(rows, 0)

	// One probe cannot converge on a middle page. The result must still be a
	// safe under-estimate rather than an error.
	_, rank, err := seekRankByCloseTime(6000, limit, oldest, int64(rows), newest, 0, store.rankBefore, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rank > 6000 {
		t.Fatalf("best-effort rank %d must not exceed the target", rank)
	}
}

func TestSeekRankByCloseTimeHandlesEmptyResultSet(t *testing.T) {
	store, oldest, newest := evenlySpacedStore(0, 0)
	boundary, rank, err := seekRankByCloseTime(100, 20, oldest, 0, newest, 0, store.rankBefore, model.MaxExecutionSeekProbes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rank != 0 {
		t.Fatalf("expected rank 0 on an empty set, got %d", rank)
	}
	if boundary.IsZero() {
		t.Fatal("expected a usable boundary even with no rows")
	}
}

func TestSeekRankByCloseTimePropagatesCountErrors(t *testing.T) {
	_, oldest, newest := evenlySpacedStore(100, 0)
	failing := func(time.Time) (int64, error) { return 0, fmt.Errorf("visibility unavailable") }
	if _, _, err := seekRankByCloseTime(50, 20, oldest, 100, newest, 0, failing, model.MaxExecutionSeekProbes); err == nil {
		t.Fatal("expected the count error to surface")
	}
}

// countingClient answers CountWorkflow from a closeTimeStore the way the SQL
// visibility store does. openMatchesCloseTime picks which of the store's two
// behaviours is in force: Temporal up to 1.31 treats a run that has not closed
// as closing in the far future, so `CloseTime > T` matches it; from 1.32 such a
// run has no close time and matches no CloseTime predicate at all.
type countingClient struct {
	client.Client
	store                *closeTimeStore
	openMatchesCloseTime bool
}

func (c *countingClient) CountWorkflow(_ context.Context, req *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	query := req.GetQuery()
	openOnly := strings.Contains(query, "ExecutionStatus='Running'")
	closedOnly := strings.Contains(query, "ExecutionStatus!='Running'")

	var boundary time.Time
	_, rest, bounded := strings.Cut(query, "CloseTime > '")
	if bounded {
		stamp, _, _ := strings.Cut(rest, "'")
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, err
		}
		boundary = parsed
	}

	var count int64
	if !closedOnly && (!bounded || c.openMatchesCloseTime) {
		count += c.store.openRows
	}
	if !openOnly {
		for _, closeTime := range c.store.closeTimes {
			if !bounded || closeTime.After(boundary) {
				count++
			}
		}
	}
	return &workflowservice.CountWorkflowExecutionsResponse{Count: count}, nil
}

func TestSeekPageBoundaryCountsOpenRunsOnce(t *testing.T) {
	const rows, openRows, limit = 5000, 7, 20
	// Page 60 at 20 rows: the first page past MaxExecutionDeepPageRows.
	const target = int64(1180)

	for name, openMatchesCloseTime := range map[string]bool{
		"open runs match a CloseTime predicate":  true,
		"open runs match no CloseTime predicate": false,
	} {
		t.Run(name, func(t *testing.T) {
			oldest := time.Now().UTC().Add(-time.Duration(rows+1) * time.Minute).Truncate(time.Microsecond)
			store := &closeTimeStore{openRows: openRows}
			for i := 0; i < rows; i++ {
				store.closeTimes = append(store.closeTimes, oldest.Add(time.Duration(i+1)*time.Minute))
			}
			s := &Service{temporalClient: &countingClient{store: store, openMatchesCloseTime: openMatchesCloseTime}}

			boundary, rank, err := s.seekPageBoundary(context.Background(), "nb_tenant_id='tenant-1'", model.ExecutionDashboardFilter{StartDate: &oldest}, target, limit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The list call returns the closed rows at or before the boundary, so
			// the page really starts at the boundary's own rank. A reported rank
			// that differs makes the caller slice the page at the wrong row.
			want, _ := store.rankBefore(boundary)
			if rank != want {
				t.Fatalf("seek reported rank %d for a boundary that ranks %d — the page would be off by %d rows", rank, want, rank-want)
			}
			if rank > target || target-rank > limit {
				t.Fatalf("rank %d not within %d of target %d", rank, limit, target)
			}
		})
	}
}

func TestRankFailedAutomationsOrdersByCountThenID(t *testing.T) {
	ranked := rankFailedAutomations(map[string]int64{
		"wf-b": 10,
		"wf-a": 10,
		"wf-c": 99,
	}, 5)

	got := make([]string, 0, len(ranked))
	for _, entry := range ranked {
		got = append(got, entry.WorkflowID)
	}
	// wf-c first on count; wf-a before wf-b on the id tiebreak, so a refresh
	// cannot reshuffle equal-count rows.
	if len(got) != 3 || got[0] != "wf-c" || got[1] != "wf-a" || got[2] != "wf-b" {
		t.Fatalf("got %v, want [wf-c wf-a wf-b]", got)
	}
}

func TestRankFailedAutomationsAppliesLimit(t *testing.T) {
	tally := map[string]int64{}
	for i := 0; i < 20; i++ {
		tally[fmt.Sprintf("wf-%02d", i)] = int64(i)
	}
	ranked := rankFailedAutomations(tally, 5)
	if len(ranked) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(ranked))
	}
	if ranked[0].WorkflowID != "wf-19" || ranked[0].FailureCount != 19 {
		t.Fatalf("expected the busiest automation first, got %+v", ranked[0])
	}
}

func TestRankFailedAutomationsHandlesEmptyTally(t *testing.T) {
	if ranked := rankFailedAutomations(map[string]int64{}, 5); len(ranked) != 0 {
		t.Fatalf("expected no entries, got %d", len(ranked))
	}
}

// statusCounter answers a status-set count the way the visibility store does:
// the sum of the listed statuses, or every run when the set is empty (no
// status clause at all).
func statusCounter(byStatus map[model.WorkflowExecutionStatus]int64) func([]model.WorkflowExecutionStatus) int64 {
	return func(statuses []model.WorkflowExecutionStatus) int64 {
		var count int64
		if len(statuses) == 0 {
			for _, n := range byStatus {
				count += n
			}
			return count
		}
		for _, status := range statuses {
			count += byStatus[status]
		}
		return count
	}
}

// The window from #39874: 17 runs, of which 4 FAILED, 1 TIMED_OUT, 3 CANCELED.
var issue39874Window = map[model.WorkflowExecutionStatus]int64{
	model.WorkflowExecutionStatusCompleted: 9,
	model.WorkflowExecutionStatusFailed:    4,
	model.WorkflowExecutionStatusTimedOut:  1,
	model.WorkflowExecutionStatusCanceled:  3,
}

func TestExecutionStatusCountsPutEveryClosedRunInOneBucket(t *testing.T) {
	got := executionStatusCounts(nil, statusCounter(issue39874Window))

	if got.Total != 17 || got.Succeeded != 9 || got.TimedOut != 1 || got.Terminated != 0 || got.Canceled != 3 || got.Running != 0 {
		t.Fatalf("unexpected buckets: %+v", got)
	}
	// `failed` keeps meaning "did not succeed", which the leaderboard ranks.
	if got.Failed != 5 {
		t.Fatalf("failed = %d, want 5 (FAILED + TIMED_OUT + TERMINATED)", got.Failed)
	}
	// The summary card's Failed number must be what Status = Failed lists.
	if failedStatusOnly := got.Failed - got.TimedOut - got.Terminated; failedStatusOnly != 4 {
		t.Fatalf("failed - timed_out - terminated = %d, want the 4 FAILED runs", failedStatusOnly)
	}
	if sum := got.Succeeded + got.Failed + got.Canceled + got.Running; sum != got.Total {
		t.Fatalf("buckets sum to %d, total is %d", sum, got.Total)
	}
}

func TestExecutionStatusCountsSplitsTerminatedOutOfFailed(t *testing.T) {
	window := map[model.WorkflowExecutionStatus]int64{
		model.WorkflowExecutionStatusFailed:     2,
		model.WorkflowExecutionStatusTerminated: 3,
		model.WorkflowExecutionStatusTimedOut:   4,
	}
	got := executionStatusCounts(nil, statusCounter(window))
	if got.Failed != 9 || got.Terminated != 3 || got.TimedOut != 4 {
		t.Fatalf("unexpected buckets: %+v", got)
	}
}

// A bucket the user's own status filter excludes is zero. It used to be counted
// with an empty status set, which drops the status clause and counts every run.
func TestExecutionStatusCountsZeroesBucketsTheFilterExcludes(t *testing.T) {
	got := executionStatusCounts(
		[]model.WorkflowExecutionStatus{model.WorkflowExecutionStatusCompleted},
		statusCounter(issue39874Window),
	)
	if got.Total != 9 || got.Succeeded != 9 {
		t.Fatalf("total/succeeded = %d/%d, want 9/9", got.Total, got.Succeeded)
	}
	if got.Failed != 0 || got.TimedOut != 0 || got.Terminated != 0 || got.Canceled != 0 || got.Running != 0 {
		t.Fatalf("filtered-out buckets must be zero: %+v", got)
	}
}

func TestLeaderboardKeyGroupsDryRuns(t *testing.T) {
	cases := map[string]string{
		"dry-run-6f1c9a52-0000-4000-8000-000000000001": model.DryRunWorkflowIDPrefix,
		"dry-run-6f1c9a52-0000-4000-8000-000000000002": model.DryRunWorkflowIDPrefix,
		"6f1c9a52-0000-4000-8000-000000000003":         "6f1c9a52-0000-4000-8000-000000000003",
		"inline-task-1-6f1c9a52":                       "inline-task-1-6f1c9a52",
	}
	for in, want := range cases {
		if got := leaderboardKey(in); got != want {
			t.Errorf("leaderboardKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// listingClient serves one page of visibility rows carrying only nb_workflow_id.
type listingClient struct {
	client.Client
	rows []*workflowpb.WorkflowExecutionInfo
}

func (c *listingClient) ListWorkflow(_ context.Context, _ *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	return &workflowservice.ListWorkflowExecutionsResponse{Executions: c.rows}, nil
}

func TestTallyFailuresByScanRanksDryRunsAsOneRow(t *testing.T) {
	dc := converter.GetDefaultDataConverter()
	row := func(workflowID string) *workflowpb.WorkflowExecutionInfo {
		payload, err := dc.ToPayload(workflowID)
		if err != nil {
			t.Fatalf("encode search attribute: %v", err)
		}
		return &workflowpb.WorkflowExecutionInfo{SearchAttributes: &commonpb.SearchAttributes{
			IndexedFields: map[string]*commonpb.Payload{model.SearchAttrWorkflowID: payload},
		}}
	}
	s := &Service{dataConverter: dc, temporalClient: &listingClient{rows: []*workflowpb.WorkflowExecutionInfo{
		row("dry-run-6f1c9a52-0000-4000-8000-000000000001"),
		row("dry-run-6f1c9a52-0000-4000-8000-000000000002"),
		row("dry-run-6f1c9a52-0000-4000-8000-000000000003"),
		row("wf-a"),
		row("wf-a"),
	}}}

	tally, err := s.tallyFailuresByScan(context.Background(), "nb_tenant_id='t1'", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ranked := rankFailedAutomations(tally, model.DefaultTopFailedLimit)
	if len(ranked) != 2 {
		t.Fatalf("want one dry-run row and one automation row, got %+v", ranked)
	}
	if ranked[0].WorkflowID != model.DryRunWorkflowIDPrefix || ranked[0].FailureCount != 3 {
		t.Fatalf("first row = %+v, want the three dry runs grouped", ranked[0])
	}
	if ranked[1].WorkflowID != "wf-a" || ranked[1].FailureCount != 2 {
		t.Fatalf("second row = %+v, want wf-a with 2", ranked[1])
	}
}
