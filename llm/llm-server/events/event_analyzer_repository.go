package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/jmoiron/sqlx"
)

// findCurrentAnalysisID resolves, and locks, the event_log_analysis row a write
// should land on, returning "" when there is none yet.
//
// An event owns a specific analysis run via event_analysis_mapping, so that is
// the authoritative lookup. Without an eventId there is nothing to look up and
// we fall back to the newest row for the fingerprint identity.
//
// Both lookups take FOR UPDATE: the row is about to be written, and the lock is
// what serialises concurrent writers for the same event.
//
// The fallback orders on COALESCE(updated_at, recorded_at). updated_at is
// nullable with no default (V443) and NULLs sort first on a plain DESC, so
// ordering on the bare column ranks legacy rows as the newest. Every caller
// needs that ordering, which is exactly why it lives here and not at each site.
func findCurrentAnalysisID(tx *sqlx.Tx, eventId, fingerprint, accountId, aggKey string, analysisType EventAnalysisType) (string, error) {
	var (
		analysisID string
		err        error
	)
	if eventId != "" {
		err = tx.QueryRowx(
			`SELECT analysis_id FROM event_analysis_mapping WHERE event_id=$1 AND analysis_type=$2 FOR UPDATE`,
			eventId, analysisType,
		).Scan(&analysisID)
	} else {
		err = tx.QueryRowx(
			`SELECT id FROM event_log_analysis WHERE event_fingerprint = $1 AND cloud_account_id = $2 AND event_aggregation_key = $3 AND analysis_type = $4 ORDER BY COALESCE(updated_at, recorded_at) DESC LIMIT 1 FOR UPDATE`,
			fingerprint, accountId, aggKey, analysisType,
		).Scan(&analysisID)
	}
	if err != nil {
		// No row yet is the ordinary first-write case, not a failure. Return the
		// empty id explicitly rather than falling through on the assumption that
		// Scan left the target untouched.
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return analysisID, nil
}

// upsertAnalysisMapping points (eventId, analysisType) at analysisID and reports
// how many rows that affected.
//
// repoint decides how a pre-existing mapping row is treated, and the two modes
// are not interchangeable:
//
//   - repoint == true: the caller already holds this event's mapping row and is
//     moving it to a newer analysis run (a forced regenerate). DO NOTHING here
//     would affect zero rows and, for callers that gate on the count, silently
//     abandon the write.
//   - repoint == false: a fresh claim, where the mapping primary key is the
//     concurrency gate. A conflict means a racing dispatcher registered the
//     event first, so zero rows affected is the signal to back out.
func upsertAnalysisMapping(tx *sqlx.Tx, eventId, analysisID string, analysisType EventAnalysisType, repoint bool) (int64, error) {
	query := `INSERT INTO event_analysis_mapping (event_id, analysis_id, analysis_type) VALUES ($1, $2, $3) ON CONFLICT (event_id, analysis_type) DO NOTHING`
	if repoint {
		query = `INSERT INTO event_analysis_mapping (event_id, analysis_id, analysis_type) VALUES ($1, $2, $3) ON CONFLICT (event_id, analysis_type) DO UPDATE SET analysis_id = EXCLUDED.analysis_id`
	}
	res, err := tx.Exec(query, eventId, analysisID, analysisType)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EventAnalysisRepository handles database operations for event analysis.
type EventAnalysisRepository struct {
	dbManager *common.DatabaseManager
	// analysisFreshness bounds how long a COMPLETED analysis may be reused for a
	// different event sharing its fingerprint. Zero disables the bound, which is
	// the previous behaviour. See config.EventAnalysisFreshnessHours.
	analysisFreshness time.Duration
}

// NewEventAnalysisRepository creates a new repository for event analysis.
func NewEventAnalysisRepository(dbManager *common.DatabaseManager) *EventAnalysisRepository {
	return &EventAnalysisRepository{
		dbManager:         dbManager,
		analysisFreshness: time.Duration(config.Config.EventAnalysisFreshnessHours) * time.Hour,
	}
}

// EventAnalysisType defines the type of analysis being performed.
type EventAnalysisType string

const (
	AnalysisTypeSummary          EventAnalysisType = "summary"
	AnalysisTypeInvestigation    EventAnalysisType = "investigation"
	AnalysisTypeLog              EventAnalysisType = "log_analysis"
	AnalysisTypeRCA              EventAnalysisType = "rca_analysis"
	AnalysisTypeDetailedResponse EventAnalysisType = "detailed_response"
	AnalysisTypeRemediation      EventAnalysisType = "remediation"
)

const (
	SessionIdPrefixEvent    = "event-"
	SessionIdPrefixEventRCA = "event-rca-"
)

// AnalysisStatus defines the status of an analysis.
type AnalysisStatus string

const (
	AnalysisStatusInProgress AnalysisStatus = "IN_PROGRESS"
	AnalysisStatusCompleted  AnalysisStatus = "COMPLETED"
	AnalysisStatusFailed     AnalysisStatus = "FAILED"
	AnalysisStatusCreated    AnalysisStatus = "CREATED"
)

// EventInfo holds basic information about an event fetched from the database.
type EventInfo struct {
	ID             string
	Fingerprint    string
	AggregationKey string
}

// EventAnalysis represents an event analysis record from the database.
type EventAnalysis struct {
	ID             string
	Analysis       string
	Status         string
	Summary        string
	RelatedEventId string
	// StatusReason carries the failure detail emitted by the agent / pipeline
	// when ``Status == FAILED``. Populated by ``UpdateEventAnalysisStatus`` and
	// surfaced to the UI so users see *why* a run failed instead of an opaque
	// "Failed" badge.
	StatusReason string
	// UpdatedAt is the row's last-touch timestamp. Used to detect a stale RCA
	// report: the RCA is derived from the summary/investigation/log rows, so an
	// input row newer than the RCA row means the report no longer reflects the
	// latest findings.
	UpdatedAt time.Time
}

// GetEventInfo fetches basic event details (ID, fingerprint, aggregation key) from the database.
func (r *EventAnalysisRepository) GetEventInfo(ctx *security.RequestContext, eventId string, accountId string) (*EventInfo, error) {
	eventSqlQuery := `SELECT id, fingerprint, aggregation_key FROM events WHERE id = $1 and cloud_account_id = $2;`
	rows, err := r.dbManager.Db.Queryx(eventSqlQuery, eventId, accountId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to get event from database", "error", err, "event_id", eventId)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getEventInfo", "error", err, "event_id", eventId)
		}
	}()

	var dbEventId, dbEventFingerprint, dbEventAggregationKey sql.NullString
	if rows.Next() {
		if err := rows.Scan(&dbEventId, &dbEventFingerprint, &dbEventAggregationKey); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan event from database", "error", err, "event_id", eventId)
			return nil, err
		}
		if dbEventId.String == "" {
			return nil, common.Error{Message: "analyzer: event not found - " + eventId}
		}
		return &EventInfo{
			ID:             dbEventId.String,
			Fingerprint:    dbEventFingerprint.String,
			AggregationKey: dbEventAggregationKey.String,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return nil, common.Error{Message: "analyzer: event not found - " + eventId}
}

// GetAnalysisIdFromMapping returns the analysis_id for a given (event_id, analysis_type) pair.
// Returns empty string and nil error if no mapping exists.
func (r *EventAnalysisRepository) GetAnalysisIdFromMapping(ctx *security.RequestContext, eventId string, analysisType EventAnalysisType) (string, error) {
	if eventId == "" {
		return "", nil
	}
	var analysisId string
	err := r.dbManager.Db.QueryRowx(
		`SELECT analysis_id FROM event_analysis_mapping WHERE event_id=$1 AND analysis_type=$2`,
		eventId, analysisType,
	).Scan(&analysisId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		ctx.GetLogger().Warn("analyzer: error querying event_analysis_mapping", "error", err, "event_id", eventId, "analysis_type", analysisType)
		return "", err
	}
	return analysisId, nil
}

// IsAnalysisStale reports whether a COMPLETED analysis is too old to be reused
// for a newly arriving event. Zero freshness disables the bound, which is the
// previous behaviour; an unknown timestamp is never treated as stale.
//
// This is the reuse bound, and it has to be applied at EVERY gate that decides
// to reuse a stage instead of regenerating it -- the outer gates in
// getOrCreateEventAnalysisStatus and the per-step caches inside
// analyzeEventUsingAgentsAndUpdateDb alike. Applying it to only some of them is
// worse than not applying it at all: an outer gate that rejects a stale analysis
// dispatches the pipeline, and per-step caches that still consider that stage
// fresh reuse it, write nothing, and leave updated_at where it was -- so the next
// event for the fingerprint takes the same path, and every event after it, with
// no run ever advancing the timestamp that would stop the cycle.
//
// The bound deliberately does not live in ClaimEventAnalysis alone. That is a
// concurrency gate reached only when log_analysis is not already COMPLETED, so a
// bound placed there never sees the case it was written for: a new event whose
// fingerprint is fully analysed and days old.
func (r *EventAnalysisRepository) IsAnalysisStale(writtenAt time.Time) bool {
	return r.analysisFreshness > 0 && !writtenAt.IsZero() && time.Since(writtenAt) > r.analysisFreshness
}

// GetEventAnalysis fetches an existing analysis from the database.
// If eventId is provided, it first checks event_analysis_mapping for a direct match.
func (r *EventAnalysisRepository) GetEventAnalysis(ctx *security.RequestContext, eventId, fingerprint, aggKey, accountId string, analysisType EventAnalysisType) (*EventAnalysis, error) {
	if eventId != "" {
		analysisId, err := r.GetAnalysisIdFromMapping(ctx, eventId, analysisType)
		if err != nil {
			return nil, err
		}
		if analysisId != "" {
			// COALESCE, not bare updated_at: the column is nullable with no default
			// (V443), and callers use this timestamp to decide whether the analysis is
			// still fresh enough to reuse. A legacy NULL would scan as the zero time
			// and read as infinitely stale, forcing a regenerate on every event.
			sqlQuery := `SELECT id, analysis, status, event_id, summary, status_reason, COALESCE(updated_at, recorded_at) AS updated_at FROM event_log_analysis WHERE id = $1 AND cloud_account_id = $2;`
			var id, analysis, status, relatedEventId, summary, statusReason sql.NullString
			var updatedAt sql.NullTime
			err := r.dbManager.Db.QueryRowx(sqlQuery, analysisId, accountId).Scan(&id, &analysis, &status, &relatedEventId, &summary, &statusReason, &updatedAt)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, nil
				}
				ctx.GetLogger().Warn("analyzer: failed to query mapped event_log_analysis by id", "error", err, "analysis_id", analysisId)
				return nil, err
			}
			return &EventAnalysis{
				ID:             id.String,
				Analysis:       analysis.String,
				Status:         status.String,
				Summary:        summary.String,
				RelatedEventId: relatedEventId.String,
				StatusReason:   statusReason.String,
				UpdatedAt:      updatedAt.Time,
			}, nil
		}
	}

	// COALESCE for the same reason as the mapped branch above; the ORDER BY
	// already uses it, so the selected value now matches the sort key.
	sqlQuery := `SELECT id, analysis, status, event_id, summary, status_reason, COALESCE(updated_at, recorded_at) AS updated_at FROM event_log_analysis WHERE event_fingerprint = $1 and cloud_account_id = $2 and event_aggregation_key = $3 and analysis_type = $4 ORDER BY COALESCE(updated_at, recorded_at) DESC LIMIT 1;`
	rows, err := r.dbManager.Db.Queryx(sqlQuery, fingerprint, accountId, aggKey, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to get log_analysis from database", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getAnalysis", "error", err, "event_fingerprint", fingerprint, "event_aggregation_key", aggKey)
		}
	}()

	var id, analysis, status, relatedEventId, summary, statusReason sql.NullString
	var updatedAt sql.NullTime
	if rows.Next() {
		if err := rows.Scan(&id, &analysis, &status, &relatedEventId, &summary, &statusReason, &updatedAt); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan analysis from database", "error", err)
			return nil, err
		}
		return &EventAnalysis{
			ID:             id.String,
			Analysis:       analysis.String,
			Status:         status.String,
			Summary:        summary.String,
			RelatedEventId: relatedEventId.String,
			StatusReason:   statusReason.String,
			UpdatedAt:      updatedAt.Time,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return nil, nil if no analysis is found
	return nil, nil
}

