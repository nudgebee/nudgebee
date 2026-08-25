package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	commonpb "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

const (
	// failureReasonFetchBudget bounds the per-page close-event lookups. A page
	// that can't resolve every reason in time returns the ones it got; the
	// full error is always available from workflow_get_execution.
	failureReasonFetchBudget = 2 * time.Second
	// failureReasonFetchConcurrency caps parallel history calls per page.
	failureReasonFetchConcurrency = 6
	// systemUserID is the nil UUID stamped on runs no person started (schedules,
	// webhook fan-out). It has no row in `users`, so looking it up is wasted work.
	systemUserID = "00000000-0000-0000-0000-000000000000"
)

// buildExecutionQuery renders the Temporal visibility query shared by the
// execution list, the summary counts and the most-failed leaderboard.
//
// Two rules are load-bearing and must not be "improved":
//
//   - No ORDER BY. The visibility store is SQL (postgres12), which rejects it —
//     see the disabled ORDER BY cases in tests/integration/workflow_api_test.go.
//     Its own ordering is open runs first, then closed runs by CloseTime DESC,
//     which is the newest-first the dashboard wants anyway. That order is not
//     just cosmetic now: seekPageBoundary addresses a deep page by close time,
//     so anything that changes it changes which rows a page number returns.
//   - Multi-value filters render as OR groups rather than IN. OR is what the
//     rest of this service already emits against this store.
//
// Tenant and account are always applied and always come from the caller's
// security context, never from request args.
func buildExecutionQuery(tenantID string, f model.ExecutionDashboardFilter) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenantID is required")
	}
	if len(f.AccountIDs) == 0 {
		return "", fmt.Errorf("at least one account_id is required")
	}
	for name, values := range map[string][]string{
		"workflow_ids": f.WorkflowIDs,
		"triggered_by": f.TriggeredBy,
		"trigger_type": f.TriggerTypes,
	} {
		if len(values) > model.MaxExecutionFilterValues {
			return "", common.ErrorBadRequest(fmt.Sprintf("%s accepts at most %d values", name, model.MaxExecutionFilterValues))
		}
	}
	if len(f.Statuses) > model.MaxExecutionFilterValues {
		return "", common.ErrorBadRequest(fmt.Sprintf("statuses accepts at most %d values", model.MaxExecutionFilterValues))
	}

	conditions := []string{
		fmt.Sprintf("%s='%s'", model.SearchAttrTenantID, escapeTemporalString(tenantID)),
	}
	// A tenant-wide caller who asked for no subset is already fully scoped by
	// the tenant clause above, so enumerating every account would be redundant
	// — and on a tenant with more accounts than MaxExecutionFilterValues it
	// would be rejected outright. Everyone else must be pinned to their own
	// accounts: the tenant clause alone would span accounts they cannot read.
	if !f.TenantWide {
		if len(f.AccountIDs) > model.MaxExecutionFilterValues {
			return "", common.ErrorBadRequest(fmt.Sprintf("account_ids accepts at most %d values", model.MaxExecutionFilterValues))
		}
		accountGroup := temporalOrGroup(model.SearchAttrAccountID, f.AccountIDs)
		if accountGroup == "" {
			return "", fmt.Errorf("at least one account_id is required")
		}
		conditions = append(conditions, accountGroup)
	}

	if group := temporalOrGroup(model.SearchAttrWorkflowID, f.WorkflowIDs); group != "" {
		conditions = append(conditions, group)
	}
	if group := temporalOrGroup(model.SearchAttrTriggeredBy, f.TriggeredBy); group != "" {
		conditions = append(conditions, group)
	}
	if group := temporalOrGroup(model.SearchAttrWorkflowTrigger, f.TriggerTypes); group != "" {
		conditions = append(conditions, group)
	}

	temporalStatuses := make([]string, 0, len(f.Statuses))
	for _, status := range f.Statuses {
		if mapped := mapToTemporalStatus(status); mapped != "" {
			temporalStatuses = append(temporalStatuses, mapped)
		}
	}
	if group := temporalOrGroup("ExecutionStatus", temporalStatuses); group != "" {
		conditions = append(conditions, group)
	}

	// Both bounds filter StartTime: a run is attributed to when it started, so
	// narrowing the range never makes a row appear in two different windows.
	if f.StartDate != nil {
		conditions = append(conditions, fmt.Sprintf("StartTime >= '%s'", f.StartDate.UTC().Format(time.RFC3339)))
	}
	if f.EndDate != nil {
		conditions = append(conditions, fmt.Sprintf("StartTime <= '%s'", f.EndDate.UTC().Format(time.RFC3339)))
	}

	return strings.Join(conditions, " AND "), nil
}

