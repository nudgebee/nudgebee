package events

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"nudgebee/llm/security"
)

const AnalysisTypeRCAAttempt EventAnalysisType = "rca_attempt"
const RCAHistoryLimit = 5

// Attempts have their own session, so regenerating never deletes prior reasoning.
func RCAAttemptSessionID(id string) string { return SessionIdPrefixEventRCA + "version-" + id }

type RCAReportVersion struct {
	ID          string     `db:"id" json:"id"`
	Analysis    string     `db:"analysis" json:"analysis"`
	GeneratedAt *time.Time `db:"generated_at" json:"generated_at,omitempty"`
}

type RCAAttempt struct {
	ID           string    `db:"id"`
	Status       string    `db:"status"`
	StatusReason string    `db:"status_reason"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// All publishers/claimers take this lock, including the first claim with no mapping.
func lockRCAEvent(tx *sqlx.Tx, accountID, eventID string) error {
	_, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "rca:"+accountID+":"+eventID)
	return err
}

func (r *EventAnalysisRepository) ListRCAReports(ctx *security.RequestContext, eventID, fingerprint, accountID, aggKey string) ([]RCAReportVersion, error) {
	return listRCAReports(r.dbManager.Db, eventID, fingerprint, accountID, aggKey)
}

func listRCAReports(db sqlx.Queryer, eventID, fingerprint, accountID, aggKey string) ([]RCAReportVersion, error) {
	reports := []RCAReportVersion{}
	err := sqlx.Select(db, &reports, `SELECT a.id, a.analysis,
 CASE WHEN a.status='COMPLETED' THEN COALESCE(a.updated_at,a.recorded_at) END AS generated_at
 FROM event_log_analysis a
 WHERE a.cloud_account_id=$1 AND a.event_fingerprint=$2 AND a.event_aggregation_key=$3
 AND a.analysis_type='rca_analysis' AND trim(COALESCE(a.analysis,''))<>''
 AND (a.event_id=$4 OR EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.event_id=$4 AND m.analysis_type='rca_analysis' AND m.analysis_id=a.id))
 ORDER BY EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.event_id=$4 AND m.analysis_type='rca_analysis' AND m.analysis_id=a.id) DESC,COALESCE(a.updated_at,a.recorded_at) DESC,a.id DESC LIMIT $5`, accountID, fingerprint, aggKey, eventID, RCAHistoryLimit)
	return reports, err
}

func (r *EventAnalysisRepository) GetRCAAttempt(ctx *security.RequestContext, eventID, accountID string) (*RCAAttempt, error) {
	return getRCAAttempt(r.dbManager.Db, eventID, accountID)
}

func getRCAAttempt(db sqlx.Queryer, eventID, accountID string) (*RCAAttempt, error) {
	var attempt RCAAttempt
	err := sqlx.Get(db, &attempt, `SELECT id,status,COALESCE(status_reason,'') AS status_reason,COALESCE(updated_at,recorded_at) AS updated_at FROM event_log_analysis WHERE event_id=$1 AND cloud_account_id=$2 AND analysis_type IN ('rca_attempt','rca_analysis') AND status<>'COMPLETED' ORDER BY recorded_at DESC,id DESC LIMIT 1`, eventID, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &attempt, err
}

// expectedReportID makes a racing regenerate request a no-op if another attempt
// already completed since the caller read the live report.
func (r *EventAnalysisRepository) ClaimRCAAttempt(ctx *security.RequestContext, eventID, fingerprint, accountID, aggKey, expectedReportID string) (string, bool, error) {
	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockRCAEvent(tx, accountID, eventID); err != nil {
		return "", false, err
	}
	var current string
	err = tx.Get(&current, `SELECT a.id FROM event_log_analysis a WHERE a.cloud_account_id=$1 AND a.event_fingerprint=$2 AND a.event_aggregation_key=$3 AND a.analysis_type='rca_analysis' AND trim(COALESCE(a.analysis,''))<>'' AND (a.event_id=$4 OR EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.event_id=$4 AND m.analysis_type='rca_analysis' AND m.analysis_id=a.id)) ORDER BY EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.event_id=$4 AND m.analysis_type='rca_analysis' AND m.analysis_id=a.id) DESC,COALESCE(a.updated_at,a.recorded_at) DESC,a.id DESC LIMIT 1`, accountID, fingerprint, aggKey, eventID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if current != expectedReportID {
		return "", false, nil
	}
	var active string
	err = tx.Get(&active, `SELECT id FROM event_log_analysis WHERE event_id=$1 AND cloud_account_id=$2 AND analysis_type IN ('rca_attempt','rca_analysis') AND status='IN_PROGRESS' LIMIT 1`, eventID, accountID)
	if err == nil {
		return active, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	// Terminal attempts contain no successful report and are not mapped.
	_, err = tx.Exec(`DELETE FROM event_log_analysis a WHERE a.event_id=$1 AND a.cloud_account_id=$2 AND a.analysis_type='rca_attempt' AND a.status<>'IN_PROGRESS' AND NOT EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.analysis_id=a.id)`, eventID, accountID)
	if err != nil {
		return "", false, err
	}
	var id string
	err = tx.QueryRowx(`INSERT INTO event_log_analysis (event_id,event_fingerprint,cloud_account_id,event_aggregation_key,analysis_type,analysis,summary,status,updated_at) VALUES ($1,$2,$3,$4,'rca_attempt','','','IN_PROGRESS',NOW()) RETURNING id`, eventID, fingerprint, accountID, aggKey).Scan(&id)
	if err != nil {
		return "", false, err
	}
	return id, true, tx.Commit()
}

// FinishRCAAttempt is idempotent. A late failure cannot mutate a successful report,
// and an old completed retry cannot repoint the mapping away from a newer report.
func (r *EventAnalysisRepository) FinishRCAAttempt(ctx *security.RequestContext, id, eventID, accountID, report string) error {
	if strings.TrimSpace(report) == "" {
		return errors.New("empty RCA report")
	}
	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockRCAEvent(tx, accountID, eventID); err != nil {
		return err
	}
	var row struct {
		Type   EventAnalysisType `db:"analysis_type"`
		Status string            `db:"status"`
	}
	err = tx.Get(&row, `SELECT analysis_type,status FROM event_log_analysis WHERE id=$1 AND event_id=$2 AND cloud_account_id=$3 FOR UPDATE`, id, eventID, accountID)
	if err != nil {
		return err
	}
	if row.Type == AnalysisTypeRCA && row.Status == string(AnalysisStatusCompleted) {
		return tx.Commit()
	}
	if row.Type != AnalysisTypeRCAAttempt || row.Status != string(AnalysisStatusInProgress) {
		return fmt.Errorf("RCA attempt is no longer active")
	}
	if err = preserveSharedRCAReport(tx, eventID, accountID); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE event_log_analysis SET analysis=$2,analysis_type='rca_analysis',status='COMPLETED',status_reason=NULL,updated_at=NOW() WHERE id=$1`, id, report)
	if err != nil {
		return err
	}
	if _, err = upsertAnalysisMapping(tx, eventID, id, AnalysisTypeRCA, true); err != nil {
		return err
	}
	if err = pruneRCAReports(tx, eventID, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *EventAnalysisRepository) UpdateRCAAttemptStatus(ctx *security.RequestContext, id, eventID, accountID, status, reason string) error {
	_, err := r.dbManager.Db.Exec(`UPDATE event_log_analysis SET status=$4,status_reason=$5,updated_at=NOW() WHERE id=$1 AND event_id=$2 AND cloud_account_id=$3 AND analysis_type='rca_attempt' AND status='IN_PROGRESS'`, id, eventID, accountID, status, reason)
	return err
}

