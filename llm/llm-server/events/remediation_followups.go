package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"log/slog"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// RemediationLoopFeature is the tenant feature flag that turns the alert remediation loop on (#40734,
// docs/incident-agent-v1.md): the follow-up, the alert-plan chat tools and the Slack thread, for every
// account of the tenant. Only the tenant-wide row counts, as for every common.IsFeatureEnabled flag.
const RemediationLoopFeature = "EVENT_REMEDIATION_LOOP"

// remediationLoopTenant limits a query over events e to tenants with the loop on.
const remediationLoopTenant = `EXISTS (SELECT 1 FROM feature_flag ff WHERE ff.feature_id = '` + RemediationLoopFeature + `'
		   AND ff.tenant_id = e.tenant AND ff.account_id IS NULL AND ff.status = 'enabled')`

// RemediationLoopOn reports whether the loop is on for accountId's tenant. Fails closed. A var so tests can stub it.
var RemediationLoopOn = func(accountId string) bool {
	if accountId == "" {
		return false
	}
	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil || tenantId == "" {
		return false
	}
	on, err := common.IsFeatureEnabled(RemediationLoopFeature, tenantId)
	if err != nil {
		slog.Warn("remediation: unable to read the tenant feature flag, treating as off",
			"feature", RemediationLoopFeature, "tenant_id", tenantId, "error", err)
		return false
	}
	return on
}

// A remediation follow-up's state lives on its run row, in event_resolution.data->'followup' (#40734,
// api/remediation_followup.go). These are the statements that read and write it. Every update requires
// data.followup to exist: jsonb_set with a NULL value would empty the row's data.

// FollowupRun is a run row as the follow-up reads it.
type FollowupRun struct {
	Id          string    `db:"id"`
	EventId     string    `db:"event_id"`
	AccountId   string    `db:"account_id"`
	Fingerprint string    `db:"fingerprint"`
	Command     string    `db:"command"`
	Status      string    `db:"status"`
	CreatedAt   time.Time `db:"created_at"`
	Data        []byte    `db:"data"`
	ClaimedAt   string    `db:"claimed_at"`
	// MessageId is the follow-up's current turn: its query message in the alert's conversation.
	MessageId string `db:"message_id"`
}

const followupRunColumns = `er.id, er.event_id, e.cloud_account_id::text AS account_id, COALESCE(e.fingerprint, '') AS fingerprint,
	COALESCE(er.type_reference_id, '') AS command, er.status, er.created_at,
	COALESCE(er.data, '{}'::jsonb) AS data, COALESCE(er.data->'followup'->>'claimed_at', '') AS claimed_at,
	COALESCE(er.data->'followup'->>'message_id', '') AS message_id`

// ClaimDueFollowups claims at most limit settled runs (not not_sent) due a follow-up: loop on, delayMinutes old or more,
// none yet. Runs over an hour past due are skipped, so an outage or a fresh enable doesn't flood turns or replace the
// current plan. SKIP LOCKED + the NULL guard give each row to one claimer, even with overlapping leaders.
func ClaimDueFollowups(ctx context.Context, db *sqlx.DB, delayMinutes, limit int) ([]FollowupRun, error) {
	var claimed []FollowupRun
	err := db.SelectContext(ctx, &claimed, `WITH due AS (
		SELECT er.id, COALESCE(NULLIF(e.fingerprint, ''), e.id::text) AS alert FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.status IN `+SettledRunStatuses+`
		   AND COALESCE(er.data->>'not_sent', '') <> 'true' AND er.data->'followup' IS NULL
		   AND er.created_at < LOCALTIMESTAMP - make_interval(mins => $1)
		   AND er.created_at > LOCALTIMESTAMP - make_interval(mins => $1 + 60)
		   AND `+remediationLoopTenant+`
		 ORDER BY er.created_at LIMIT $2 FOR UPDATE OF er SKIP LOCKED),
	upd AS (
		UPDATE event_resolution r
		   SET data = COALESCE(NULLIF(r.data, 'null'::jsonb), '{}'::jsonb) || jsonb_build_object('followup',
		       jsonb_build_object('claimed_at', now(), 'session', '`+SessionIdPrefixEvent+`' || due.alert))
		  FROM due WHERE r.id = due.id AND r.data->'followup' IS NULL
		RETURNING r.*)
	SELECT `+followupRunColumns+` FROM upd er JOIN events e ON e.id = er.event_id`,
		delayMinutes, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimDueFollowups: %w", err)
	}
	return claimed, nil
}

