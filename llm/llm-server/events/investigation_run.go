package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/security"
)

// With the remediation loop on, an alert has one conversation (event-<fingerprint>) for its investigation and for
// everything after it: plans, questions, approvals and runs (#40734). The investigation pipeline holds that
// conversation for its whole run with its advisory lock (EventPipelineLockKey); the other turns NudgeBee starts
// there check it (EventPipelineHoldOf) before they start.

// EventPipelineLockKey is the advisory-lock key of the event pipeline of an alert (account and fingerprint). The
// pipeline takes it for its whole run (api tryEventAnalysisLock); a turn NudgeBee starts in the alert's conversation
// must find it free.
func EventPipelineLockKey(accountId, fingerprint string) string {
	return fmt.Sprintf("event-analysis:%d:%s:%s", len(accountId), accountId, fingerprint)
}

// PipelineHeartbeatInterval is how often a running pipeline marks its IN_PROGRESS stage rows as live
// (HeartbeatEventRun); pipelineHeartbeatFresh is how recent that mark must be for the lock to count as a live run.
const (
	PipelineHeartbeatInterval = 30 * time.Second
	pipelineHeartbeatFresh    = 3 * PipelineHeartbeatInterval
)

// PipelineHold is what EventPipelineHoldOf found.
type PipelineHold int

const (
	// PipelineFree: nobody holds the alert's pipeline lock.
	PipelineFree PipelineHold = iota
	// PipelineLive: the lock is held and the run marked its rows live recently.
	PipelineLive
	// PipelineStale: the lock is held but no row of the alert was marked live recently.
	PipelineStale
)

// EventPipelineHoldOf reports whether the alert's investigation pipeline holds its conversation now. It reads
// pg_locks and never takes the lock, so it cannot make a starting pipeline lose its try-lock.
//
// The lock alone is not proof of a live run: it is a session lock on a dedicated connection, and a connection that
// dropped without Postgres noticing yet (no FIN, keepalive not expired) keeps it. So a held lock counts as live only
// while the run's IN_PROGRESS stage rows carry a fresh heartbeat (HeartbeatEventRun, every
// PipelineHeartbeatInterval). A held lock with no fresh heartbeat is PipelineStale: callers then degrade to
// treating the conversation as free of the pipeline (they start without the hold key) instead of waiting for a
// lock nobody will release until Postgres reaps the connection.
func EventPipelineHoldOf(ctx context.Context, db *sqlx.DB, accountId, fingerprint string) (PipelineHold, error) {
	var row struct {
		Locked bool `db:"locked"`
		Fresh  bool `db:"fresh"`
	}
	err := db.GetContext(ctx, &row, `WITH k AS (SELECT hashtextextended($1, 0) AS key)
		SELECT EXISTS (SELECT 1 FROM pg_locks l, k WHERE l.locktype = 'advisory' AND l.granted AND l.objsubid = 1
		         AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
		         AND l.classid = ((k.key >> 32) & 4294967295)::oid AND l.objid = (k.key & 4294967295)::oid) AS locked,
		       EXISTS (SELECT 1 FROM event_log_analysis a WHERE a.cloud_account_id = $2::uuid AND a.event_fingerprint = $3
		         AND a.status = 'IN_PROGRESS' AND a.updated_at > LOCALTIMESTAMP - make_interval(secs => $4)) AS fresh`,
		EventPipelineLockKey(accountId, fingerprint), accountId, fingerprint, pipelineHeartbeatFresh.Seconds())
	if err != nil {
		return PipelineFree, fmt.Errorf("EventPipelineHoldOf: %w", err)
	}
	switch {
	case !row.Locked:
		return PipelineFree, nil
	case row.Fresh:
		return PipelineLive, nil
	}
	return PipelineStale, nil
}

// HeartbeatEventRun marks eventId's IN_PROGRESS stage rows live (updated_at), which EventPipelineHoldOf and the
// recovery job's minimum age read. The pipeline calls it while it holds the lock.
func HeartbeatEventRun(ctx context.Context, db *sqlx.DB, accountId, eventId string) error {
	if _, err := db.ExecContext(ctx, `UPDATE event_log_analysis a SET updated_at = NOW()
		  FROM event_analysis_mapping m
		 WHERE m.event_id = $1::uuid AND a.id = m.analysis_id AND a.cloud_account_id = $2::uuid AND a.status = 'IN_PROGRESS'`,
		eventId, accountId); err != nil {
		return fmt.Errorf("HeartbeatEventRun: %w", err)
	}
	return nil
}