// temporalOrGroup renders `attr='a'` or `(attr='a' OR attr='b')`. Empty values
// are dropped; an all-empty list yields no clause at all.
func temporalOrGroup(attr string, values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s='%s'", attr, escapeTemporalString(value)))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return "(" + strings.Join(parts, " OR ") + ")"
	}
}

// intersectStatuses narrows `want` by the user's own status filter so a stat
// card never reports rows the table isn't showing. An empty user filter means
// "no restriction", so `want` passes through untouched.
func intersectStatuses(userFilter, want []model.WorkflowExecutionStatus) []model.WorkflowExecutionStatus {
	if len(userFilter) == 0 {
		return want
	}
	selected := make(map[model.WorkflowExecutionStatus]struct{}, len(userFilter))
	for _, status := range userFilter {
		selected[status] = struct{}{}
	}
	out := make([]model.WorkflowExecutionStatus, 0, len(want))
	for _, status := range want {
		if _, ok := selected[status]; ok {
			out = append(out, status)
		}
	}
	return out
}

// ListAccountExecutions returns one page of executions across every automation
// in an account.
func (s *Service) ListAccountExecutions(ctx *security.RequestContext, req model.ListAccountExecutionsRequest) (model.ListAccountExecutionsResponse, error) {
	scope, err := ResolveAccountScope(ctx, req.AccountIDs)
	if err != nil {
		return model.ListAccountExecutionsResponse{}, err
	}
	req.AccountIDs = scope.AccountIDs
	req.TenantWide = scope.TenantWide
	tenantID := ctx.GetSecurityContext().GetTenantId()

	query, err := buildExecutionQuery(tenantID, req.ExecutionDashboardFilter)
	if err != nil {
		return model.ListAccountExecutionsResponse{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = model.DefaultExecutionPageSize
	}
	if limit > model.MaxExecutionPageSize {
		limit = model.MaxExecutionPageSize
	}

	// Temporal only offers a forward-only cursor, but the table lets users
	// click a page number. When a token is available it is used directly.
	// Otherwise there are two ways to serve page N, and which one is cheaper
	// depends on how deep it is:
	//
	//   - Shallow: one over-fetch of N*limit rows, then slice. A single round
	//     trip, and the fastest thing available while N*limit stays small.
	//   - Deep: seek to the page's close-time boundary (seekPageBoundary) and
	//     fetch one small page from there. A handful of counts instead of a
	//     multi-megabyte read, and flat in depth.
	listQuery := query
	pageSize := limit
	skip := 0
	if req.NextPageToken == "" && req.Page > 1 {
		targetRank := (req.Page - 1) * limit
		if targetRank+limit <= model.MaxExecutionDeepPageRows {
			pageSize = targetRank + limit
			skip = targetRank
		} else {
			boundary, rank, seekErr := s.seekPageBoundary(ctx.GetContext(), query, req.ExecutionDashboardFilter, int64(targetRank), int64(limit))
			if seekErr != nil {
				return model.ListAccountExecutionsResponse{}, seekErr
			}
			// The count that produced `rank` is strictly `CloseTime > boundary`,
			// so `CloseTime <= boundary` picks up exactly where it left off —
			// rows sharing the boundary microsecond land on one side only, and
			// the pair can neither drop nor duplicate a row.
			listQuery = query + fmt.Sprintf(" AND CloseTime <= '%s'", boundary.UTC().Format(time.RFC3339Nano))
			skip = int(int64(targetRank) - rank)
			if skip+limit > model.MaxExecutionDeepPageRows {
				return model.ListAccountExecutionsResponse{}, common.ErrorBadRequest(
					"could not locate that page — narrow the date range or filters")
			}
			pageSize = skip + limit
		}
	}

	resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
		Query:         listQuery,
		PageSize:      int32(pageSize),
		NextPageToken: []byte(req.NextPageToken),
	})
	if err != nil {
		slog.Error("failed to list account executions", "error", err, "query", listQuery)
		return model.ListAccountExecutionsResponse{}, fmt.Errorf("failed to list executions: %w", err)
	}

	infos := resp.GetExecutions()
	if skip > 0 {
		if skip >= len(infos) {
			infos = nil
		} else {
			infos = infos[skip:]
		}
	}

	rows := make([]model.AccountExecutionSummary, 0, len(infos))
	for _, info := range infos {
		summary := s.executionSummaryFromInfo(info)
		row := model.AccountExecutionSummary{
			WorkflowExecutionSummary: summary,
			// Read off the row's own visibility record — a page can span
			// several accounts now.
			AccountID: s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrAccountID),
		}
		if summary.StartTime != nil && summary.CloseTime != nil {
			durationMs := summary.CloseTime.Sub(*summary.StartTime).Milliseconds()
			row.DurationMs = &durationMs
		}
		rows = append(rows, row)
	}

	s.resolveWorkflowNames(ctx, tenantID, req.AccountIDs, rows)
	s.resolveUserNames(ctx, rows)
	if req.IncludeFailureReason {
		s.resolveFailureReasons(ctx, rows)
	}

	// Best effort: a failed count must not blank out a page of real rows.
	var total int64
	if countResp, countErr := s.temporalClient.CountWorkflow(ctx.GetContext(), &workflowservice.CountWorkflowExecutionsRequest{
		Query: query,
	}); countErr != nil {
		slog.Warn("failed to count account executions", "error", countErr, "query", query)
	} else {
		total = countResp.GetCount()
	}
	// The count is approximate by Temporal's own admission, and zero when it
	// failed outright. Either way it must not claim fewer rows than this page
	// actually contains, or the pager renders "0 of 0" over visible rows.
	if total < int64(len(rows)) {
		total = int64(len(rows))
	}

	return model.ListAccountExecutionsResponse{
		Executions:         rows,
		NextPageToken:      string(resp.GetNextPageToken()),
		TotalCount:         total,
		TotalIsApproximate: true,
	}, nil
}

