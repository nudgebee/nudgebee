package models

import "time"

// TrendDataPoint represents a single point in the trends chart
type TrendDataPoint struct {
	Date        time.Time `json:"date"`
	Environment string    `json:"environment"`
	TotalRuns   int       `json:"total_runs"`
	PassedRuns  int       `json:"passed_runs"`
	FailedRuns  int       `json:"failed_runs"`
	PassRate    float64   `json:"pass_rate"`
	AvgDuration int64     `json:"avg_duration_ms"`
}

// TrendsResponse is the response for the trends endpoint
type TrendsResponse struct {
	Data []TrendDataPoint `json:"data"`
}

// FailureStats represents common failure patterns
type FailureStats struct {
	TestFile     string `json:"test_file"`
	TestName     string `json:"test_name"`
	FailureCount int    `json:"failure_count"`
	LastFailed   string `json:"last_failed"`
}

// DashboardSummary provides overview stats
type DashboardSummary struct {
	TotalRuns       int     `json:"total_runs"`
	PassedRuns      int     `json:"passed_runs"`
	FailedRuns      int     `json:"failed_runs"`
	OverallPassRate float64 `json:"overall_pass_rate"`
	LastRunStatus   string  `json:"last_run_status"`
	LastRunTime     string  `json:"last_run_time"`
}