// UpsertEventAnalysisInProgress inserts or updates an analysis entry to 'IN_PROGRESS'.
func (r *EventAnalysisRepository) UpsertEventAnalysisInProgress(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey string, analysisType EventAnalysisType) error {
	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existingId, err := findCurrentAnalysisID(tx, eventId, fingerprint, accountId, aggKey, analysisType)
	if err != nil {
		return err
	}

	if existingId != "" {
		// Stamp event_id only when we got here through the mapping; the
		// fingerprint fallback has no event to attribute the row to.
		if eventId != "" {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET status=$2, status_reason=NULL, event_id=$3, updated_at=NOW() WHERE id=$1`,
				existingId, AnalysisStatusInProgress, eventId,
			)
		} else {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET status=$2, status_reason=NULL, updated_at=NOW() WHERE id=$1`,
				existingId, AnalysisStatusInProgress,
			)
		}
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to update analysis in progress", "error", err, "analysis_id", existingId)
			return err
		}
		return tx.Commit()
	}

	var dbEventId any = eventId
	if eventId == "" {
		dbEventId = nil
	}

	insertQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	var analysisId string
	err = tx.QueryRowx(insertQuery, dbEventId, fingerprint, "", "", AnalysisStatusInProgress, accountId, aggKey, analysisType).Scan(&analysisId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to insert analysis in progress", "error", err, "event_id", eventId)
		return err
	}

	if eventId != "" {
		if _, err = upsertAnalysisMapping(tx, eventId, analysisId, analysisType, true); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ClaimEventAnalysis atomically claims an analysis row for processing.
//
// CONCURRENCY & RECOVERY DESIGN:
// Multiple entry points (MQ consumers, HTTP endpoints, retry loops) fire
// concurrently for the same event fingerprint when an alert bursts.
//
//  1. Multi-replica leader serialization: When eventId is provided, claims are
//     serialized per (event_id, analysis_type) in event_analysis_mapping.
//  2. Atomic Winner Selection: Uses ON CONFLICT DO NOTHING with RowsAffected()
//     check so exactly one concurrent caller wins the claim.
//  3. Duplicate-dispatch prevention (#29472):
//     - IN_PROGRESS rows are never stolen by another worker.
//     - The force flag controls COMPLETED handling:
//     * force == false (auto dispatch): a COMPLETED fingerprint row returns
//     claimed = false and binds eventId to the existing analysis.
//     * force == true (regenerate): a COMPLETED row IS re-claimed so explicit
//     regeneration re-runs the pipeline.
func (r *EventAnalysisRepository) ClaimEventAnalysis(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey string, analysisType EventAnalysisType, force bool) (bool, error) {
	if accountId == "" {
		return false, errors.New("accountId cannot be empty")
	}

	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// hadMapping records that this (eventId, analysisType) already owns a mapping
	// row, which we hold locked for the rest of the transaction. It decides how the
	// mapping insert below resolves a conflict: a forced regenerate must repoint the
	// mapping at the new historical row, whereas a fresh claim must treat a conflict
	// as "another dispatcher won" and back out.
	hadMapping := false
	if eventId != "" {
		var existingId string
		err = tx.QueryRowx(
			`SELECT analysis_id FROM event_analysis_mapping WHERE event_id=$1 AND analysis_type=$2 FOR UPDATE`,
			eventId, analysisType,
		).Scan(&existingId)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}

		if existingId != "" {
			hadMapping = true
			var currentStatus string
			err := tx.QueryRowx(`SELECT status FROM event_log_analysis WHERE id = $1`, existingId).Scan(&currentStatus)
			if err != nil {
				return false, err
			}
			if currentStatus == string(AnalysisStatusInProgress) {
				return false, nil
			}
			if currentStatus == string(AnalysisStatusCompleted) && !force {
				return false, nil
			}
			if !force {
				res, err := tx.Exec(`UPDATE event_log_analysis SET status = $2, status_reason = NULL, updated_at = NOW() WHERE id = $1 AND status <> $3`, existingId, AnalysisStatusInProgress, AnalysisStatusInProgress)
				if err != nil {
					return false, err
				}
				affected, _ := res.RowsAffected()
				if err := tx.Commit(); err != nil {
					return false, err
				}
				return affected > 0, nil
			}
		}
	}

	var existingStatus string
	var existingAnalysisId string
	var existingWrittenAt sql.NullTime
	err = tx.QueryRowx(
		`SELECT id, status, COALESCE(updated_at, recorded_at) FROM event_log_analysis WHERE id = (SELECT id FROM event_log_analysis WHERE event_fingerprint = $1 AND cloud_account_id = $2 AND event_aggregation_key = $3 AND analysis_type = $4 ORDER BY COALESCE(updated_at, recorded_at) DESC LIMIT 1) FOR UPDATE`,
		fingerprint, accountId, aggKey, analysisType,
	).Scan(&existingAnalysisId, &existingStatus, &existingWrittenAt)

	// A completed analysis for this fingerprint is only reusable while it is
	// recent enough to still describe the system. Past that, a newly arriving
	// event would otherwise be bound to findings produced against telemetry from
	// days ago — and, since V850 gives it a mapping row, bound permanently: it
	// would never pick up a later run for the same fingerprint. Falling through
	// here lets the new event claim and generate against current data.
	//
	// IN_PROGRESS is deliberately not aged out. An active run is never stolen
	// regardless of how long it has been going -- doing so would reintroduce the
	// duplicate-dispatch bug (#29472) this gate exists to prevent, since a slow
	// run is indistinguishable here from a dead one. Dead runs are handled
	// elsewhere: the sync tick in api/conversation_sync.go marks an IN_PROGRESS
	// analysis FAILED once it exceeds its own maxRecoveryAge, and FAILED falls
	// through this condition and is claimable. That constant is separate from
	// this window and is NOT affected by the config knob below, despite both
	// being natural to set to 24h.
	//
	// FAILED and CREATED are likewise unaffected by the bound: neither matches
	// this condition, so both already fall through to a fresh claim.
	if err == nil {
		staleForReuse := r.analysisFreshness > 0 && existingWrittenAt.Valid &&
			time.Since(existingWrittenAt.Time) > r.analysisFreshness

		if existingStatus == string(AnalysisStatusInProgress) || (existingStatus == string(AnalysisStatusCompleted) && !force && !staleForReuse) {
			if eventId != "" {
				// hadMapping here means a forced regenerate declined to steal the
				// active run. The mapping still points at this event's previous,
				// now-superseded analysis, so repoint it at the run that is actually
				// live — DO NOTHING would leave the regenerate showing stale output.
				if _, err = upsertAnalysisMapping(tx, eventId, existingAnalysisId, analysisType, hadMapping); err != nil {
					return false, err
				}
			}
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return false, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	var dbEventId any = eventId
	if eventId == "" {
		dbEventId = nil
	}

	insertQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, '', '', $3, $4, $5, $6) RETURNING id`
	var analysisId string
	err = tx.QueryRowx(insertQuery, dbEventId, fingerprint, AnalysisStatusInProgress, accountId, aggKey, analysisType).Scan(&analysisId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to claim analysis in database", "error", err, "event_id", eventId)
		return false, err
	}

	if eventId != "" {
		// Forced regenerate on an event that already had a mapping repoints it at
		// the new historical row; DO NOTHING there would affect zero rows and back
		// the whole claim out, silently turning every regenerate into a no-op.
		//
		// A fresh claim instead uses the mapping primary key as the concurrency
		// gate: zero rows affected means a racing dispatcher inserted it first, so
		// back out and let that one run.
		affected, err := upsertAnalysisMapping(tx, eventId, analysisId, analysisType, hadMapping)
		if err != nil {
			return false, err
		}
		if !hadMapping && affected == 0 {
			_ = tx.Rollback()
			return false, nil
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// SaveEventRCAAnalysis saves the final RCA analysis result.
func (r *EventAnalysisRepository) SaveEventRCAAnalysis(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey, analysisResult string) error {
	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existingId, err := findCurrentAnalysisID(tx, eventId, fingerprint, accountId, aggKey, AnalysisTypeRCA)
	if err != nil {
		return err
	}

	if existingId != "" {
		if eventId != "" {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET analysis=$2, status=$3, status_reason=NULL, event_id=$4, updated_at=NOW() WHERE id=$1`,
				existingId, analysisResult, AnalysisStatusCompleted, eventId,
			)
		} else {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET analysis=$2, status=$3, status_reason=NULL, updated_at=NOW() WHERE id=$1`,
				existingId, analysisResult, AnalysisStatusCompleted,
			)
		}
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to update rca analysis row", "error", err, "analysis_id", existingId)
			return err
		}
		return tx.Commit()
	}

	var dbEventId any = eventId
	if eventId == "" {
		dbEventId = nil
	}

	insertQuery := `INSERT INTO event_log_analysis (event_id, analysis, status, event_fingerprint, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`
	var analysisId string
	err = tx.QueryRowx(insertQuery, dbEventId, analysisResult, AnalysisStatusCompleted, fingerprint, accountId, aggKey, AnalysisTypeRCA).Scan(&analysisId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to insert rca analysis into database", "error", err, "event_id", eventId)
		return err
	}

	if eventId != "" {
		if _, err = upsertAnalysisMapping(tx, eventId, analysisId, AnalysisTypeRCA, true); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetLatestAnalysisUpdatedAt returns the newest updated_at across the given
// analysis types for one event identity, in a single query. Returns the zero
// time when no matching rows exist.
func (r *EventAnalysisRepository) GetLatestAnalysisUpdatedAt(ctx *security.RequestContext, fingerprint, aggKey, accountId string, analysisTypes []EventAnalysisType) (time.Time, error) {
	if len(analysisTypes) == 0 {
		return time.Time{}, nil
	}
	placeholders := make([]string, len(analysisTypes))
	args := []any{fingerprint, accountId, aggKey}
	for i, aType := range analysisTypes {
		placeholders[i] = fmt.Sprintf("$%d", i+4)
		args = append(args, aType)
	}
	query := fmt.Sprintf(`SELECT MAX(updated_at) FROM event_log_analysis WHERE event_fingerprint = $1 AND cloud_account_id = $2 AND event_aggregation_key = $3 AND analysis_type IN (%s);`, strings.Join(placeholders, ", "))
	var latest sql.NullTime
	if err := r.dbManager.Db.Get(&latest, query, args...); err != nil {
		return time.Time{}, fmt.Errorf("GetLatestAnalysisUpdatedAt: failed to query max updated_at: %w", err)
	}
	return latest.Time, nil
}

// GetEventRuleDefinition fetches the rule definition and annotations for a given aggregation key.
func (r *EventAnalysisRepository) GetEventRuleDefinition(ctx *security.RequestContext, accountId string, aggregationKey string) (string, map[string]any, error) {
	rows, err := r.dbManager.Db.Query("select expr, annotations::jsonb from event_rules er where er.account_id = $1 and er.alert  = $2", accountId, aggregationKey)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getEventRuleDefinition", "error", err, "rule", aggregationKey)
		}
	}()

	var expr string
	var annotations []byte
	if rows.Next() {
		if err := rows.Scan(&expr, &annotations); err != nil {
			return "", nil, err
		}
		annotationMap := map[string]any{}
		if err := json.Unmarshal(annotations, &annotationMap); err != nil {
			return expr, nil, err
		}
		return expr, annotationMap, nil
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

// GetEventRuleActionDefinitions fetches action definitions for a given alert rule.
func (r *EventAnalysisRepository) GetEventRuleActionDefinitions(ctx *security.RequestContext, accountId string, aggregationKey string) ([]map[string]any, error) {
	rows, err := r.dbManager.Db.Query(`select action_params
		from agent_playbook
		where alert_name = $2 and cloud_account_id = $1`, accountId, aggregationKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getAlertRuleActionDefinitions", "error", err)
		}
	}()

	alertRuleActionDefinitions := []map[string]any{}
	for rows.Next() {
		var actionParams []byte
		err = rows.Scan(&actionParams)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to scan rows in getAlertRuleActionDefinitions", "error", err)
			continue
		}
		err := common.UnmarshalJson(actionParams, &alertRuleActionDefinitions)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to unmarshal rows in getAlertRuleActionDefinitions", "error", err)
			continue
		}
		break
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return alertRuleActionDefinitions, nil
}

// UpdateEventAnalysisStatusById updates status on a specific analysis row by its UUID.
func (r *EventAnalysisRepository) UpdateEventAnalysisStatusById(ctx *security.RequestContext, analysisId, status, statusReason string) error {
	_, err := r.dbManager.Db.Exec(
		`UPDATE event_log_analysis SET status=$2, status_reason=$3, updated_at=NOW() WHERE id=$1`,
		analysisId, status, statusReason,
	)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis status by id", "error", err, "analysis_id", analysisId)
		return err
	}
	return nil
}

// UpdateEventAnalysisStatus updates the status and status reason for the latest log analysis entry.
func (r *EventAnalysisRepository) UpdateEventAnalysisStatus(ctx *security.RequestContext, eventFingerprint string, cloudAccountId string, aggregationKey string, status string, statusReason string, analysisType EventAnalysisType) error {
	ctx.GetLogger().Info("analyzer: updating event analysis entry", "event_fingerprint", eventFingerprint, "account_id", cloudAccountId, "event_aggregation_key", aggregationKey, "analysis_type", analysisType, "status", status)
	updateQuery := `UPDATE event_log_analysis SET status=$2, status_reason=$3, updated_at=NOW() WHERE id IN (SELECT id FROM event_log_analysis WHERE event_fingerprint = $1 AND event_aggregation_key = $4 AND cloud_account_id = $5 AND analysis_type = $6 ORDER BY COALESCE(updated_at, recorded_at) DESC LIMIT 1);`
	_, err := r.dbManager.Db.Exec(updateQuery, eventFingerprint, status, statusReason, aggregationKey, cloudAccountId, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis status in database", "error", err, "event_id", eventFingerprint)
		return err
	}
	return nil
}

// UpsertEventAnalysisStatus records a terminal status for an analysis stage,
// creating the row when the stage never produced one.
//
// UpdateEventAnalysisStatus cannot do this. It is a bare UPDATE, so when a stage
// is skipped before it ever inserted a row the statement matches nothing, changes
// nothing, and still returns nil. The caller believes it marked the stage
// COMPLETED; the database has no such row. allEventAnalysisTypesCompleted then
// never sees that stage as complete, the pipeline can never report itself
// finished, and every later event sharing the fingerprint re-enters it -- writing
// another detailed_response row each time, now that V850 permits more than one
// row per identity.
//
// Scope is the fingerprint identity, matching UpdateEventAnalysisStatus rather
// than the per-event mapping. A skip decision is a property of the event's shape
// (a missing label, no logs), so it holds for every event sharing the
// fingerprint; resolving through the mapping instead would insert a fresh row for
// each unmapped event, which is the duplication this exists to stop.
func (r *EventAnalysisRepository) UpsertEventAnalysisStatus(ctx *security.RequestContext, eventId, eventFingerprint, cloudAccountId, aggregationKey, status, statusReason string, analysisType EventAnalysisType) error {
	ctx.GetLogger().Info("analyzer: recording event analysis stage status", "event_fingerprint", eventFingerprint, "account_id", cloudAccountId, "event_aggregation_key", aggregationKey, "analysis_type", analysisType, "status", status)

	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Empty eventId forces the fingerprint branch of the lookup -- see the scope
	// note above.
	existingId, err := findCurrentAnalysisID(tx, "", eventFingerprint, cloudAccountId, aggregationKey, analysisType)
	if err != nil {
		return err
	}

	if existingId != "" {
		if _, err = tx.Exec(
			`UPDATE event_log_analysis SET status=$2, status_reason=$3, updated_at=NOW() WHERE id=$1`,
			existingId, status, statusReason,
		); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to update analysis status in database", "error", err, "analysis_id", existingId)
			return err
		}
		return tx.Commit()
	}

	var dbEventId any = eventId
	if eventId == "" {
		dbEventId = nil
	}

	var analysisId string
	if err = tx.QueryRowx(
		`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, status_reason, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, '', '', $3, $4, $5, $6, $7) RETURNING id`,
		dbEventId, eventFingerprint, status, statusReason, cloudAccountId, aggregationKey, analysisType,
	).Scan(&analysisId); err != nil {
		ctx.GetLogger().Warn("analyzer: failed to insert skipped analysis stage", "error", err, "event_fingerprint", eventFingerprint, "analysis_type", analysisType)
		return err
	}

	if eventId != "" {
		if _, err = upsertAnalysisMapping(tx, eventId, analysisId, analysisType, true); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UpsertEventAnalysis inserts or updates an analysis entry.
func (r *EventAnalysisRepository) UpsertEventAnalysis(ctx *security.RequestContext, eventId, analysis, summary, status, fingerprint, accountId, aggKey string, analysisType EventAnalysisType) error {
	tx, err := r.dbManager.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existingId, err := findCurrentAnalysisID(tx, eventId, fingerprint, accountId, aggKey, analysisType)
	if err != nil {
		return err
	}

	if existingId != "" {
		if eventId != "" {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET analysis=$2, summary=$3, status=$4, status_reason=NULL, event_id=$5, updated_at=NOW() WHERE id=$1`,
				existingId, analysis, summary, status, eventId,
			)
		} else {
			_, err = tx.Exec(
				`UPDATE event_log_analysis SET analysis=$2, summary=$3, status=$4, status_reason=NULL, updated_at=NOW() WHERE id=$1`,
				existingId, analysis, summary, status,
			)
		}
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to update analysis row in database", "error", err, "analysis_id", existingId)
			return err
		}
		return tx.Commit()
	}

	var dbEventId any = eventId
	if eventId == "" {
		dbEventId = nil
	}

	insertQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	var analysisId string
	err = tx.QueryRowx(insertQuery, dbEventId, fingerprint, analysis, summary, status, accountId, aggKey, analysisType).Scan(&analysisId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis in database", "error", err, "event_id", eventId)
		return err
	}

	if eventId != "" {
		if _, err = upsertAnalysisMapping(tx, eventId, analysisId, analysisType, true); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// InProgressAnalysis holds minimal data to identify and restart a stuck analysis.
type InProgressAnalysis struct {
	ID                  string
	EventId             string
	AccountId           string
	EventFingerprint    string
	EventAggregationKey string
	AnalysisType        EventAnalysisType
	UpdatedAt           time.Time
}

// ListInProgressAnalysis returns all analysis entries that are currently 'IN_PROGRESS'.
func (r *EventAnalysisRepository) ListInProgressAnalysis(ctx *security.RequestContext) ([]InProgressAnalysis, error) {
	sqlQuery := `SELECT id, event_id, cloud_account_id, event_fingerprint, event_aggregation_key, analysis_type, COALESCE(updated_at, recorded_at) as updated_at FROM event_log_analysis WHERE status = 'IN_PROGRESS';`
	rows, err := r.dbManager.Db.Queryx(sqlQuery)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to list in-progress analyses", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in ListInProgressAnalysis", "error", err)
		}
	}()

	var results []InProgressAnalysis
	for rows.Next() {
		var a InProgressAnalysis
		if err := rows.Scan(&a.ID, &a.EventId, &a.AccountId, &a.EventFingerprint, &a.EventAggregationKey, &a.AnalysisType, &a.UpdatedAt); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan in-progress analysis", "error", err)
			continue
		}
		results = append(results, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

type EventKnowledgebase struct {
	Description string `json:"description"`
	Impact      string `json:"impact"`
	Diagnosis   string `json:"diagnosis"`
	Mitigation  string `json:"mitigation"`
}

// GetKnowledgebase inserts or updates an analysis entry.
func (r *EventAnalysisRepository) GetKnowledgebase(ctx *security.RequestContext, rulename string) (EventKnowledgebase, bool) {
	knowledgeQuery := `select description, impact, diagnosis, mitigation from knowledge_base where lower(rule_name) = lower($1)`
	rows, err := r.dbManager.Db.Queryx(knowledgeQuery, rulename)
	if err != nil {
		ctx.GetLogger().Debug("analyzer: failed to get knowledge_base from database", "error", err, "rule_name", rulename)
		return EventKnowledgebase{}, false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getKnowledgebase", "error", err, "rule_name", rulename)
		}
	}()

	var description, impact, diagnosis, mitigation sql.NullString
	if rows.Next() {
		if err := rows.Scan(&description, &impact, &diagnosis, &mitigation); err != nil {
			ctx.GetLogger().Debug("analyzer: failed to scan knowledgebase from database", "error", err, "rule_name", rulename)
			return EventKnowledgebase{}, false
		}
		kb := EventKnowledgebase{
			Description: description.String,
			Impact:      impact.String,
			Diagnosis:   diagnosis.String,
			Mitigation:  mitigation.String,
		}
		return kb, true
	}
	if err := rows.Err(); err != nil {
		ctx.GetLogger().Debug("analyzer: rows error in getKnowledgebase", "error", err, "rule_name", rulename)
		return EventKnowledgebase{}, false
	}
	return EventKnowledgebase{}, false
}

