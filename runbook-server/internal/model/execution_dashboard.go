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
	// MaxExecutionDeepPageRows caps random-access ("jump to page N") paging.
	// Temporal only offers forward-only cursors, so page N is served by
	// over-fetching N*limit rows and slicing. Past this the request is refused.
	MaxExecutionDeepPageRows = 1000
	// MaxLeaderboardScanRows caps the single failure scan behind the
	// most-failed leaderboard. Temporal cannot group by nb_workflow_id, so the
	// failures are tallied in-process; beyond this the result is approximate.
	MaxLeaderboardScanRows = 1000
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
	AccountID    string                    `json:"account_id"`
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
	// CountsAreApproximate mirrors Temporal's own caveat on CountWorkflow.
	CountsAreApproximate bool                    `json:"counts_are_approximate"`
	TopFailed            []FailedAutomationCount `json:"top_failed"`
	// TopFailedIsApproximate is set when more failures matched the filter than
	// MaxLeaderboardScanRows, so the ranking only covers the scanned prefix.
	TopFailedIsApproximate bool `json:"top_failed_is_approximate"`
	// RetentionDays is the Temporal namespace retention. Executions older than
	// this do not exist, so the UI must clamp its date picker to it. Zero means
	// the retention could not be read — the UI should then not clamp.
	RetentionDays int `json:"retention_days"`
}
