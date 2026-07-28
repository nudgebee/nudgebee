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
//     Temporal's default ordering is StartTime DESC, which is what the
//     dashboard wants anyway.
//   - Multi-value filters render as OR groups rather than IN. OR is what the
//     rest of this service already emits against this store.
//
// Tenant and account are always applied and always come from the caller's
// security context, never from request args.
func buildExecutionQuery(tenantID string, f model.ExecutionDashboardFilter) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenantID is required")
	}
	if f.AccountID == "" {
		return "", fmt.Errorf("account_id is required")
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
		fmt.Sprintf("%s='%s'", model.SearchAttrAccountID, escapeTemporalString(f.AccountID)),
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
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountID, security.SecurityAccessTypeRead) {
		return model.ListAccountExecutionsResponse{}, common.ErrorUnauthorized("account not accessible")
	}
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
	// click a page number. When a token is available it is used directly;
	// otherwise page N is served by one over-fetch of N*limit rows which is
	// then sliced. That is a single round trip, not N of them.
	pageSize := limit
	skip := 0
	if req.NextPageToken == "" && req.Page > 1 {
		if req.Page*limit > model.MaxExecutionDeepPageRows {
			return model.ListAccountExecutionsResponse{}, common.ErrorBadRequest(
				fmt.Sprintf("page too deep — at most %d rows can be paged through; narrow the date range or filters", model.MaxExecutionDeepPageRows))
		}
		pageSize = req.Page * limit
		skip = (req.Page - 1) * limit
	}

	resp, err := s.temporalClient.ListWorkflow(ctx.GetContext(), &workflowservice.ListWorkflowExecutionsRequest{
		Query:         query,
		PageSize:      int32(pageSize),
		NextPageToken: []byte(req.NextPageToken),
	})
	if err != nil {
		slog.Error("failed to list account executions", "error", err, "query", query)
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
		row := model.AccountExecutionSummary{WorkflowExecutionSummary: summary}
		if summary.StartTime != nil && summary.CloseTime != nil {
			durationMs := summary.CloseTime.Sub(*summary.StartTime).Milliseconds()
			row.DurationMs = &durationMs
		}
		rows = append(rows, row)
	}

	s.resolveWorkflowNames(ctx, tenantID, req.AccountID, rows)
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

// AggregateExecutions returns the dashboard's summary metrics plus the
// most-failed-automation leaderboard, over the same filter as the list.
func (s *Service) AggregateExecutions(ctx *security.RequestContext, req model.AggregateExecutionsRequest) (model.AggregateExecutionsResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountID, security.SecurityAccessTypeRead) {
		return model.AggregateExecutionsResponse{}, common.ErrorUnauthorized("account not accessible")
	}
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
		CountsAreApproximate: true,
		TopFailed:            []model.FailedAutomationCount{},
		RetentionDays:        s.executionRetentionDays(ctx.GetContext()),
	}

	// A count of zero for an intersected-away bucket is correct, not a bug: a
	// user filtering to COMPLETED genuinely has no failures in view.
	if len(failedStatuses) > 0 {
		topFailed, approximate, err := s.topFailedAutomations(ctx, tenantID, req.ExecutionDashboardFilter, failedStatuses, topFailedLimit)
		if err != nil {
			slog.Warn("failed to build most-failed leaderboard", "error", err)
		} else {
			response.TopFailed = topFailed
			response.TopFailedIsApproximate = approximate
		}
	}

	return response, nil
}

// topFailedAutomations ranks automations by failure count within the filter.
//
// Temporal cannot GROUP BY a custom search attribute, and one CountWorkflow per
// automation would be 50-200 gRPC calls. Instead a single ListWorkflow pulls
// the failures and they are tallied in-process. That is exact whenever the
// window holds no more than MaxLeaderboardScanRows failures, which is the
// normal case; beyond that the caller is told the ranking is approximate.
func (s *Service) topFailedAutomations(
	ctx *security.RequestContext,
	tenantID string,
	filter model.ExecutionDashboardFilter,
	failedStatuses []model.WorkflowExecutionStatus,
	limit int,
) ([]model.FailedAutomationCount, bool, error) {
	filter.Statuses = failedStatuses
	query, err := buildExecutionQuery(tenantID, filter)
	if err != nil {
		return nil, false, err
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

	ranked := make([]model.FailedAutomationCount, 0, len(tally))
	for workflowID, count := range tally {
		ranked = append(ranked, model.FailedAutomationCount{WorkflowID: workflowID, FailureCount: count})
	}
	// Ties break on workflow id so the ranking is stable across refreshes.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].FailureCount != ranked[j].FailureCount {
			return ranked[i].FailureCount > ranked[j].FailureCount
		}
		return ranked[i].WorkflowID < ranked[j].WorkflowID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	if len(ranked) > 0 {
		ids := make([]string, 0, len(ranked))
		for _, entry := range ranked {
			ids = append(ids, entry.WorkflowID)
		}
		names, nameErr := s.store.GetWorkflowNames(ctx.GetContext(), tenantID, filter.AccountID, ids)
		if nameErr != nil {
			ctx.GetLogger().Error("failed to get workflow names for leaderboard", "error", nameErr)
		} else {
			for i := range ranked {
				ranked[i].WorkflowName = names[ranked[i].WorkflowID]
			}
		}
	}

	return ranked, len(resp.GetNextPageToken()) > 0, nil
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

func (s *Service) resolveWorkflowNames(ctx *security.RequestContext, tenantID, accountID string, rows []model.AccountExecutionSummary) {
	ids := distinctNonEmpty(len(rows), func(i int) string { return rows[i].WorkflowID })
	if len(ids) == 0 {
		return
	}
	names, err := s.store.GetWorkflowNames(ctx.GetContext(), tenantID, accountID, ids)
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