// seekPageBoundary locates the close time that page `targetRank` starts at,
// and returns it with the rank it actually landed on.
//
// The visibility store has no OFFSET, so a deep page cannot be addressed by
// position — but it can be addressed by value. Rows are ordered open-first then
// close_time DESC, and both halves of that order are queryable: counting
// `CloseTime > T` gives the rank of T, and listing `CloseTime <= T` starts at
// that rank. So the search is over close times, not over rows, and its cost is
// independent of how deep the page is.
//
// The returned rank is always <= targetRank; the caller slices the difference
// off the front of its page.
func (s *Service) seekPageBoundary(
	ctx context.Context,
	query string,
	filter model.ExecutionDashboardFilter,
	targetRank int64,
	limit int64,
) (time.Time, int64, error) {
	count := func(q string) (int64, error) {
		resp, err := s.temporalClient.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{Query: q})
		if err != nil {
			return 0, fmt.Errorf("failed to count executions while seeking page: %w", err)
		}
		return resp.GetCount(), nil
	}

	// Open runs have no close time, so no CloseTime predicate can see them —
	// yet they sort ahead of every closed row. Their count is the constant
	// offset between "rows before T" and "closed rows before T".
	openRows, err := count(query + " AND ExecutionStatus='Running'")
	if err != nil {
		return time.Time{}, 0, err
	}
	total, err := count(query)
	if err != nil {
		return time.Time{}, 0, err
	}

	rankBefore := func(boundary time.Time) (int64, error) {
		closedAhead, countErr := count(query + fmt.Sprintf(" AND CloseTime > '%s'", boundary.UTC().Format(time.RFC3339Nano)))
		if countErr != nil {
			return 0, countErr
		}
		return openRows + closedAhead, nil
	}

	// A page that starts inside the open runs cannot be addressed by close time
	// at all — they have none. Reaching this needs more concurrently-running
	// executions than MaxExecutionDeepPageRows, which is its own problem.
	if targetRank < openRows {
		return time.Time{}, 0, common.ErrorBadRequest(
			"could not locate that page — narrow the date range or filters")
	}

	// The bracket needs no probes to seed. A row cannot close before it starts
	// and the query already floors StartTime, so every row lies after `oldest`
	// — rank(oldest) is the whole result set. Nothing has closed in the future,
	// so rank(newest) is just the open runs.
	oldest := time.Unix(0, 0).UTC()
	if filter.StartDate != nil {
		oldest = filter.StartDate.UTC()
	}
	// A page number past the end of the result set — a hand-edited URL, or a
	// filter that narrowed under a stale pager. There is no boundary to find,
	// so searching for one would spend the whole probe budget to arrive at the
	// oldest row anyway.
	if targetRank >= total {
		return oldest, total, nil
	}
	newest := time.Now().UTC().Add(time.Minute)

	return seekRankByCloseTime(targetRank, limit, oldest, total, newest, openRows, rankBefore, model.MaxExecutionSeekProbes)
}

