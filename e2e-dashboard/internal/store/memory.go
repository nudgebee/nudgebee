package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nudgebee/e2e-dashboard/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides access to the in-memory SQLite database
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

// New creates a new in-memory SQLite store
func New() (*Store, error) {
	// Use shared cache to ensure all connections see the same in-memory database
	// Without this, each connection gets its own separate in-memory database
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared&mode=rwc")
	if err != nil {
		return nil, err
	}

	// Set connection pool to avoid issues with shared in-memory database
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, err
	}

	slog.Info("In-memory SQLite store initialized")
	return store, nil
}

// migrate creates the required tables
func (s *Store) migrate() error {
	schema := `
		CREATE TABLE IF NOT EXISTS test_runs (
			id TEXT PRIMARY KEY,
			environment TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'running',
			started_at DATETIME NOT NULL,
			completed_at DATETIME,
			total_tests INTEGER DEFAULT 0,
			passed INTEGER DEFAULT 0,
			failed INTEGER DEFAULT 0,
			skipped INTEGER DEFAULT 0,
			duration_ms INTEGER DEFAULT 0,
			github_run_url TEXT
		);

		CREATE TABLE IF NOT EXISTS test_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES test_runs(id) ON DELETE CASCADE,
			test_file TEXT NOT NULL,
			test_name TEXT NOT NULL,
			status TEXT NOT NULL,
			duration_ms INTEGER DEFAULT 0,
			error_message TEXT,
			stack_trace TEXT,
			screenshot_url TEXT,
			video_url TEXT,
			trace_url TEXT,
			retry_count INTEGER DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_runs_environment ON test_runs(environment);
		CREATE INDEX IF NOT EXISTS idx_runs_started_at ON test_runs(started_at);
		CREATE INDEX IF NOT EXISTS idx_results_run_id ON test_results(run_id);
		CREATE INDEX IF NOT EXISTS idx_results_status ON test_results(status);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateRun creates a new test run
func (s *Store) CreateRun(run *models.TestRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		INSERT INTO test_runs (id, environment, status, started_at, github_run_url)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query, run.ID, run.Environment, run.Status, run.StartedAt, run.GithubRunURL)
	return err
}

// GetRun retrieves a test run by ID
func (s *Store) GetRun(id string) (*models.TestRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, environment, status, started_at, completed_at,
		       total_tests, passed, failed, skipped, duration_ms, github_run_url
		FROM test_runs WHERE id = ?
	`

	run := &models.TestRun{}
	var completedAt sql.NullTime
	var githubURL sql.NullString

	err := s.db.QueryRow(query, id).Scan(
		&run.ID, &run.Environment, &run.Status, &run.StartedAt, &completedAt,
		&run.TotalTests, &run.Passed, &run.Failed, &run.Skipped, &run.DurationMs, &githubURL,
	)
	if err != nil {
		return nil, err
	}

	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	if githubURL.Valid {
		run.GithubRunURL = githubURL.String
	}

	return run, nil
}