// OpenFollowups are at most limit follow-ups that have not ended and are not waiting for a re-check: each
// has a turn to start (no message_id) or to watch.
func OpenFollowups(ctx context.Context, db *sqlx.DB, limit int) ([]FollowupRun, error) {
	var open []FollowupRun
	err := db.SelectContext(ctx, &open, `SELECT `+followupRunColumns+`
		  FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.data->'followup' IS NOT NULL
		   AND er.data->'followup'->>'done_at' IS NULL AND er.data->'followup'->>'recheck_at' IS NULL
		   AND `+remediationLoopTenant+`
		 ORDER BY (er.data->'followup'->>'claimed_at')::timestamptz LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("OpenFollowups: %w", err)
	}
	return open, nil
}

// ClaimDueRechecks claims at most limit follow-ups waiting for a re-check whose time has come, as a new
// claim whose turn is still to start.
func ClaimDueRechecks(ctx context.Context, db *sqlx.DB, limit int) ([]FollowupRun, error) {
	var claimed []FollowupRun
	err := db.SelectContext(ctx, &claimed, `WITH due AS (
		SELECT er.id FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.data->'followup'->>'done_at' IS NULL
		   AND (er.data->'followup'->>'recheck_at')::timestamptz <= now()
		   AND `+remediationLoopTenant+`
		 ORDER BY (er.data->'followup'->>'recheck_at')::timestamptz LIMIT $1 FOR UPDATE OF er SKIP LOCKED),
	upd AS (
		UPDATE event_resolution r
		   SET data = jsonb_set(r.data, '{followup}', ((r.data->'followup') - 'recheck_at' - 'message_id') || jsonb_build_object('claimed_at', now()))
		  FROM due WHERE r.id = due.id AND r.data->'followup'->>'recheck_at' IS NOT NULL AND r.data->'followup'->>'done_at' IS NULL
		RETURNING r.*)
	SELECT `+followupRunColumns+` FROM upd er JOIN events e ON e.id = er.event_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimDueRechecks: %w", err)
	}
	return claimed, nil
}

// SetFollowupTurn records messageId as the follow-up's current turn, replacing the turn whose message was
// expected (expected, "" for none), so two leaders do not both start one; it reports whether it did.
func SetFollowupTurn(ctx context.Context, db *sqlx.DB, runId, expected, messageId string) (bool, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || jsonb_build_object('message_id', $3::text))
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL
		   AND COALESCE(data->'followup'->>'message_id', '') = $2`, runId, expected, messageId))
	if err != nil {
		return false, fmt.Errorf("SetFollowupTurn: %w", err)
	}
	return n == 1, nil
}

// OpenFollowupOfTurn is the open follow-up on accountId whose current turn is messageId: sql.ErrNoRows
// (wrapped) when there is none.
func OpenFollowupOfTurn(ctx context.Context, db *sqlx.DB, messageId, accountId string) (FollowupRun, error) {
	var run FollowupRun
	err := db.GetContext(ctx, &run, `SELECT `+followupRunColumns+` FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.data->'followup'->>'message_id' = $1 AND e.cloud_account_id::text = $2
		   AND er.data->'followup'->>'done_at' IS NULL`, messageId, accountId)
	if err != nil {
		return run, fmt.Errorf("OpenFollowupOfTurn: %w", err)
	}
	return run, nil
}

// ScheduleFollowupRecheck turns the turn's recheck_request into the time to look again, between minMinutes
// and maxMinutes, and returns it; empty when the turn asked for none, proposed, or the follow-up has used
// its maxRechecks. A non-empty summary is kept as the last report while it waits.
func ScheduleFollowupRecheck(ctx context.Context, db *sqlx.DB, runId, summary string, minMinutes, maxMinutes, maxRechecks int) (string, error) {
	var at string
	err := db.GetContext(ctx, &at, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', ((data->'followup') - 'recheck_request') || jsonb_build_object(
		       'recheck_at', now() + make_interval(mins => LEAST(GREATEST((data->'followup'->'recheck_request'->>'minutes')::int, $3), $4)),
		       'recheck_reason', data->'followup'->'recheck_request'->>'reason',
		       'rechecks', COALESCE((data->'followup'->>'rechecks')::int, 0) + 1)
		       || CASE WHEN $2::text = '' THEN '{}'::jsonb ELSE jsonb_build_object('summary', $2::text) END)
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL
		   AND data->'followup'->'recheck_request' IS NOT NULL AND data->'followup'->>'proposed_at' IS NULL
		   AND COALESCE((data->'followup'->>'rechecks')::int, 0) < $5
		RETURNING data->'followup'->>'recheck_at'`, runId, summary, minMinutes, maxMinutes, maxRechecks)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ScheduleFollowupRecheck: %w", err)
	}
	return at, nil
}