// EventRun is an event's current investigation run: its id (the event's mapped log_analysis row, which every new
// run of the event — its first claim, every regenerate — creates afresh, and every continuation keeps; the report
// row for a run that reuses a related event's code-fix stage and claims only its report) and when that row was
// created.
type EventRun struct {
	Id        string    `db:"id"`
	StartedAt time.Time `db:"started_at"`
}

// RunOfEvent is eventId's current run, or the zero EventRun when the event has none of its own.
func RunOfEvent(ctx context.Context, db *sqlx.DB, accountId, eventId string) (EventRun, error) {
	var run EventRun
	err := db.GetContext(ctx, &run, `SELECT a.id::text AS id, a.recorded_at AS started_at
		  FROM event_analysis_mapping m JOIN event_log_analysis a ON a.id = m.analysis_id
		 WHERE m.event_id = $1::uuid AND m.analysis_type IN ($2, $3) AND a.cloud_account_id = $4::uuid
		 ORDER BY (m.analysis_type = $2) DESC LIMIT 1`,
		eventId, string(AnalysisTypeLog), string(AnalysisTypeDetailedResponse), accountId)
	if errors.Is(err, sql.ErrNoRows) {
		return EventRun{}, nil
	}
	if err != nil {
		return EventRun{}, fmt.Errorf("RunOfEvent: %w", err)
	}
	return run, nil
}

// ParkEventAnalysis keeps eventId's investigation waiting with reason: its IN_PROGRESS stage rows get reason as
// status_reason and a fresh updated_at, and its report row is made IN_PROGRESS if it has none, so the recovery job
// finds the event and starts it once nothing holds it back. No new status: the rows stay IN_PROGRESS.
func (r *EventAnalysisRepository) ParkEventAnalysis(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey, reason string) error {
	db := r.dbManager.Db
	var hasReport bool
	if err := db.GetContext(ctx.GetContext(), &hasReport, `SELECT EXISTS (SELECT 1 FROM event_analysis_mapping WHERE event_id = $1::uuid AND analysis_type = $2)`,
		eventId, string(AnalysisTypeDetailedResponse)); err != nil {
		return fmt.Errorf("ParkEventAnalysis: %w", err)
	}
	if !hasReport {
		if err := r.UpsertEventAnalysisInProgress(ctx, eventId, fingerprint, accountId, aggKey, AnalysisTypeDetailedResponse); err != nil {
			return fmt.Errorf("ParkEventAnalysis: report row: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx.GetContext(), `UPDATE event_log_analysis a SET status_reason = $3, updated_at = NOW()
		  FROM event_analysis_mapping m
		 WHERE m.event_id = $1::uuid AND a.id = m.analysis_id AND a.cloud_account_id = $2::uuid AND a.status = 'IN_PROGRESS'`,
		eventId, accountId, reason); err != nil {
		return fmt.Errorf("ParkEventAnalysis: %w", err)
	}
	return nil
}

// AlertEpisodeStart is when the alert's current episode started: the newest firing of the alert (account and
// fingerprint, or the event id for an alert without one) that started after every earlier firing had ended — the
// boundary the Remediation tab's "fired again after it cleared" uses. nil when the alert never cleared and fired
// again (its first firing's episode is still the current one).
func AlertEpisodeStart(ctx context.Context, db *sqlx.DB, accountId, alert string) (*time.Time, error) {
	var start sql.NullTime
	if err := db.GetContext(ctx, &start, `WITH f AS (SELECT e.created_at,
		       max(COALESCE(e.ends_at, 'infinity'::timestamp)) OVER (ORDER BY e.created_at, e.id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS open_until
		  FROM events e WHERE e.cloud_account_id = $1::uuid AND COALESCE(NULLIF(e.fingerprint, ''), e.id::text) = $2)
		SELECT max(created_at) FROM f WHERE open_until < created_at`, accountId, alert); err != nil {
		return nil, fmt.Errorf("AlertEpisodeStart: %w", err)
	}
	if !start.Valid {
		return nil, nil
	}
	return &start.Time, nil
}