// seekRankByCloseTime searches the close-time axis for a boundary whose rank is
// at most targetRank and no more than `tolerance` short of it.
//
// Rank falls as the boundary moves later in time, and executions are dense
// enough that rank is close to linear in time — so this interpolates rather
// than bisecting. A bisection to microsecond precision over a 10-day window
// would need ~30 probes; interpolation converges in a handful. When a guess
// fails to move the bracket it falls back to the midpoint, which bounds the
// worst case to plain bisection instead of a stall.
//
// Returns the best boundary found if the probe budget runs out. That result is
// still correct — the rows from it are contiguous and in order — only further
// from the requested page than the caller asked for, which the caller checks.
func seekRankByCloseTime(
	targetRank, tolerance int64,
	oldest time.Time, rankAtOldest int64,
	newest time.Time, rankAtNewest int64,
	rankBefore func(time.Time) (int64, error),
	maxProbes int,
) (time.Time, int64, error) {
	// lo/hi bracket the answer in time: rank(lo) >= targetRank >= rank(hi).
	// rank(newest) is not zero when runs are still open — they sort ahead of
	// every closed row and no close-time boundary can move past them.
	lo, rankLo := oldest, rankAtOldest
	hi, rankHi := newest, rankAtNewest
	if targetRank-rankHi <= tolerance {
		return hi, rankHi, nil
	}

	for probe := 0; probe < maxProbes; probe++ {
		if !hi.After(lo.Add(time.Microsecond)) {
			break
		}
		span := hi.Sub(lo)
		guess := hi.Add(-span / 2)
		if rankLo > rankHi {
			// Where along the bracket the target rank should fall, measured
			// from the high (later, lower-rank) end.
			fraction := float64(targetRank-rankHi) / float64(rankLo-rankHi)
			guess = hi.Add(-time.Duration(fraction * float64(span)))
		}
		// Microsecond is the store's own resolution; anything finer cannot
		// move the result and would burn probes.
		guess = guess.Truncate(time.Microsecond)
		if !guess.After(lo) || !guess.Before(hi) {
			guess = lo.Add(span / 2).Truncate(time.Microsecond)
		}

		rank, err := rankBefore(guess)
		if err != nil {
			return time.Time{}, 0, err
		}
		if rank <= targetRank {
			hi, rankHi = guess, rank
			if targetRank-rank <= tolerance {
				return hi, rankHi, nil
			}
		} else {
			lo, rankLo = guess, rank
		}
	}

	// hi only ever holds a probe whose rank was <= targetRank, so this is the
	// closest safe boundary seen.
	return hi, rankHi, nil
}

// AggregateExecutions returns the dashboard's summary metrics plus the
// most-failed-automation leaderboard, over the same filter as the list.
func (s *Service) AggregateExecutions(ctx *security.RequestContext, req model.AggregateExecutionsRequest) (model.AggregateExecutionsResponse, error) {
	scope, err := ResolveAccountScope(ctx, req.AccountIDs)
	if err != nil {
		return model.AggregateExecutionsResponse{}, err
	}
	req.AccountIDs = scope.AccountIDs
	req.TenantWide = scope.TenantWide
	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Validate the base filter up front so a bad request fails before any
	// Temporal round trip.
	if _, err := buildExecutionQuery(tenantID, req.ExecutionDashboardFilter); err != nil {
		return model.AggregateExecutionsResponse{}, err
	}

	topFailedLimit := req.TopFailedLimit
	if topFailedLimit <= 0 {
		topFailedLimit = model.DefaultTopFailedLimit
	}
	if topFailedLimit > model.MaxTopFailedLimit {
		topFailedLimit = model.MaxTopFailedLimit
	}

	countFor := func(statuses []model.WorkflowExecutionStatus) int64 {
		filter := req.ExecutionDashboardFilter
		filter.Statuses = statuses
		query, err := buildExecutionQuery(tenantID, filter)
		if err != nil {
			return 0
		}
		resp, err := s.temporalClient.CountWorkflow(ctx.GetContext(), &workflowservice.CountWorkflowExecutionsRequest{Query: query})
		if err != nil {
			slog.Warn("failed to count executions for aggregate", "error", err, "query", query)
			return 0
		}
		return resp.GetCount()
	}

	// Temporal cannot GROUP BY ExecutionStatus on the SQL visibility store, so
	// each bucket is its own count. Each bucket is intersected with the user's
	// own status filter, otherwise the cards would contradict the table.
	failedStatuses := intersectStatuses(req.Statuses, model.FailedExecutionStatuses)
	response := model.AggregateExecutionsResponse{
		Total:                countFor(req.Statuses),
		Succeeded:            countFor(intersectStatuses(req.Statuses, []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusCompleted})),
		Failed:               countFor(failedStatuses),
		Running:              countFor(intersectStatuses(req.Statuses, []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusRunning})),
		TimedOut:             countFor(intersectStatuses(req.Statuses, []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusTimedOut})),
		CountsAreApproximate: true,
		TopFailed:            []model.FailedAutomationCount{},
		RetentionDays:        s.executionRetentionDays(ctx.GetContext()),
	}

	// A count of zero for an intersected-away bucket is correct, not a bug: a
	// user filtering to COMPLETED genuinely has no failures in view.
	if len(failedStatuses) > 0 {
		topFailed, approximate, err := s.topFailedAutomations(ctx, tenantID, req.ExecutionDashboardFilter, failedStatuses, response.Failed, topFailedLimit)
		if err != nil {
			slog.Warn("failed to build most-failed leaderboard", "error", err)
		} else {
			response.TopFailed = topFailed
			response.TopFailedIsApproximate = approximate
		}
	}

	return response, nil
}