// RequestFollowupRecheck records the turn's request to look again in minutes, for reason, unless the
// follow-up ended, proposed, or used its maxRechecks; it returns the rows changed (1 when recorded).
func RequestFollowupRecheck(ctx context.Context, db *sqlx.DB, runId string, minutes int, reason string, maxRechecks int) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || jsonb_build_object('recheck_request',
		       jsonb_build_object('minutes', $2::int, 'reason', $3::text)))
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL
		   AND data->'followup'->>'proposed_at' IS NULL
		   AND COALESCE((data->'followup'->>'rechecks')::int, 0) < $4`, runId, minutes, reason, maxRechecks))
	if err != nil {
		return 0, fmt.Errorf("RequestFollowupRecheck: %w", err)
	}
	return n, nil
}

// ClaimFollowupProposal takes the follow-up's one proposal, with findings, before the plan is written, so a
// parallel or repeated call loses; it returns the rows changed (1 when taken).
func ClaimFollowupProposal(ctx context.Context, db *sqlx.DB, runId, findings string) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || jsonb_build_object('findings', $2::text, 'proposed_at', now()))
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL AND data->'followup'->>'proposed_at' IS NULL`, runId, findings))
	if err != nil {
		return 0, fmt.Errorf("ClaimFollowupProposal: %w", err)
	}
	return n, nil
}

// ReleaseFollowupProposal frees the proposal taken by ClaimFollowupProposal when nothing was proposed.
func ReleaseFollowupProposal(ctx context.Context, db *sqlx.DB, runId string) error {
	if _, err := db.ExecContext(ctx, `UPDATE event_resolution SET data = jsonb_set(data, '{followup}', (data->'followup') - 'proposed_at') WHERE id = $1 AND data->'followup' IS NOT NULL`, runId); err != nil {
		return fmt.Errorf("ReleaseFollowupProposal: %w", err)
	}
	return nil
}

// LinkFollowupProposal records the plan version the follow-up proposed.
func LinkFollowupProposal(ctx context.Context, db *sqlx.DB, runId, planVersion string) error {
	if _, err := db.ExecContext(ctx, `UPDATE event_resolution SET data = jsonb_set(data, '{followup}', data->'followup' || jsonb_build_object('proposed_plan_version', $2::text)) WHERE id = $1 AND data->'followup' IS NOT NULL`,
		runId, planVersion); err != nil {
		return fmt.Errorf("LinkFollowupProposal: %w", err)
	}
	return nil
}

// FollowupProposedVersion is the plan version the follow-up of runId proposed, or "".
func FollowupProposedVersion(ctx context.Context, db *sqlx.DB, runId string) (string, error) {
	var proposed string
	if err := db.GetContext(ctx, &proposed, `SELECT COALESCE(data->'followup'->>'proposed_plan_version', '') FROM event_resolution WHERE id = $1`, runId); err != nil {
		return "", fmt.Errorf("FollowupProposedVersion: %w", err)
	}
	return proposed, nil
}

// FinishFollowup records how a follow-up ended (patch, a JSON object), once; it returns the rows changed
// (1 when this call ended it).
func FinishFollowup(ctx context.Context, db *sqlx.DB, runId, patch string) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || $2::jsonb || jsonb_build_object('done_at', now()))
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL`, runId, patch))
	if err != nil {
		return 0, fmt.Errorf("FinishFollowup: %w", err)
	}
	return n, nil
}

// AlertRunInFlight reports whether a run of the alert (account and fingerprint, or the event id when it has
// none) is running or its follow-up has not ended.
func AlertRunInFlight(ctx context.Context, db *sqlx.DB, accountId, fingerprint string) (bool, error) {
	var busy bool
	if err := db.GetContext(ctx, &busy, `SELECT EXISTS (SELECT 1 FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND e.cloud_account_id::text = $1 AND COALESCE(NULLIF(e.fingerprint, ''), e.id::text) = $2
		   AND (er.status = 'InProgress' OR (er.data->'followup' IS NOT NULL AND er.data->'followup'->>'done_at' IS NULL)))`,
		accountId, fingerprint); err != nil {
		return false, fmt.Errorf("AlertRunInFlight: %w", err)
	}
	return busy, nil
}

func affected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
