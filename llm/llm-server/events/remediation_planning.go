package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// The background planner (#40734): Nubi writes each alert's remediation plan in a turn of the alert's
// conversation (api/remediation_planning.go). A request for that turn is a row of event_log_analysis with
// analysis_type remediation_planning, one per request, holding its state as JSON in analysis. The row is
// never IN_PROGRESS: stuck-analysis recovery (ListInProgressAnalysis) restarts every IN_PROGRESS row as an
// investigation. status is COMPLETED, or FAILED with status_reason when the request ended with no plan.

// AnalysisTypeRemediationPlanning is a background planning request's row.
const AnalysisTypeRemediationPlanning EventAnalysisType = "remediation_planning"

// Planning request states.
const (
	PlanningQueued  = "queued"  // waiting for the alert's conversation to be free
	PlanningRunning = "running" // its turn started (MessageId)
	PlanningDone    = "done"    // the turn wrote the plan (PlanVersion)
	PlanningNone    = "none"    // it ended with no plan; Reason says why
	// PlanningParked: an automatic re-check held while an approval waits in the alert's chat; it runs when the
	// approval is denied or lapses, and is dropped when the approved step ran.
	PlanningParked = "parked"
	// PlanningDropped: an automatic re-check not needed any more (Reason: ran, a step ran since it was asked).
	PlanningDropped = "dropped"
)

