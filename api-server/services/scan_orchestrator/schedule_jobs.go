package scan_orchestrator

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// scheduleJobRecord mirrors the legacy Robusta wire shape stored under
// agent.connection_status -> 'schedule_jobs'. Keys are kept verbatim so the UI
// parser, which has read this shape since the Robusta era, continues to work.
type scheduleJobRecord struct {
	JobID            string                 `json:"job_id"`
	RunnableName     string                 `json:"runnable_name"`
	RunnableParams   map[string]any         `json:"runnable_params"`
	SchedulingParams map[string]string      `json:"scheduling_params"`
	State            scheduleJobRecordState `json:"state"`
}

type scheduleJobRecordState struct {
	ExecCount       int   `json:"exec_count"`
	JobStatus       int   `json:"job_status"`
	LastExecTimeSec int64 `json:"last_exec_time_sec"`
}

const (
	// scheduleJobStatusDone is the legacy JobStatus.DONE int value Robusta wrote.
	// We always write DONE — we only update state after a run finishes.
	scheduleJobStatusDone = 2

	// scheduleJobRunnableName is the constant action wrapper Robusta used.
	scheduleJobRunnableName = "scheduled_integration_task"
)

// cronExpressionForScanner resolves the cron tag for either Job-based scanners
// (ScannerCatalog) or direct-API scanners (DirectAPIScanners). Returns "" if
// unknown — caller skips schedule-job tracking in that case.
func cronExpressionForScanner(name string) string {
	if s, ok := ScannerCatalog[name]; ok {
		return s.CronExpression
	}
	if d, ok := DirectAPIScanners[name]; ok {
		return d.CronExpression
	}
	return ""
}

// deterministicJobID generates a stable id for a scanner so the schedule_jobs
// array can be UPSERT'd by id. Legacy Robusta used md5 of the playbook config;
// we use sha1 of the scanner name — equivalent for the UI which only needs a
// stable label per scanner.
func deterministicJobID(scannerName string) string {
	h := sha1.Sum([]byte(scannerName))
	return hex.EncodeToString(h[:16])
}

