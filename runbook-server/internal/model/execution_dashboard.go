package model

import "time"

// Bounds for the cross-automation execution dashboard.
//
// Executions live in Temporal's visibility store, which here is SQL
// (postgres12) rather than Elasticsearch. That store cannot ORDER BY, cannot
// GROUP BY a custom search attribute, and reports counts as approximate, so
// every limit below exists to keep a single dashboard request from turning
// into a pathological visibility query.
const (
	// MaxExecutionFilterValues caps how many values a multi-select filter may
	// carry. Each value becomes another OR term in the visibility query.
	MaxExecutionFilterValues = 50
	// DefaultExecutionPageSize / MaxExecutionPageSize bound the rows per page.
	DefaultExecutionPageSize = 20
	MaxExecutionPageSize     = 100
	// MaxExecutionDeepPageRows caps how many rows a single list call may
	// over-fetch and slice. Inside it, page N is served by one over-fetch of
	// N*limit rows; past it the page is located by close-time seek instead,
	// which only ever over-fetches one page's worth.
	MaxExecutionDeepPageRows = 1000
	// MaxExecutionSeekProbes bounds the close-time search behind a deep page
	// jump. Each probe is one CountWorkflow — tens of ms from inside the
	// cluster, ~350ms measured across a port-forward — so this is the
	// worst-case latency budget for locating a page.
	//
	// Unfiltered pages on dev converge in 3-6 probes. A filter that selects a
	// bursty subset — one automation that ran hard for an hour — is the slow
	// case and needed 8, so the budget sits above the worst measured run rather
	// than on it. Overrunning is not an error, only a wider slice.
	MaxExecutionSeekProbes = 12
	// Temporal cannot GROUP BY nb_workflow_id, so the most-failed leaderboard
	// has to be assembled client-side. Two ways to do that, and which is
	// cheaper depends on the tenant's shape, because both cost about the same
	// per round trip:
	//
	//   - Page through the failures and tally: ceil(failures / 1000) calls.
	//   - Count each automation separately:    one call per automation.
	//
	// Both are exact. The dashboard measures each against these caps and picks
	// the cheaper, so a tenant with many failures and few automations and a
	// tenant with the reverse shape both stay fast. Only when neither fits does
	// the single-page approximation come back, and it says so.

	// MaxLeaderboardScanRows is the page size for the tallying path, and the
	// row cap on the approximate fallback.
	MaxLeaderboardScanRows = 1000
	// MaxLeaderboardScanPages bounds the tallying path. 25 pages is 25,000
	// failures; past that the fan-out is likely cheaper anyway.
	MaxLeaderboardScanPages = 25
	// MaxLeaderboardFanOut caps the per-automation counting path.
	//
	// Sized against a real tenant, not a guess: the dev tenant holds 323
	// automations, and a CountWorkflow is ~85ms in-cluster (~360ms across a
	// port-forward), so counting all of them costs tens of seconds — measurably
	// worse than the 9 list calls the same tenant's 8,069 failures need.
	// Fanning out only wins when automations are few relative to failures.
	MaxLeaderboardFanOut = 200
	// LeaderboardCountConcurrency bounds the parallel CountWorkflow calls.
	// Measured on dev, raising this past 8 bought almost nothing (33.6s at 8
	// vs 25.1s at 64 for 323 counts) and multiplied deadline-exceeded errors
	// from 2 to 85 — the visibility store does not parallelise these, so extra
	// concurrency only deepens the queue.
	LeaderboardCountConcurrency = 8
	// DefaultTopFailedLimit is the leaderboard length.
	DefaultTopFailedLimit = 5
	MaxTopFailedLimit     = 20
)

// FailedExecutionStatuses are the statuses that count as "did not succeed" on
// the dashboard. A cancelled run is deliberately excluded — it failed to
// finish, but not because the automation broke.
var FailedExecutionStatuses = []WorkflowExecutionStatus{
	WorkflowExecutionStatusFailed,
	WorkflowExecutionStatusTerminated,
	WorkflowExecutionStatusTimedOut,
}