// UpdateRun updates an existing test run
func (s *Store) UpdateRun(id string, req *models.UpdateRunRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		UPDATE test_runs
		SET status = COALESCE(NULLIF(?, ''), status),
		    total_tests = COALESCE(?, total_tests),
		    passed = COALESCE(?, passed),
		    failed = COALESCE(?, failed),
		    skipped = COALESCE(?, skipped),
		    duration_ms = COALESCE(?, duration_ms),
		    completed_at = CASE WHEN ? != '' THEN ? ELSE completed_at END
		WHERE id = ?
	`

	_, err := s.db.Exec(query,
		req.Status,
		req.TotalTests,
		req.Passed,
		req.Failed,
		req.Skipped,
		req.DurationMs,
		req.CompletedAt, req.CompletedAt,
		id,
	)
	return err
}

// ListRuns retrieves test runs with optional filters
func (s *Store) ListRuns(environment string, limit, offset int) ([]models.TestRun, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var runs []models.TestRun
	var total int

	// Count query
	countQuery := "SELECT COUNT(*) FROM test_runs"
	if environment != "" {
		countQuery += " WHERE environment = ?"
		err := s.db.QueryRow(countQuery, environment).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
	} else {
		err := s.db.QueryRow(countQuery).Scan(&total)
		if err != nil {
			return nil, 0, err
		}
	}

	// List query
	query := `
		SELECT id, environment, status, started_at, completed_at,
		       total_tests, passed, failed, skipped, duration_ms, github_run_url
		FROM test_runs
	`
	var args []interface{}
	if environment != "" {
		query += " WHERE environment = ?"
		args = append(args, environment)
	}
	query += " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var run models.TestRun
		var completedAt sql.NullTime
		var githubURL sql.NullString

		err := rows.Scan(
			&run.ID, &run.Environment, &run.Status, &run.StartedAt, &completedAt,
			&run.TotalTests, &run.Passed, &run.Failed, &run.Skipped, &run.DurationMs, &githubURL,
		)
		if err != nil {
			return nil, 0, err
		}

		if completedAt.Valid {
			run.CompletedAt = &completedAt.Time
		}
		if githubURL.Valid {
			run.GithubRunURL = githubURL.String
		}

		runs = append(runs, run)
	}

	return runs, total, nil
}

// CreateResults batch inserts test results
func (s *Store) CreateResults(runID string, results []models.CreateResultRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO test_results (run_id, test_file, test_name, status, duration_ms, error_message, stack_trace, screenshot_url, video_url, trace_url, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range results {
		_, err := stmt.Exec(runID, r.TestFile, r.TestName, r.Status, r.DurationMs, r.ErrorMessage, r.StackTrace, r.ScreenshotURL, r.VideoURL, r.TraceURL, r.RetryCount)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetResultsByRunID retrieves all results for a test run
func (s *Store) GetResultsByRunID(runID string) ([]models.TestResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, run_id, test_file, test_name, status, duration_ms, error_message,
		       stack_trace, screenshot_url, video_url, trace_url, retry_count
		FROM test_results WHERE run_id = ?
		ORDER BY id
	`

	rows, err := s.db.Query(query, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []models.TestResult
	for rows.Next() {
		var r models.TestResult
		var errMsg, stackTrace, screenshotURL, videoURL, traceURL sql.NullString
		var retryCount sql.NullInt64

		err := rows.Scan(&r.ID, &r.RunID, &r.TestFile, &r.TestName, &r.Status, &r.DurationMs,
			&errMsg, &stackTrace, &screenshotURL, &videoURL, &traceURL, &retryCount)
		if err != nil {
			return nil, err
		}

		if errMsg.Valid {
			r.ErrorMessage = errMsg.String
		}
		if stackTrace.Valid {
			r.StackTrace = stackTrace.String
		}
		if screenshotURL.Valid {
			r.ScreenshotURL = screenshotURL.String
		}
		if videoURL.Valid {
			r.VideoURL = videoURL.String
		}
		if traceURL.Valid {
			r.TraceURL = traceURL.String
		}
		if retryCount.Valid {
			r.RetryCount = int(retryCount.Int64)
		}

		results = append(results, r)
	}

	return results, nil
}

// GetTrends retrieves pass rate trends for the last N days
func (s *Store) GetTrends(days int, environment string) ([]models.TrendDataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT
			date(started_at) as date,
			environment,
			COUNT(*) as total_runs,
			SUM(CASE WHEN status = 'passed' THEN 1 ELSE 0 END) as passed_runs,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failed_runs,
			AVG(duration_ms) as avg_duration
		FROM test_runs
		WHERE started_at >= datetime('now', ?)
	`

	args := []interface{}{fmt.Sprintf("-%d days", days)}

	if environment != "" {
		query += " AND environment = ?"
		args = append(args, environment)
	}

	query += " GROUP BY date(started_at), environment ORDER BY date DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var trends []models.TrendDataPoint
	for rows.Next() {
		var t models.TrendDataPoint
		var dateStr string
		var avgDuration float64

		err := rows.Scan(&dateStr, &t.Environment, &t.TotalRuns, &t.PassedRuns, &t.FailedRuns, &avgDuration)
		if err != nil {
			return nil, err
		}

		t.Date, _ = time.Parse("2006-01-02", dateStr)
		t.AvgDuration = int64(avgDuration)
		if t.TotalRuns > 0 {
			t.PassRate = float64(t.PassedRuns) / float64(t.TotalRuns) * 100
		}

		trends = append(trends, t)
	}

	return trends, nil
}

// GetSummary retrieves dashboard summary stats
func (s *Store) GetSummary(environment string) (*models.DashboardSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	summary := &models.DashboardSummary{}

	query := `
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status = 'passed' THEN 1 ELSE 0 END), 0) as passed,
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) as failed
		FROM test_runs
	`
	args := []interface{}{}
	if environment != "" {
		query += " WHERE environment = ?"
		args = append(args, environment)
	}

	err := s.db.QueryRow(query, args...).Scan(&summary.TotalRuns, &summary.PassedRuns, &summary.FailedRuns)
	if err != nil {
		return nil, err
	}

	if summary.TotalRuns > 0 {
		summary.OverallPassRate = float64(summary.PassedRuns) / float64(summary.TotalRuns) * 100
	}

	// Get last run
	lastQuery := "SELECT status, started_at FROM test_runs"
	if environment != "" {
		lastQuery += " WHERE environment = ?"
	}
	lastQuery += " ORDER BY started_at DESC LIMIT 1"

	var lastStatus string
	var lastTime time.Time
	if environment != "" {
		err = s.db.QueryRow(lastQuery, environment).Scan(&lastStatus, &lastTime)
	} else {
		err = s.db.QueryRow(lastQuery).Scan(&lastStatus, &lastTime)
	}
	if err == nil {
		summary.LastRunStatus = lastStatus
		summary.LastRunTime = lastTime.Format(time.RFC3339)
	}

	return summary, nil
}
