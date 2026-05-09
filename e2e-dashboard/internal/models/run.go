package models

import "time"

// TestRun represents a single E2E test execution
type TestRun struct {
	ID           string     `json:"id"`
	Environment  string     `json:"environment"`
	Status       string     `json:"status"` // running, passed, failed, cancelled
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	TotalTests   int        `json:"total_tests"`
	Passed       int        `json:"passed"`
	Failed       int        `json:"failed"`
	Skipped      int        `json:"skipped"`
	DurationMs   int64      `json:"duration_ms"`
	GithubRunURL string     `json:"github_run_url,omitempty"`
}

// CreateRunRequest is the request body for creating a new test run
type CreateRunRequest struct {
	Environment  string `json:"environment" binding:"required"`
	GithubRunURL string `json:"github_run_url,omitempty"`
}

// UpdateRunRequest is the request body for updating a test run
type UpdateRunRequest struct {
	Status      string `json:"status,omitempty"`
	TotalTests  *int   `json:"total_tests,omitempty"`
	Passed      *int   `json:"passed,omitempty"`
	Failed      *int   `json:"failed,omitempty"`
	Skipped     *int   `json:"skipped,omitempty"`
	DurationMs  *int64 `json:"duration_ms,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// RunsListResponse is the response for listing test runs
type RunsListResponse struct {
	Runs  []TestRun `json:"runs"`
	Total int       `json:"total"`
}

// RunWithResults includes the test run with all its results
type RunWithResults struct {
	TestRun
	Results []TestResult `json:"results"`
}