// UpsertScheduleJobState records that `scannerName` just ran for `account`.
// Inside a single transaction it does SELECT ... FOR UPDATE on the agent row,
// mutates the schedule_jobs array (replacing the entry for this scanner,
// incrementing exec_count), and UPDATEs the same row by id. The row lock
// serializes concurrent writers across api-server replicas as well as
// within a single worker pool — no in-process mutex needed.
//
// Best-effort — errors are logged, not propagated, so a successful scan
// persist isn't undone by a schedule-state hiccup.
func UpsertScheduleJobState(ctx *security.RequestContext, account ScanAccount, scannerName string) {
	logger := ctx.GetLogger().With("scanner", scannerName, "account_id", account.AccountID)

	cron := cronExpressionForScanner(scannerName)
	if cron == "" {
		return // unscheduled scanner (e.g. image_scanner) — nothing to record
	}
	if account.AccountID == "" {
		logger.Warn("scan_orchestrator: schedule-job state: empty account_id, skipping")
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		logger.Error("scan_orchestrator: schedule-job state: db", "error", err)
		return
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		logger.Error("scan_orchestrator: schedule-job state: begin tx", "error", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Row-level lock on the SELECT means a parallel UpsertScheduleJobState
	// for the same account (same process or different replica) will wait
	// here until we COMMIT or ROLLBACK. Deterministic ORDER BY so we pick
	// the same row consistently when an account has multiple non-proxy
	// agent rows.
	var row struct {
		ID  string `db:"id"`
		Raw string `db:"raw"`
	}
	err = tx.Get(&row,
		`SELECT id::text AS id, COALESCE(connection_status::text, '{}') AS raw
		 FROM agent
		 WHERE cloud_account_id = $1 AND type != 'proxy'
		 ORDER BY created_at DESC, id
		 LIMIT 1
		 FOR UPDATE`,
		account.AccountID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// No agent row yet — collector hasn't received first heartbeat. Nothing
		// to write into. Schedule state is also "not yet recorded", so the next
		// heartbeat will re-evaluate as missed.
		return
	}
	if err != nil {
		logger.Error("scan_orchestrator: schedule-job state: read", "error", err)
		return
	}

	connStatus := map[string]any{}
	if row.Raw != "" {
		if err := json.Unmarshal([]byte(row.Raw), &connStatus); err != nil {
			logger.Error("scan_orchestrator: schedule-job state: unmarshal connection_status", "error", err)
			return
		}
	}

	existing, _ := connStatus["schedule_jobs"].([]any)
	updated, prevExec := upsertScheduleJobsArray(existing, scannerName, cron, time.Now())

	newJSON, err := json.Marshal(updated)
	if err != nil {
		logger.Error("scan_orchestrator: schedule-job state: marshal", "error", err)
		return
	}

	if _, err := tx.Exec(
		`UPDATE agent
		 SET connection_status = jsonb_set(
		         COALESCE(connection_status, '{}'::jsonb),
		         '{schedule_jobs}',
		         $1::jsonb,
		         true
		     ),
		     updated_at = NOW()
		 WHERE id = $2`,
		string(newJSON), row.ID,
	); err != nil {
		logger.Error("scan_orchestrator: schedule-job state: update", "error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		logger.Error("scan_orchestrator: schedule-job state: commit", "error", err)
		return
	}
	committed = true
	logger.Info("scan_orchestrator: schedule-job state updated", "exec_count", prevExec+1, "cron", cron)
}

// upsertScheduleJobsArray returns a new schedule_jobs slice with the entry for
// `scannerName` replaced (or inserted if absent), and the prior exec_count.
// The exec_count from the old entry is incremented; new entries start at 1.
func upsertScheduleJobsArray(existing []any, scannerName, cron string, now time.Time) (updated []any, prevExec int) {
	rec := scheduleJobRecord{
		JobID:            deterministicJobID(scannerName),
		RunnableName:     scheduleJobRunnableName,
		RunnableParams:   map[string]any{"action_func_name": scannerName},
		SchedulingParams: map[string]string{"cron_expression": cron},
		State: scheduleJobRecordState{
			ExecCount:       1,
			JobStatus:       scheduleJobStatusDone,
			LastExecTimeSec: now.Unix(),
		},
	}

	out := make([]any, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		entry, ok := e.(map[string]any)
		if !ok {
			out = append(out, e)
			continue
		}
		if scannerNameFromJobEntry(entry) == scannerName {
			if state, _ := entry["state"].(map[string]any); state != nil {
				switch v := state["exec_count"].(type) {
				case float64:
					prevExec = int(v)
				case int:
					prevExec = v
				}
			}
			rec.State.ExecCount = prevExec + 1
			out = append(out, recordToMap(rec))
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, recordToMap(rec))
	}
	return out, prevExec
}

// scannerNameFromJobEntry pulls the legacy action_func_name from an existing
// schedule_jobs entry. Returns "" if the entry is malformed.
func scannerNameFromJobEntry(entry map[string]any) string {
	params, _ := entry["runnable_params"].(map[string]any)
	if params == nil {
		return ""
	}
	name, _ := params["action_func_name"].(string)
	return name
}

// recordToMap converts a typed scheduleJobRecord to a map[string]any so it
// sits alongside the other untyped entries we read from existing data. Inner
// maps use map[string]any rather than the struct's narrower types so the
// shape matches what comes back from json.Unmarshal of persisted entries —
// readers (and tests) can type-assert uniformly.
func recordToMap(rec scheduleJobRecord) map[string]any {
	sched := make(map[string]any, len(rec.SchedulingParams))
	for k, v := range rec.SchedulingParams {
		sched[k] = v
	}
	return map[string]any{
		"job_id":            rec.JobID,
		"runnable_name":     rec.RunnableName,
		"runnable_params":   rec.RunnableParams,
		"scheduling_params": sched,
		"state": map[string]any{
			"exec_count":         rec.State.ExecCount,
			"job_status":         rec.State.JobStatus,
			"last_exec_time_sec": rec.State.LastExecTimeSec,
		},
	}
}

// lastExecForScanner extracts last_exec_time_sec for `scannerName` from the
// schedule_jobs array. Returns 0 if scanner not present or value missing.
// Exposed for EvaluateHeartbeat to consume the same wire shape we write.
func lastExecForScanner(existing []any, scannerName string) int64 {
	for _, e := range existing {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if scannerNameFromJobEntry(entry) != scannerName {
			continue
		}
		state, _ := entry["state"].(map[string]any)
		if state == nil {
			return 0
		}
		switch v := state["last_exec_time_sec"].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return 0
}

// fetchScheduleJobs reads the schedule_jobs array from agent.connection_status
// for the account. Empty slice when the agent row, column, or key is absent —
// "no agent row yet" is the first-ever-connect case, which the caller treats
// as "all scanners missed", same as an empty schedule_jobs array.
//
// ORDER BY mirrors UpsertScheduleJobState so the row we read for missed-scan
// detection is the same row a subsequent write would target.
func fetchScheduleJobs(account ScanAccount) ([]any, error) {
	if account.AccountID == "" {
		return nil, errors.New("schedule_jobs: empty account_id")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("schedule_jobs: db: %w", err)
	}
	var raw string
	err = dbms.Db.Get(&raw,
		`SELECT COALESCE(connection_status::text, '{}')
		 FROM agent
		 WHERE cloud_account_id = $1 AND type != 'proxy'
		 ORDER BY created_at DESC, id
		 LIMIT 1`,
		account.AccountID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	connStatus := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &connStatus); err != nil {
		return nil, fmt.Errorf("schedule_jobs: unmarshal connection_status: %w", err)
	}
	jobs, _ := connStatus["schedule_jobs"].([]any)
	return jobs, nil
}