// topFailedAutomations ranks automations by failure count within the filter,
// and reports whether the ranking is approximate.
//
// Temporal cannot GROUP BY a custom search attribute, so the ranking is
// assembled client-side from whichever of two exact strategies is cheaper for
// this tenant's shape. Both cost about the same per round trip, so the choice
// is simply which needs fewer: one call per 1,000 failures, or one call per
// automation. See the leaderboard block in model/execution_dashboard.go.
//
// The old single-page tally survives as the last resort, for the tenant that
// fits neither. It is the only path that returns approximate = true.
func (s *Service) topFailedAutomations(
	ctx *security.RequestContext,
	tenantID string,
	filter model.ExecutionDashboardFilter,
	failedStatuses []model.WorkflowExecutionStatus,
	failedTotal int64,
	limit int,
) ([]model.FailedAutomationCount, bool, error) {
	filter.Statuses = failedStatuses
	query, err := buildExecutionQuery(tenantID, filter)
	if err != nil {
		return nil, false, err
	}

	// One over the cap, so "more automations than we will fan out to" is
	// visible from the result size without a second query.
	automations, namesErr := s.store.ListWorkflowIDNames(ctx.GetContext(), tenantID, filter.AccountIDs, model.MaxLeaderboardFanOut+1)
	if namesErr != nil {
		ctx.GetLogger().Warn("failed to list automations for leaderboard", "error", namesErr)
		automations = nil
	}
	if len(automations) > model.MaxLeaderboardFanOut {
		// Truncated by the LIMIT, so it is neither a complete list to count nor
		// a trustworthy name source — an automation past the cut would render
		// as a blank row. Drop it and let the name lookup handle the few ids
		// that actually make the ranking.
		automations = nil
	}
	// The query already restricts to the user's automation filter, so counting
	// the ones it excludes would return zeroes at full price.
	if len(automations) > 0 && len(filter.WorkflowIDs) > 0 {
		selected := make(map[string]string, len(filter.WorkflowIDs))
		for _, workflowID := range filter.WorkflowIDs {
			if name, ok := automations[workflowID]; ok {
				selected[workflowID] = name
			}
		}
		automations = selected
	}

	scanPages := int((failedTotal + model.MaxLeaderboardScanRows - 1) / model.MaxLeaderboardScanRows)
	canScan := scanPages <= model.MaxLeaderboardScanPages
	canFanOut := len(automations) > 0

	// Prefer whichever costs fewer round trips. On a tenant with 8,000 failures
	// across 300 automations that is 8 calls instead of 300.
	if canScan && (!canFanOut || scanPages <= len(automations)) {
		tally, scanErr := s.tallyFailuresByScan(ctx.GetContext(), query, scanPages)
		if scanErr == nil {
			return s.nameRanked(ctx, tenantID, filter, automations, rankFailedAutomations(tally, limit)), false, nil
		}
		slog.Warn("failed to tally failures by paging, trying per-automation counts", "error", scanErr)
	}

	if canFanOut {
		if ranked, complete := s.countFailuresPerAutomation(ctx.GetContext(), query, automations, limit); complete {
			return ranked, false, nil
		}
		// A ranking built from some of the counts is not a smaller truth, it is
		// a wrong order — the automation whose count timed out could have been
		// first. Fall through to the approximation, which at least says so.
		slog.Warn("leaderboard fan-out incomplete, falling back to first-page tally", "automations", len(automations))
	}

	resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
		Query:    query,
		PageSize: model.MaxLeaderboardScanRows,
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to scan failed executions: %w", err)
	}

	tally := map[string]int64{}
	for _, info := range resp.GetExecutions() {
		if workflowID := s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrWorkflowID); workflowID != "" {
			tally[workflowID]++
		}
	}

	ranked := s.nameRanked(ctx, tenantID, filter, automations, rankFailedAutomations(tally, limit))
	return ranked, len(resp.GetNextPageToken()) > 0, nil
}