// ExecutionDashboardFilter is the filter set shared by the execution list and
// the aggregate. Both build their visibility query from this same struct so a
// summary metric can never disagree with the rows below it.
type ExecutionDashboardFilter struct {
	// AccountIDs narrows the dashboard to specific accounts. Empty means every
	// account the caller can read — the Automations page is tenant-level
	// (#35113), so the Executions tab spans accounts the same way the listing
	// does.
	AccountIDs []string `json:"account_ids,omitempty"`
	// TenantWide marks AccountIDs as "every account in the tenant", letting the
	// query scope by tenant alone instead of OR-ing every account id. Resolved
	// server-side; never read off the wire.
	TenantWide   bool                      `json:"-"`
	StartDate    *time.Time                `json:"start_date,omitempty"`
	EndDate      *time.Time                `json:"end_date,omitempty"`
	WorkflowIDs  []string                  `json:"workflow_ids,omitempty"`
	TriggeredBy  []string                  `json:"triggered_by,omitempty"`
	Statuses     []WorkflowExecutionStatus `json:"statuses,omitempty"`
	TriggerTypes []string                  `json:"trigger_types,omitempty"`
}

// ListAccountExecutionsRequest asks for one page of executions across every
// automation in an account.
type ListAccountExecutionsRequest struct {
	ExecutionDashboardFilter
	Limit int `json:"limit,omitempty"`
	// Page is 1-based and only consulted when NextPageToken is empty.
	Page int `json:"page,omitempty"`
	// NextPageToken is Temporal's opaque forward cursor — the cheap path.
	NextPageToken string `json:"next_page_token,omitempty"`
	// IncludeFailureReason toggles the per-row close-event lookup that
	// resolves why a run failed. Visibility records carry no error text.
	IncludeFailureReason bool `json:"include_failure_reason,omitempty"`
}

// AccountExecutionSummary is a dashboard row: the standard execution summary
// plus the fields the dashboard shows that visibility alone cannot supply.
type AccountExecutionSummary struct {
	WorkflowExecutionSummary
	// AccountID of the run's own account — a page can span several, so the UI
	// labels each row and links it back to the right account.
	AccountID string `json:"account_id,omitempty"`
	// DurationMs is nil while the run is still open.
	DurationMs *int64 `json:"duration_ms,omitempty"`
	// UserName resolves TriggeredBy (a user id) to a display name. Empty for
	// scheduled runs, which have no triggering user.
	UserName string `json:"user_name,omitempty"`
	// FailureReason is best-effort: the close event's failure message without
	// the stack trace. The full error stays on workflow_get_execution.
	FailureReason string `json:"failure_reason,omitempty"`
}

type ListAccountExecutionsResponse struct {
	Executions    []AccountExecutionSummary `json:"executions"`
	NextPageToken string                    `json:"next_page_token,omitempty"`
	TotalCount    int64                     `json:"total_count"`
	// TotalIsApproximate is always true — Temporal documents its count API as
	// approximate. Surfaced so the UI can render "≈ N" honestly.
	TotalIsApproximate bool `json:"total_is_approximate"`
}

// AggregateExecutionsRequest asks for the summary metrics and the most-failed
// leaderboard over the same filter as the list.
type AggregateExecutionsRequest struct {
	ExecutionDashboardFilter
	TopFailedLimit int `json:"top_failed_limit,omitempty"`
}

// FailedAutomationCount is one row of the most-failed leaderboard.
type FailedAutomationCount struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name,omitempty"`
	FailureCount int64  `json:"failure_count"`
}

type AggregateExecutionsResponse struct {
	Total     int64 `json:"total"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Running   int64 `json:"running"`
	// TimedOut is a subset of Failed, broken out because "ran too long" and
	// "the automation broke" are different problems with different fixes.
	TimedOut int64 `json:"timed_out"`
	// CountsAreApproximate mirrors Temporal's own caveat on CountWorkflow.
	CountsAreApproximate bool                    `json:"counts_are_approximate"`
	TopFailed            []FailedAutomationCount `json:"top_failed"`
	// TopFailedIsApproximate is set only on the fallback scan path, when more
	// failures matched the filter than MaxLeaderboardScanRows and the ranking
	// therefore covers a prefix. The normal path counts per automation and is
	// exact at any failure volume.
	TopFailedIsApproximate bool `json:"top_failed_is_approximate"`
	// RetentionDays is the Temporal namespace retention. Executions older than
	// this do not exist, so the UI must clamp its date picker to it. Zero means
	// the retention could not be read — the UI should then not clamp.
	RetentionDays int `json:"retention_days"`
}