// InsertRemediationExecution records that a remediation command was run against the event's cluster,
// so the panel can show "already applied" on reload. type=CommandExecution, resolver NBLLM (attributed
// to the acting user via resolver_id); type_reference_id is the command so the UI can match it to an action.
func (r *EventAnalysisRepository) InsertRemediationExecution(ctx *security.RequestContext, eventId, userId, command string, dataJSON string, statusMessage string, success bool) error {
	status := "Success"
	if !success {
		status = "Failed"
	}
	_, err := r.dbManager.Db.Exec(
		`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		common.GenerateUUID(),
		eventId,
		"CommandExecution",
		dataJSON,
		status,
		command,
		// A person clicked run; the model only proposed the command. Recording NBLLM here attributed
		// the action to the assistant and hid the human behind it, because the resolutions list shows
		// resolver_type and never resolver_id. "User" is what actually happened.
		"User",
		userId,
		statusMessage)
	if err != nil {
		return fmt.Errorf("InsertRemediationExecution: %w", err)
	}
	return nil
}

func (r *EventAnalysisRepository) InsertEventRecommendationResolution(ctx *security.RequestContext, eventId string, parentConversationId string, prUrl string) error {
	_, err := r.dbManager.Db.Exec(`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		common.GenerateUUID(),
		eventId,
		"PullRequest",
		"{}",
		"Success",
		prUrl,
		"NBLLM",
		parentConversationId,
		"PR raised successfully")
	return err
}

// GetAccountRCAFormat fetches the custom RCA format for a given account.
func (r *EventAnalysisRepository) GetAccountRCAFormat(ctx *security.RequestContext, accountId string) (string, error) {
	var format string
	query := `SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'rca_report_format'`
	err := r.dbManager.Db.Get(&format, query, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // No custom format exists
		}
		ctx.GetLogger().Error("analyzer: failed to get account RCA format", "error", err, "account_id", accountId)
		return "", err
	}
	return format, nil
}

// SetAccountRCAFormat sets the custom RCA format for a given account.
func (r *EventAnalysisRepository) SetAccountRCAFormat(ctx *security.RequestContext, accountId string, format string) error {
	if format == "" {
		// Delete the format if empty
		_, err := r.dbManager.Db.Exec(`DELETE FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'rca_report_format'`, accountId)
		return err
	}

	query := `
		INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
		VALUES ($1, 'rca_report_format', $2, NOW(), NOW())
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
	`
	_, err := r.dbManager.Db.Exec(query, accountId, format)
	return err
}