// tallyFailuresByScan pages through every failure the filter matches and counts
// them per automation.
//
// One call covers 1,000 failures, which makes this far cheaper than one call
// per automation whenever a tenant has more automations than it has thousands
// of failures — the common shape, since automations accumulate slowly and
// failures accumulate fast.
func (s *Service) tallyFailuresByScan(ctx context.Context, query string, maxPages int) (map[string]int64, error) {
	tally := map[string]int64{}
	var token []byte
	// One page past the estimate: the count that produced it is approximate by
	// Temporal's own admission, and running one page short would silently drop
	// failures from the ranking.
	for page := 0; page <= maxPages; page++ {
		resp, err := s.temporalClient.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         query,
			PageSize:      model.MaxLeaderboardScanRows,
			NextPageToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to page failed executions: %w", err)
		}
		for _, info := range resp.GetExecutions() {
			if workflowID := s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrWorkflowID); workflowID != "" {
				tally[workflowID]++
			}
		}
		token = resp.GetNextPageToken()
		if len(token) == 0 {
			return tally, nil
		}
	}
	// More pages than the count said there would be. Reporting a tally that
	// stopped early as exact is the bug this whole path exists to fix.
	return nil, fmt.Errorf("failures did not fit in %d pages", maxPages)
}

// nameRanked fills in workflow names, preferring the automation list already in
// hand and falling back to a lookup when it could not be read.
func (s *Service) nameRanked(
	ctx *security.RequestContext,
	tenantID string,
	filter model.ExecutionDashboardFilter,
	automations map[string]string,
	ranked []model.FailedAutomationCount,
) []model.FailedAutomationCount {
	if len(ranked) == 0 {
		return ranked
	}
	if len(automations) > 0 {
		for i := range ranked {
			ranked[i].WorkflowName = automations[ranked[i].WorkflowID]
		}
		return ranked
	}
	ids := make([]string, 0, len(ranked))
	for _, entry := range ranked {
		ids = append(ids, entry.WorkflowID)
	}
	names, nameErr := s.store.GetWorkflowNames(ctx.GetContext(), tenantID, filter.AccountIDs, ids)
	if nameErr != nil {
		ctx.GetLogger().Error("failed to get workflow names for leaderboard", "error", nameErr)
		return ranked
	}
	for i := range ranked {
		ranked[i].WorkflowName = names[ranked[i].WorkflowID]
	}
	return ranked
}

// countFailuresPerAutomation asks the visibility store how many failures each
// automation has, one CountWorkflow each, and ranks the answers.
//
// Cost scales with the number of automations rather than the number of
// failures, and each call returns a single integer instead of a page of
// visibility rows — so this stays the same size whether the window holds a
// hundred failures or a hundred thousand.
//
// An automation that no longer exists is absent from `automations` and so
// drops out of the ranking. The scan showed those with a blank name, which was
// not more useful.
//
// The bool reports whether every count came back. It is false as soon as one
// did not: a ranking assembled from a subset of the counts is not a partial
// answer, it is a wrong order, and the caller must not present it as exact.
func (s *Service) countFailuresPerAutomation(
	ctx context.Context,
	query string,
	automations map[string]string,
	limit int,
) ([]model.FailedAutomationCount, bool) {
	var mu sync.Mutex
	tally := map[string]int64{}
	complete := true

	var wg sync.WaitGroup
	slots := make(chan struct{}, model.LeaderboardCountConcurrency)
	for workflowID := range automations {
		wg.Add(1)
		go func(workflowID string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				complete = false
				mu.Unlock()
				return
			}
			defer func() { <-slots }()

			scoped := query + fmt.Sprintf(" AND %s='%s'", model.SearchAttrWorkflowID, escapeTemporalString(workflowID))
			resp, err := s.temporalClient.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{Query: scoped})
			if err != nil {
				slog.Warn("failed to count failures for automation", "error", err, "workflow_id", workflowID)
				mu.Lock()
				complete = false
				mu.Unlock()
				return
			}
			// Zero-failure automations are the majority and would only pad the
			// ranking with empty rows.
			if count := resp.GetCount(); count > 0 {
				mu.Lock()
				tally[workflowID] = count
				mu.Unlock()
			}
		}(workflowID)
	}
	wg.Wait()

	if !complete {
		return nil, false
	}
	ranked := rankFailedAutomations(tally, limit)
	for i := range ranked {
		ranked[i].WorkflowName = automations[ranked[i].WorkflowID]
	}
	return ranked, true
}

