package models

// TestResult represents a single test case result
type TestResult struct {
	ID            int64  `json:"id"`
	RunID         string `json:"run_id"`
	TestFile      string `json:"test_file"`
	TestName      string `json:"test_name"`
	Status        string `json:"status"` // passed, failed, skipped, timedOut
	DurationMs    int64  `json:"duration_ms"`
	ErrorMessage  string `json:"error_message,omitempty"`
	StackTrace    string `json:"stack_trace,omitempty"`
	ScreenshotURL string `json:"screenshot_url,omitempty"`
	VideoURL      string `json:"video_url,omitempty"`
	TraceURL      string `json:"trace_url,omitempty"`
	RetryCount    int    `json:"retry_count,omitempty"`
}

// BatchResultsRequest is the request body for inserting multiple results
type BatchResultsRequest struct {
	Results []CreateResultRequest `json:"results" binding:"required"`
}

// CreateResultRequest is the request body for creating a single result
type CreateResultRequest struct {
	TestFile      string `json:"test_file" binding:"required"`
	TestName      string `json:"test_name" binding:"required"`
	Status        string `json:"status" binding:"required"`
	DurationMs    int64  `json:"duration_ms"`
	ErrorMessage  string `json:"error_message,omitempty"`
	StackTrace    string `json:"stack_trace,omitempty"`
	ScreenshotURL string `json:"screenshot_url,omitempty"`
	VideoURL      string `json:"video_url,omitempty"`
	TraceURL      string `json:"trace_url,omitempty"`
	RetryCount    int    `json:"retry_count,omitempty"`
}