// PlanningRequest is one request for a planning turn. Its JSON fields are the row's analysis.
type PlanningRequest struct {
	Id        string `db:"id" json:"-"`
	EventId   string `db:"event_id" json:"-"`
	AccountId string `db:"account_id" json:"-"`
	// Alert is the event's fingerprint, or its id when it has none: the conversation is event-<Alert>.
	Alert  string `db:"alert" json:"-"`
	Reason string `db:"reason" json:"-"`
	Raw    []byte `db:"analysis" json:"-"`

	State       string    `json:"state"`
	Kind        string    `json:"kind"`
	RequestedAt time.Time `json:"requested_at"`
	RequestedBy string    `json:"requested_by,omitempty"`
	// Attempts counts turns started for the request; a failed turn is started again while attempts are left.
	Attempts  int        `json:"attempts,omitempty"`
	MessageId string     `json:"message_id,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Reasons   []string   `json:"reasons,omitempty"`
	Note      string     `json:"note,omitempty"`
	// BaseVersion is the plan version the request was made against (recheck, replan).
	BaseVersion string `json:"base_version,omitempty"`
	PlanVersion string `json:"plan_version,omitempty"`
	Proposing   bool   `json:"proposing,omitempty"`
}

// Open reports whether the request has not ended.
func (p PlanningRequest) Open() bool {
	return p.State == PlanningQueued || p.State == PlanningRunning || p.State == PlanningParked
}

const planningColumns = `a.id, a.event_id::text AS event_id, a.cloud_account_id::text AS account_id,
	COALESCE(NULLIF(a.event_fingerprint, ''), a.event_id::text) AS alert, COALESCE(a.status_reason, '') AS reason, a.analysis`

// planningField reads a field of a planning row's JSON. The CASE keeps the cast off every other row: their
// analysis is text, and AND does not fix the order Postgres evaluates its terms in.
func planningField(field string) string {
	return `(CASE WHEN a.analysis_type = '` + string(AnalysisTypeRemediationPlanning) + `' THEN (a.analysis::jsonb)->>'` + field + `' END)`
}

var planningOpen = `a.analysis_type = '` + string(AnalysisTypeRemediationPlanning) + `' AND a.status = 'COMPLETED'
	AND ` + planningField("state") + ` IN ('` + PlanningQueued + `', '` + PlanningRunning + `', '` + PlanningParked + `')`

func decodePlanning(rows []PlanningRequest) ([]PlanningRequest, error) {
	for i := range rows {
		raw := rows[i].Raw
		if err := json.Unmarshal(raw, &rows[i]); err != nil {
			return nil, fmt.Errorf("planning request %s: %w", rows[i].Id, err)
		}
		rows[i].Raw = raw
	}
	return rows, nil
}

// EnqueuePlanning records a planning request for the event, unless one is open: then that one is returned with
// queued false. The event's fingerprint and aggregation key come from the event row, on accountId only.
func EnqueuePlanning(ctx context.Context, db *sqlx.DB, accountId, eventId string, req PlanningRequest) (PlanningRequest, bool, error) {
	req.State = PlanningQueued
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}
	data, err := json.Marshal(req)
	if err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: %w", err)
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "remediation_planning:"+accountId+":"+eventId); err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: lock: %w", err)
	}
	var open []PlanningRequest
	if err := tx.SelectContext(ctx, &open, `SELECT `+planningColumns+` FROM event_log_analysis a
		 WHERE a.event_id = $1 AND a.cloud_account_id = $2::uuid AND `+planningOpen+` ORDER BY a.recorded_at DESC LIMIT 1`, eventId, accountId); err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: open request: %w", err)
	}
	if len(open) > 0 {
		decoded, err := decodePlanning(open)
		if err != nil {
			return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: %w", err)
		}
		return decoded[0], false, nil
	}
	var rows []PlanningRequest
	if err := tx.SelectContext(ctx, &rows, `WITH ins AS (
		INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		SELECT e.id, COALESCE(NULLIF(e.fingerprint, ''), e.id::text), $3, $4, 'COMPLETED', e.cloud_account_id, COALESCE(e.aggregation_key, ''), $5
		  FROM events e WHERE e.id = $1 AND e.cloud_account_id = $2::uuid
		RETURNING *)
		SELECT `+planningColumns+` FROM ins a`, eventId, accountId, string(data), req.Kind, AnalysisTypeRemediationPlanning); err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: insert: %w", err)
	}
	if len(rows) == 0 {
		return PlanningRequest{}, false, errors.New("EnqueuePlanning: event not found")
	}
	if err := tx.Commit(); err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: commit: %w", err)
	}
	decoded, err := decodePlanning(rows)
	if err != nil {
		return PlanningRequest{}, false, fmt.Errorf("EnqueuePlanning: %w", err)
	}
	return decoded[0], true, nil
}

// OpenPlannings are at most limit planning requests that have not ended, oldest first, of tenants with the loop on.
func OpenPlannings(ctx context.Context, db *sqlx.DB, limit int) ([]PlanningRequest, error) {
	var rows []PlanningRequest
	if err := db.SelectContext(ctx, &rows, `SELECT `+planningColumns+` FROM event_log_analysis a JOIN events e ON e.id = a.event_id
		 WHERE `+planningOpen+` AND `+remediationLoopTenant+`
		 ORDER BY a.recorded_at LIMIT $1`, limit); err != nil {
		return nil, fmt.Errorf("OpenPlannings: %w", err)
	}
	return decodePlanning(rows)
}

// LatestPlanning is the event's newest planning request, or nil.
func LatestPlanning(ctx context.Context, db sqlx.QueryerContext, accountId, eventId string) (*PlanningRequest, error) {
	var rows []PlanningRequest
	if err := sqlx.SelectContext(ctx, db, &rows, `SELECT `+planningColumns+` FROM event_log_analysis a
		 WHERE a.event_id = $1 AND a.cloud_account_id = $2::uuid AND a.analysis_type = $3
		 ORDER BY a.recorded_at DESC LIMIT 1`, eventId, accountId, AnalysisTypeRemediationPlanning); err != nil {
		return nil, fmt.Errorf("LatestPlanning: %w", err)
	}
	decoded, err := decodePlanning(rows)
	if err != nil || len(decoded) == 0 {
		return nil, err
	}
	return &decoded[0], nil
}

// PlanningOfTurn is the running planning request whose turn is messageId, on accountId.
func PlanningOfTurn(ctx context.Context, db *sqlx.DB, accountId, messageId string) (PlanningRequest, error) {
	var rows []PlanningRequest
	if err := db.SelectContext(ctx, &rows, `SELECT `+planningColumns+` FROM event_log_analysis a
		 WHERE a.cloud_account_id = $1::uuid AND `+planningOpen+` AND `+planningField("message_id")+` = $2 LIMIT 1`, accountId, messageId); err != nil {
		return PlanningRequest{}, fmt.Errorf("PlanningOfTurn: %w", err)
	}
	decoded, err := decodePlanning(rows)
	if err != nil {
		return PlanningRequest{}, fmt.Errorf("PlanningOfTurn: %w", err)
	}
	if len(decoded) == 0 {
		return PlanningRequest{}, sql.ErrNoRows
	}
	return decoded[0], nil
}

// patchOpenPlanning merges patch into an open request's JSON when where (over a) holds, and reports whether it did.
func patchOpenPlanning(ctx context.Context, db *sqlx.DB, id, where string, patch map[string]any, args ...any) (bool, error) {
	data, err := json.Marshal(patch)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `UPDATE event_log_analysis a SET analysis = ((a.analysis::jsonb) || $2::jsonb)::text, updated_at = NOW()
		 WHERE a.id = $1 AND `+planningOpen+` AND `+where, append([]any{id, string(data)}, args...)...)
	n, err := affected(res, err)
	return n == 1, err
}

// SetPlanningTurn records messageId as the request's turn (running), or with messageId "" puts it back to queued,
// only while its turn is expected (""  for none). It reports whether it did: false means another worker moved it.
func SetPlanningTurn(ctx context.Context, db *sqlx.DB, id, expected, messageId string) (bool, error) {
	patch := map[string]any{"state": PlanningQueued, "message_id": ""}
	if messageId != "" {
		patch = map[string]any{"state": PlanningRunning, "message_id": messageId, "started_at": time.Now().UTC()}
	}
	set, err := patchOpenPlanning(ctx, db, id, `COALESCE(`+planningField("message_id")+`, '') = $3`, patch, expected)
	if err != nil {
		return false, fmt.Errorf("SetPlanningTurn: %w", err)
	}
	if set && messageId != "" {
		if _, err := db.ExecContext(ctx, `UPDATE event_log_analysis SET analysis = jsonb_set(analysis::jsonb, '{attempts}',
			to_jsonb(COALESCE((analysis::jsonb->>'attempts')::int, 0) + 1))::text WHERE id = $1`, id); err != nil {
			return true, fmt.Errorf("SetPlanningTurn: attempts: %w", err)
		}
	}
	return set, nil
}

// ParkPlanning holds a queued request (parked); UnparkPlanning queues a parked one again, its wait starting now.
// Each reports whether it moved the request (false: another worker did, or it changed).
func ParkPlanning(ctx context.Context, db *sqlx.DB, id string) (bool, error) {
	moved, err := patchOpenPlanning(ctx, db, id, planningField("state")+` = '`+PlanningQueued+`'`, map[string]any{"state": PlanningParked, "parked_at": time.Now().UTC()})
	if err != nil {
		return false, fmt.Errorf("ParkPlanning: %w", err)
	}
	return moved, nil
}

func UnparkPlanning(ctx context.Context, db *sqlx.DB, id string) (bool, error) {
	moved, err := patchOpenPlanning(ctx, db, id, planningField("state")+` = '`+PlanningParked+`'`, map[string]any{"state": PlanningQueued, "requested_at": time.Now().UTC()})
	if err != nil {
		return false, fmt.Errorf("UnparkPlanning: %w", err)
	}
	return moved, nil
}

// TakeOverParkedPlanning turns a parked automatic request into the person's request: theirs runs, queued, once the
// alert's chat is free. It reports whether it did.
func TakeOverParkedPlanning(ctx context.Context, db *sqlx.DB, id string, req PlanningRequest) (bool, error) {
	moved, err := patchOpenPlanning(ctx, db, id, planningField("state")+` = '`+PlanningParked+`'`, map[string]any{
		"state": PlanningQueued, "kind": req.Kind, "requested_at": time.Now().UTC(), "requested_by": req.RequestedBy,
		"reasons": req.Reasons, "note": req.Note, "base_version": req.BaseVersion})
	if err != nil {
		return false, fmt.Errorf("TakeOverParkedPlanning: %w", err)
	}
	return moved, nil
}

// PlanningRanSince reports whether a step of the request's alert ran (a recorded run, not declined, sent) after
// the request was made.
func PlanningRanSince(ctx context.Context, db *sqlx.DB, id string) (bool, error) {
	var ran bool
	err := db.GetContext(ctx, &ran, `SELECT EXISTS (SELECT 1 FROM event_log_analysis a
		  JOIN events pe ON pe.id = a.event_id
		  JOIN events e ON e.cloud_account_id = pe.cloud_account_id
		       AND (e.id = pe.id OR (NULLIF(pe.fingerprint, '') IS NOT NULL AND e.fingerprint = pe.fingerprint))
		  JOIN event_resolution er ON er.event_id = e.id
		 WHERE a.id = $1 AND er.type = 'CommandExecution' AND er.status <> 'Declined'
		   AND COALESCE(er.data->>'not_sent', '') <> 'true' AND er.created_at > a.recorded_at)`, id)
	if err != nil {
		return false, fmt.Errorf("PlanningRanSince: %w", err)
	}
	return ran, nil
}

// ClaimPlanningProposal takes the one plan a running request's turn writes; false when the turn already holds it.
func ClaimPlanningProposal(ctx context.Context, db *sqlx.DB, id string) (bool, error) {
	claimed, err := patchOpenPlanning(ctx, db, id, `COALESCE(`+planningField("proposing")+`, 'false') <> 'true'`, map[string]any{"proposing": true})
	if err != nil {
		return false, fmt.Errorf("ClaimPlanningProposal: %w", err)
	}
	return claimed, nil
}

// ReleasePlanningProposal frees the claim after a plan that was not stored.
func ReleasePlanningProposal(ctx context.Context, db *sqlx.DB, id string) error {
	if _, err := patchOpenPlanning(ctx, db, id, `TRUE`, map[string]any{"proposing": false}); err != nil {
		return fmt.Errorf("ReleasePlanningProposal: %w", err)
	}
	return nil
}

// FinishPlanning ends an open request: done with the plan version it wrote, dropped with the reason, or none with
// the reason. It reports whether it ended it (false: already ended).
func FinishPlanning(ctx context.Context, db *sqlx.DB, id, state, planVersion, reason string) (bool, error) {
	status, statusReason := "COMPLETED", sql.NullString{}
	switch state {
	case PlanningDone:
	case PlanningDropped:
		statusReason = sql.NullString{String: reason, Valid: reason != ""}
	default:
		state, status, statusReason = PlanningNone, "FAILED", sql.NullString{String: reason, Valid: reason != ""}
	}
	patch, err := json.Marshal(map[string]any{"state": state, "plan_version": planVersion, "proposing": false})
	if err != nil {
		return false, fmt.Errorf("FinishPlanning: %w", err)
	}
	res, err := db.ExecContext(ctx, `UPDATE event_log_analysis a SET analysis = ((a.analysis::jsonb) || $2::jsonb)::text,
		       status = $3, status_reason = $4, updated_at = NOW()
		 WHERE a.id = $1 AND `+planningOpen, id, string(patch), status, statusReason)
	n, err := affected(res, err)
	if err != nil {
		return false, fmt.Errorf("FinishPlanning: %w", err)
	}
	return n == 1, nil
}