// rankFailedAutomations orders a tally by failure count, descending, and keeps
// the top `limit`. Ties break on workflow id so the ranking is stable across
// refreshes rather than reshuffling on every poll.
func rankFailedAutomations(tally map[string]int64, limit int) []model.FailedAutomationCount {
	ranked := make([]model.FailedAutomationCount, 0, len(tally))
	for workflowID, count := range tally {
		ranked = append(ranked, model.FailedAutomationCount{WorkflowID: workflowID, FailureCount: count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].FailureCount != ranked[j].FailureCount {
			return ranked[i].FailureCount > ranked[j].FailureCount
		}
		return ranked[i].WorkflowID < ranked[j].WorkflowID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// executionSummaryFromInfo maps a visibility record onto the shared summary
// shape, pulling workflow id / trigger / user out of the search attributes.
func (s *Service) executionSummaryFromInfo(info *workflowpb.WorkflowExecutionInfo) model.WorkflowExecutionSummary {
	summary := model.WorkflowExecutionSummary{
		TemporalWorkflowID: info.GetExecution().GetWorkflowId(),
		ID:                 info.GetExecution().GetRunId(),
		Status:             mapTemporalStatusToModelStatus(info.GetStatus()),
		StartTime:          timestampPBToTimestamp(info.GetStartTime()),
		CloseTime:          timestampPBToTimestamp(info.GetCloseTime()),
		WorkflowID:         s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrWorkflowID),
		TriggeredBy:        s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrTriggeredBy),
		TriggerType:        s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrWorkflowTrigger),
		ParentWorkflowID:   s.searchAttrString(info.GetSearchAttributes(), model.SearchAttrParentWorkflowID),
	}
	if info.GetMemo() != nil {
		if payload, ok := info.GetMemo().GetFields()[model.MemoWorkflowVersionNumber]; ok {
			var raw any
			if err := s.dataConverter.FromPayload(payload, &raw); err == nil {
				if version, ok := toInt(raw); ok {
					summary.Version = &version
					summary.VersionNumber = &version
				}
			}
		}
	}
	return summary
}

// searchAttrString decodes one keyword search attribute, returning "" when the
// attribute is absent or undecodable.
func (s *Service) searchAttrString(attrs *commonpb.SearchAttributes, key string) string {
	if attrs == nil {
		return ""
	}
	payload, ok := attrs.GetIndexedFields()[key]
	if !ok {
		return ""
	}
	var value string
	if err := s.dataConverter.FromPayload(payload, &value); err != nil {
		return ""
	}
	return value
}

func (s *Service) resolveWorkflowNames(ctx *security.RequestContext, tenantID string, accountIDs []string, rows []model.AccountExecutionSummary) {
	ids := distinctNonEmpty(len(rows), func(i int) string { return rows[i].WorkflowID })
	if len(ids) == 0 {
		return
	}
	names, err := s.store.GetWorkflowNames(ctx.GetContext(), tenantID, accountIDs, ids)
	if err != nil {
		ctx.GetLogger().Error("failed to get workflow names for executions", "error", err)
		return
	}
	for i := range rows {
		rows[i].WorkflowName = names[rows[i].WorkflowID]
	}
}

// resolveUserNames turns nb_triggered_by (a user id) into a display name.
// Runs no person started carry either no id or the nil-UUID system user, and
// have no row in `users`; those are left empty for the UI to label.
func (s *Service) resolveUserNames(ctx *security.RequestContext, rows []model.AccountExecutionSummary) {
	ids := distinctNonEmpty(len(rows), func(i int) string {
		if rows[i].TriggeredBy == systemUserID {
			return ""
		}
		return rows[i].TriggeredBy
	})
	if len(ids) == 0 {
		return
	}
	names, err := s.store.GetUserNames(ctx.GetContext(), ids)
	if err != nil {
		ctx.GetLogger().Error("failed to get user names for executions", "error", err)
		return
	}
	for i := range rows {
		rows[i].UserName = names[rows[i].TriggeredBy]
	}
}