// Lock candidate report rows before the reference check. A concurrent mapping
// insert takes a FK key-share lock; the next statement sees it after the wait.
// A single DELETE/NOT EXISTS snapshot could otherwise cascade-delete that mapping.
func pruneRCAReports(tx *sqlx.Tx, eventID, accountID string) error {
	var ids []string
	if err := tx.Select(&ids, `SELECT id FROM event_log_analysis WHERE event_id=$1 AND cloud_account_id=$2 AND analysis_type='rca_analysis' AND status='COMPLETED' ORDER BY COALESCE(updated_at,recorded_at) DESC,id DESC OFFSET $3 FOR UPDATE`, eventID, accountID, RCAHistoryLimit); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	query, args, err := sqlx.In(`DELETE FROM event_log_analysis a WHERE a.id IN (?) AND NOT EXISTS (SELECT 1 FROM event_analysis_mapping m WHERE m.analysis_id=a.id)`, ids)
	if err != nil {
		return err
	}
	_, err = tx.Exec(tx.Rebind(query), args...)
	return err
}

// An event's old shared report needs durable history membership after its live
// mapping moves. Copy only the report and its original timestamps; reasoning
// remains owned by the original conversation and historical UI is read-only.
func preserveSharedRCAReport(tx *sqlx.Tx, eventID, accountID string) error {
	_, err := tx.Exec(`INSERT INTO event_log_analysis (event_id,event_fingerprint,cloud_account_id,event_aggregation_key,analysis_type,analysis,summary,status,recorded_at,updated_at)
 SELECT $1,a.event_fingerprint,a.cloud_account_id,a.event_aggregation_key,a.analysis_type,a.analysis,a.summary,a.status,a.recorded_at,a.updated_at
 FROM event_log_analysis a JOIN event_analysis_mapping m ON m.analysis_id=a.id
 WHERE m.event_id=$1 AND m.analysis_type='rca_analysis' AND a.analysis_type='rca_analysis' AND a.cloud_account_id=$2 AND a.event_id IS DISTINCT FROM $1 AND trim(COALESCE(a.analysis,''))<>''`, eventID, accountID)
	return err
}

func (r *EventAnalysisRepository) UpdateLegacyRCAStatus(ctx *security.RequestContext, id, eventID, accountID, status, reason string) error {
	_, err := r.dbManager.Db.Exec(`UPDATE event_log_analysis SET status=$4,status_reason=$5,updated_at=NOW() WHERE id=$1 AND event_id=$2 AND cloud_account_id=$3 AND analysis_type='rca_analysis' AND status='IN_PROGRESS'`, id, eventID, accountID, status, reason)
	return err
}

// A consistent read prevents publication between the report and attempt reads
// from returning COMPLETED with the old report and stopping client polling.
func (r *EventAnalysisRepository) GetRCAHistory(ctx *security.RequestContext, eventID, fingerprint, accountID, aggKey string) ([]RCAReportVersion, *RCAAttempt, error) {
	tx, err := r.dbManager.Db.BeginTxx(ctx.GetContext(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	reports, err := listRCAReports(tx, eventID, fingerprint, accountID, aggKey)
	if err != nil {
		return nil, nil, err
	}
	attempt, err := getRCAAttempt(tx, eventID, accountID)
	if err != nil {
		return nil, nil, err
	}
	return reports, attempt, tx.Commit()
}