// resolveFailureReasons fills in why each failed run failed.
//
// Visibility records carry no error text, so the close event has to be fetched
// per row. Only non-successful rows are looked up, concurrency and total time
// are bounded, and a miss simply leaves the field empty — the full error is
// always available from workflow_get_execution.
func (s *Service) resolveFailureReasons(ctx *security.RequestContext, rows []model.AccountExecutionSummary) {
	targets := make([]int, 0, len(rows))
	for i := range rows {
		if rows[i].TemporalWorkflowID == "" || rows[i].ID == "" {
			continue
		}
		for _, status := range model.FailedExecutionStatuses {
			if rows[i].Status == status {
				targets = append(targets, i)
				break
			}
		}
	}
	if len(targets) == 0 {
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx.GetContext(), failureReasonFetchBudget)
	defer cancel()

	var wg sync.WaitGroup
	slots := make(chan struct{}, failureReasonFetchConcurrency)
	for _, idx := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Select rather than a bare send: once the budget expires the
			// queued goroutines exit immediately instead of each taking a turn
			// through the semaphore to do nothing.
			select {
			case slots <- struct{}{}:
			case <-fetchCtx.Done():
				return
			}
			defer func() { <-slots }()
			rows[i].FailureReason = s.closeEventFailureReason(fetchCtx, rows[i].TemporalWorkflowID, rows[i].ID)
		}(idx)
	}
	wg.Wait()
}

// closeEventFailureReason reads only the run's close event, which is a single
// history page, and renders it as a one-line reason.
func (s *Service) closeEventFailureReason(ctx context.Context, temporalWorkflowID, runID string) string {
	iter := s.temporalClient.GetWorkflowHistory(ctx, temporalWorkflowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
	if !iter.HasNext() {
		return ""
	}
	event, err := iter.Next()
	if err != nil {
		return ""
	}
	switch {
	case event.GetWorkflowExecutionFailedEventAttributes() != nil:
		return formatFailureMessage(event.GetWorkflowExecutionFailedEventAttributes().GetFailure())
	case event.GetWorkflowExecutionTerminatedEventAttributes() != nil:
		if reason := event.GetWorkflowExecutionTerminatedEventAttributes().GetReason(); reason != "" {
			return reason
		}
		return "Execution terminated"
	case event.GetWorkflowExecutionTimedOutEventAttributes() != nil:
		return "Execution timed out"
	default:
		return ""
	}
}

// formatFailureMessage renders a failure as `message | Cause: …`. The stack
// trace is deliberately omitted — this string is a table cell, and the full
// error (with stack) is served by workflow_get_execution.
func formatFailureMessage(failure *failurepb.Failure) string {
	if failure == nil {
		return "Unknown failure (no details)"
	}
	var sb strings.Builder
	sb.WriteString(failure.GetMessage())
	if cause := failure.GetCause(); cause != nil {
		if causeMsg := cause.GetMessage(); causeMsg != "" {
			sb.WriteString(" | Cause: ")
			sb.WriteString(causeMsg)
		}
	}
	return sb.String()
}

// distinctNonEmpty collects the unique non-empty values produced by get.
func distinctNonEmpty(n int, get func(int) string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for i := 0; i < n; i++ {
		value := get(i)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// executionRetentionDays reports the Temporal namespace retention, which is the
// hard ceiling on how far back the dashboard can look. Cached after the first
// successful read; zero means "unknown", and the UI then leaves its date range
// unclamped rather than guessing.
func (s *Service) executionRetentionDays(ctx context.Context) int {
	s.retentionMu.RLock()
	cached := s.retentionDays
	s.retentionMu.RUnlock()
	if cached > 0 {
		return cached
	}

	resp, err := s.temporalClient.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{
		Namespace: client.DefaultNamespace,
	})
	if err != nil {
		slog.Warn("failed to read temporal namespace retention", "error", err)
		return 0
	}
	ttl := resp.GetConfig().GetWorkflowExecutionRetentionTtl()
	if ttl == nil {
		return 0
	}
	days := int(ttl.AsDuration().Hours() / 24)
	if days <= 0 {
		return 0
	}

	s.retentionMu.Lock()
	s.retentionDays = days
	s.retentionMu.Unlock()
	return days
}
