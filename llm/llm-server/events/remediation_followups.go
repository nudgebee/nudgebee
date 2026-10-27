package events

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
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
	on, err := RemediationLoopRead(accountId)
	if err != nil {
		slog.Warn("remediation: unable to read the tenant feature flag, treating as off",
			"feature", RemediationLoopFeature, "account_id", accountId, "error", err)
		return false
	}
	return on
}

// RemediationLoopRead is RemediationLoopOn without failing closed: err says the flag could not be read, so
// a caller that shows state can say so instead of showing the loop as off. A var so tests can stub it.
// A read is kept per account for remediationLoopCacheTTL, so one request (which asks several times) reads the
// flag once; a failed read is never kept, so the next call reads again and an error still fails closed.
var RemediationLoopRead = func(accountId string) (bool, error) {
	if accountId == "" {
		return false, nil
	}
	if on, ok := remediationLoopCache.get(accountId, time.Now()); ok {
		return on, nil
	}
	on, err := loadRemediationLoopFlag(accountId)
	if err != nil {
		return false, err
	}
	remediationLoopCache.put(accountId, on, time.Now())
	return on, nil
}

// loadRemediationLoopFlag reads the flag from the database. A var so tests can count and fail reads.
var loadRemediationLoopFlag = func(accountId string) (bool, error) {
	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return false, fmt.Errorf("remediation loop flag: tenant not read: %w", err)
	}
	if tenantId == "" {
		return false, nil
	}
	on, err := common.IsFeatureEnabled(RemediationLoopFeature, tenantId)
	if err != nil {
		return false, fmt.Errorf("remediation loop flag: not read: %w", err)
	}
	return on, nil
}

// remediationLoopCacheTTL is how long a flag read is kept: a flip in Tenant Settings takes effect within it.
const remediationLoopCacheTTL = 30 * time.Second

type loopFlagEntry struct {
	on bool
	at time.Time
}

type loopFlagCache struct {
	mu      sync.Mutex
	entries map[string]loopFlagEntry
}

var remediationLoopCache = &loopFlagCache{entries: map[string]loopFlagEntry{}}

func (c *loopFlagCache) get(accountId string, now time.Time) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[accountId]
	if !ok || now.Sub(e.at) >= remediationLoopCacheTTL || now.Before(e.at) {
		return false, false
	}
	return e.on, true
}

func (c *loopFlagCache) put(accountId string, on bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[accountId] = loopFlagEntry{on: on, at: now}
}

// ResetRemediationLoopCache forgets every kept flag read: for tests that turn the flag on or off in the database.
func ResetRemediationLoopCache() {
	remediationLoopCache.mu.Lock()
	defer remediationLoopCache.mu.Unlock()
	remediationLoopCache.entries = map[string]loopFlagEntry{}
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
		   AND (er.data->'followup'->'wait' IS NULL OR COALESCE(er.data->'followup'->>'message_id', '') <> '')
		   AND `+remediationLoopTenant+`
		 ORDER BY (er.data->'followup'->>'claimed_at')::timestamptz LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("OpenFollowups: %w", err)
	}
	return open, nil
}

// ClaimDueRechecks claims at most limit follow-ups waiting for a re-check whose time has come, as a new
// claim whose turn is still to start. Only rows from before waits ran on watches (#42461): a row with a wait is
// claimed when its watch ends (ClaimFinishedWaits), even though it also carries recheck_at for older pods.
func ClaimDueRechecks(ctx context.Context, db *sqlx.DB, limit int) ([]FollowupRun, error) {
	var claimed []FollowupRun
	err := db.SelectContext(ctx, &claimed, `WITH due AS (
		SELECT er.id FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.data->'followup' IS NOT NULL AND er.data->'followup'->>'done_at' IS NULL
		   AND (er.data->'followup'->>'recheck_at')::timestamptz <= now()
		   AND er.data->'followup'->'wait' IS NULL
		   AND `+remediationLoopTenant+`
		 ORDER BY (er.data->'followup'->>'recheck_at')::timestamptz LIMIT $1 FOR UPDATE OF er SKIP LOCKED),
	upd AS (
		UPDATE event_resolution r
		   SET data = jsonb_set(r.data, '{followup}', ((r.data->'followup') - 'recheck_at' - 'message_id') || jsonb_build_object('claimed_at', now()))
		  FROM due WHERE r.id = due.id AND r.data->'followup'->>'recheck_at' IS NOT NULL AND r.data->'followup'->>'done_at' IS NULL
		   AND r.data->'followup'->'wait' IS NULL
		RETURNING r.*)
	SELECT `+followupRunColumns+` FROM upd er JOIN events e ON e.id = er.event_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimDueRechecks: %w", err)
	}
	return claimed, nil
}

// ClaimFinishedWaits claims at most limit follow-ups whose wait's watch ended (#42461): matched (COMPLETED), its
// deadline passed (EXPIRED) or it could not read (FAILED). A watch that is gone, or still live or cancelled
// graceMinutes past the wait's deadline (the watcher is not running, or something else cancelled it), claims too,
// as lost — never earlier for a cancel, so cancelling a wait does not start the next look early. The claim
// removes the wait (and the recheck_at kept for older pods), so a late end of the same watch cannot claim twice,
// and keeps how it ended as wait_ended for the next turn. Only rows with no turn running.
func ClaimFinishedWaits(ctx context.Context, db *sqlx.DB, graceMinutes, limit int) ([]FollowupRun, error) {
	var claimed []FollowupRun
	err := db.SelectContext(ctx, &claimed, `WITH waiting AS (
		SELECT er.id, e.cloud_account_id AS account_id, er.data->'followup'->'wait' AS wait,
		       CASE WHEN er.data->'followup'->'wait'->>'watch_id' ~ '^`+uuidPattern+`$'
		            THEN (er.data->'followup'->'wait'->>'watch_id')::uuid END AS watch_id
		  FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.data->'followup'->'wait' IS NOT NULL
		   AND er.data->'followup'->>'done_at' IS NULL AND COALESCE(er.data->'followup'->>'message_id', '') = ''
		   AND `+remediationLoopTenant+`
		 FOR UPDATE OF er SKIP LOCKED),
	due AS (
		SELECT w.id, jsonb_build_object(
		         'status', CASE WHEN t.id IS NULL THEN 'LOST'
		                        WHEN t.status IN ('COMPLETED','EXPIRED','FAILED') THEN t.status ELSE 'LOST' END,
		         'kind', w.wait->>'kind', 'command', w.wait->>'command', 'expect', w.wait->>'expect',
		         'negate', w.wait->'negate', 'until', w.wait->>'until', 'reason', w.wait->>'reason',
		         'ended_at', COALESCE(t.updated_at, now()), 'final', t.final_result, 'last', t.last_poll_result,
		         'error', t.error) AS ended
		  FROM waiting w
		  LEFT JOIN llm_watch_tasks t ON t.id = w.watch_id AND t.account_id = w.account_id
		 WHERE t.id IS NULL OR t.status IN ('COMPLETED','EXPIRED','FAILED')
		    OR (t.status IN ('PENDING','ACTIVE','CANCELLED') AND (w.wait->>'until')::timestamptz + make_interval(mins => $1) < now())
		 LIMIT $2),
	upd AS (
		UPDATE event_resolution r
		   SET data = jsonb_set(r.data, '{followup}', ((r.data->'followup') - 'wait' - 'recheck_at' - 'message_id')
		       || jsonb_build_object('claimed_at', now(), 'wait_ended', due.ended))
		  FROM due WHERE r.id = due.id AND r.data->'followup'->'wait' IS NOT NULL AND r.data->'followup'->>'done_at' IS NULL
		   AND COALESCE(r.data->'followup'->>'message_id', '') = ''
		RETURNING r.*)
	SELECT `+followupRunColumns+` FROM upd er JOIN events e ON e.id = er.event_id`, graceMinutes, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimFinishedWaits: %w", err)
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
		 WHERE er.type = 'CommandExecution' AND er.data->'followup'->>'message_id' = $1 AND e.cloud_account_id = $2::uuid
		   AND er.data->'followup'->>'done_at' IS NULL`, messageId, accountId)
	if err != nil {
		return run, fmt.Errorf("OpenFollowupOfTurn: %w", err)
	}
	return run, nil
}

// ScheduleFollowupWait records the wait for the turn's recheck_request (#42461): wait (a JSON object holding its
// watch_id, kind, command, expect, negate, until and reason), recheck_reason, one more re-check, recheck_at = until
// for older pods, and summary as the last report while it waits; the turn that asked is over, so its message_id
// goes (a claim takes only rows with no turn). It returns the rows changed; 0 when the follow-up
// ended, proposed, or used its maxRechecks meanwhile (the caller then cancels the watch).
func ScheduleFollowupWait(ctx context.Context, db *sqlx.DB, runId, summary, wait, until, reason string, maxRechecks int) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', ((data->'followup') - 'recheck_request' - 'wait_ended' - 'message_id') || jsonb_build_object(
		       'wait', $3::jsonb, 'recheck_at', $4::text, 'recheck_reason', $5::text,
		       'rechecks', COALESCE((data->'followup'->>'rechecks')::int, 0) + 1)
		       || CASE WHEN $2::text = '' THEN '{}'::jsonb ELSE jsonb_build_object('summary', $2::text) END)
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL
		   AND data->'followup'->'recheck_request' IS NOT NULL AND data->'followup'->>'proposed_at' IS NULL
		   AND COALESCE((data->'followup'->>'rechecks')::int, 0) < $6`, runId, summary, wait, until, reason, maxRechecks))
	if err != nil {
		return 0, fmt.Errorf("ScheduleFollowupWait: %w", err)
	}
	return n, nil
}

// RequestFollowupRecheck records the turn's request to look again in minutes, for reason, unless the
// follow-up ended, proposed, or used its maxRechecks; it returns the rows changed (1 when recorded).
//
// until (#42461), when set, makes the wait end as soon as a read matches: {command, expect, negate}.
func RequestFollowupRecheck(ctx context.Context, db *sqlx.DB, runId string, minutes int, reason string, until RecheckCondition, maxRechecks int) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || jsonb_build_object('recheck_request',
		       jsonb_build_object('minutes', $2::int, 'reason', $3::text)
		       || CASE WHEN $5::text = '' THEN '{}'::jsonb
		               ELSE jsonb_build_object('until_command', $5::text, 'until_expect', $6::text, 'until_negate', $7::bool) END))
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL
		   AND data->'followup'->>'proposed_at' IS NULL
		   AND COALESCE((data->'followup'->>'rechecks')::int, 0) < $4`, runId, minutes, reason, maxRechecks, until.Command, until.Expect, until.Negate))
	if err != nil {
		return 0, fmt.Errorf("RequestFollowupRecheck: %w", err)
	}
	return n, nil
}

// RecheckCondition is what a re-check waits for besides its deadline (#42461): a read of the alert's own cluster
// whose output matches Expect (a regular expression), or stops matching it with Negate. The zero value: a timer.
type RecheckCondition struct {
	Command string
	Expect  string
	Negate  bool
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
		 WHERE er.type = 'CommandExecution' AND e.cloud_account_id = $1::uuid AND COALESCE(NULLIF(e.fingerprint, ''), e.id::text) = $2
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

// AlertSignal is the alert's own state as the follow-up's verdict reads it: the run's event subject, and every
// firing of the alert (account and fingerprint, or the event alone when it has none) since the run's event
// started, with the source's status and times. The verdict decides from the firings (api/remediation_verdict.go).
type AlertSignal struct {
	// The run's event subject, for the in-cluster check while the source still fires.
	SubjectType string `db:"subject_type"`
	SubjectName string `db:"subject_name"`
	SubjectNS   string `db:"subject_namespace"`
	Owner       string `db:"subject_owner"`
	OwnerKind   string `db:"subject_owner_kind"`
	// Firings are the alert's events since the run's event started, newest first (at most maxAlertFirings, and the
	// run's event itself).
	Firings []AlertFiring `db:"-"`
	// RuleForSeconds is the alert rule's `for:` (event_rules.duration of the rule the event names), when
	// RuleForKnown: how long a new firing of it, such as a replacement pod's, waits before it fires.
	RuleForSeconds int  `db:"-"`
	RuleForKnown   bool `db:"-"`
}

// AlertFiring is one event of an alert. A source may record one transition as two rows a second apart; the
// verdict only asks whether any firing qualifies, so twins never count twice.
type AlertFiring struct {
	Id string `db:"id"`
	// Status is the source's status (FIRING, CLOSED, RESOLVED).
	Status string `db:"status"`
	// StartsAt and EndsAt are when it started and ended, as the source said; nil when it did not say.
	StartsAt    *time.Time `db:"starts_at"`
	EndsAt      *time.Time `db:"ends_at"`
	SubjectType string     `db:"subject_type"`
	SubjectName string     `db:"subject_name"`
	SubjectNS   string     `db:"subject_namespace"`
}

// maxAlertFirings bounds the firings ReadAlertSignal returns.
const maxAlertFirings = 200

// ReadAlertSignal reads the alert of eventId on accountId: the event's subject, the alert's firings since the
// event started, and its rule's `for:`. sql.ErrNoRows (wrapped) when the event is not on the account.
//
// The firings are the event's own fingerprint and, for an alert on a pod with a known owner, every firing of the
// same alert (aggregation key) on a pod of the same workload: a rollout restart or a pod delete replaces the pod,
// and the replacement's alert has a fingerprint of its own.
func ReadAlertSignal(ctx context.Context, db *sqlx.DB, accountId, eventId string) (AlertSignal, error) {
	var s AlertSignal
	if err := db.GetContext(ctx, &s, `SELECT COALESCE(subject_type, '') AS subject_type, COALESCE(subject_name, '') AS subject_name,
		       COALESCE(subject_namespace, '') AS subject_namespace, COALESCE(subject_owner, '') AS subject_owner,
		       COALESCE(subject_owner_kind, '') AS subject_owner_kind
		  FROM events WHERE id = $2::uuid AND cloud_account_id = $1::uuid`, accountId, eventId); err != nil {
		return s, fmt.Errorf("ReadAlertSignal: %w", err)
	}
	if err := db.SelectContext(ctx, &s.Firings, alertFiringsSQL, accountId, eventId, maxAlertFirings); err != nil {
		return s, fmt.Errorf("ReadAlertSignal: firings: %w", err)
	}
	var rule []sql.NullString
	if err := db.SelectContext(ctx, &rule, alertRuleForSQL, accountId, eventId); err != nil {
		return s, fmt.Errorf("ReadAlertSignal: rule: %w", err)
	}
	if len(rule) > 0 {
		s.RuleForSeconds, s.RuleForKnown = ruleForSeconds(rule[0].String)
	}
	return s, nil
}

// alertFiringsSQL reads the firings of the alert of event $2 on account $1, newest first: the event, its
// fingerprint's events, and for a pod alert with an owner the same alert on the owner's pods. Each branch has an
// index (events_fingerprint_account_starts_idx, idx_events_tenant_account_owner). At most the $3 newest, and the
// event itself always: the verdict reads the run's own firing even when the alert fired more often than that since.
const alertFiringsSQL = `WITH r AS (
		SELECT id, tenant, cloud_account_id, NULLIF(fingerprint, '') AS fingerprint, starts_at, aggregation_key,
		       subject_owner, COALESCE(subject_owner_kind, '') AS owner_kind, COALESCE(subject_namespace, '') AS ns,
		       lower(COALESCE(subject_type, '')) = 'pod' AND COALESCE(subject_owner, '') <> '' AS by_owner
		  FROM events WHERE id = $2::uuid AND cloud_account_id = $1::uuid),
	f AS (
		SELECT e.id FROM r JOIN events e ON e.id = r.id
		UNION
		SELECT e.id FROM r JOIN events e ON e.cloud_account_id = r.cloud_account_id AND e.fingerprint = r.fingerprint
		 WHERE r.starts_at IS NULL OR e.starts_at >= r.starts_at
		UNION
		SELECT e.id FROM r JOIN events e ON e.tenant = r.tenant AND e.cloud_account_id = r.cloud_account_id
		   AND e.subject_owner = r.subject_owner AND COALESCE(e.subject_owner_kind, '') = r.owner_kind
		   AND COALESCE(e.subject_namespace, '') = r.ns AND e.aggregation_key IS NOT DISTINCT FROM r.aggregation_key
		   AND lower(COALESCE(e.subject_type, '')) = 'pod'
		 WHERE r.by_owner AND (r.starts_at IS NULL OR e.starts_at >= r.starts_at))
	SELECT e.id::text AS id, COALESCE(e.status, '') AS status, e.starts_at, e.ends_at,
	       COALESCE(e.subject_type, '') AS subject_type, COALESCE(e.subject_name, '') AS subject_name,
	       COALESCE(e.subject_namespace, '') AS subject_namespace
	  FROM events e
	 WHERE e.id IN (SELECT id FROM r
	                UNION
	                (SELECT f.id FROM f JOIN events fe ON fe.id = f.id ORDER BY fe.starts_at DESC NULLS LAST, fe.id LIMIT $3))
	 ORDER BY e.starts_at DESC NULLS LAST, e.id`

// alertRuleForSQL reads the `for:` (event_rules.duration) of the enabled rule the event of $2 names (its
// nb_alert_name or alertname label, else its aggregation key) on account $1; no row when there is none.
const alertRuleForSQL = `SELECT COALESCE(er.duration, '') FROM events e JOIN event_rules er ON er.account_id = e.cloud_account_id
	 WHERE e.id = $2::uuid AND e.cloud_account_id = $1::uuid AND er.enabled
	   AND er.alert = COALESCE(NULLIF(e.labels->>'nb_alert_name', ''), NULLIF(e.labels->>'alertname', ''), e.aggregation_key)
	 ORDER BY (right(er.source, 8) = '_webhook'), er.updated_at DESC LIMIT 1`

// ruleForSeconds reads a rule's `for:` ("15m", "1h30m", "" for none): seconds, and whether it was understood.
func ruleForSeconds(duration string) (int, bool) {
	duration = strings.TrimSpace(duration)
	if duration == "" {
		return 0, true
	}
	d, err := time.ParseDuration(duration)
	if err != nil || d < 0 {
		return 0, false
	}
	return int(d.Seconds()), true
}

// FollowupData is the data of run runId as it is now, for a turn started after code wrote to it this claim.
func FollowupData(ctx context.Context, db *sqlx.DB, runId string) ([]byte, error) {
	var data []byte
	if err := db.GetContext(ctx, &data, `SELECT COALESCE(data, '{}'::jsonb) FROM event_resolution WHERE id = $1`, runId); err != nil {
		return nil, fmt.Errorf("FollowupData: %w", err)
	}
	return data, nil
}

// SetFollowupVerdict merges patch (a JSON object: verdict, verdict_at, verdict_reason, verdict_checked_at)
// into an open follow-up; it returns the rows changed.
func SetFollowupVerdict(ctx context.Context, db *sqlx.DB, runId, patch string) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		   SET data = jsonb_set(data, '{followup}', data->'followup' || $2::jsonb)
		 WHERE id = $1 AND data->'followup' IS NOT NULL AND data->'followup'->>'done_at' IS NULL`, runId, patch))
	if err != nil {
		return 0, fmt.Errorf("SetFollowupVerdict: %w", err)
	}
	return n, nil
}

// ConversationTurnLive reports whether the conversation of session on accountId has a turn under way (running,
// queued or waiting on a person) that started within the last within: a person is using it.
func ConversationTurnLive(ctx context.Context, db *sqlx.DB, accountId, session string, within time.Duration) (bool, error) {
	var live bool
	if err := db.GetContext(ctx, &live, `SELECT EXISTS (SELECT 1 FROM llm_conversation_messages m
		  JOIN llm_conversations c ON c.id = m.conversation_id
		 WHERE c.account_id = $1::uuid AND c.session_id = $2
		   AND m.status IN ('IN_PROGRESS', 'QUEUED', 'WAITING', 'WAITING_FOR_CLIENT_TOOL', 'PENDING')
		   AND m.created_at > LOCALTIMESTAMP - make_interval(secs => $3))`,
		accountId, session, within.Seconds()); err != nil {
		return false, fmt.Errorf("ConversationTurnLive: %w", err)
	}
	return live, nil
}

// PrimaryEventOf is the main event whose plan a duplicate reads and acts on (PlanEventOf: a live main event,
// unless it has no plan and the duplicate has one), or "" when eventId reads its own.
func PrimaryEventOf(ctx context.Context, db *sqlx.DB, accountId, eventId string) (string, error) {
	planEvent, err := PlanEventOf(ctx, db, accountId, eventId)
	if err != nil {
		return "", fmt.Errorf("PrimaryEventOf: %w", err)
	}
	if planEvent == eventId {
		return "", nil
	}
	return planEvent, nil
}

// NewestPlanOfEvent is the text of the newest plan stored for eventId on accountId, else for its main event when
// it is a duplicate; "" when neither has one.
func NewestPlanOfEvent(ctx context.Context, db *sqlx.DB, accountId, eventId string) (string, error) {
	primary, err := PrimaryEventOf(ctx, db, accountId, eventId)
	if err != nil {
		return "", fmt.Errorf("NewestPlanOfEvent: %w", err)
	}
	var plans []string
	if err := db.SelectContext(ctx, &plans, `SELECT a.analysis FROM event_log_analysis a
		 WHERE a.cloud_account_id = $1::uuid AND a.event_id IN ($2::uuid, NULLIF($3, '')::uuid) AND a.analysis_type = $4
		   AND COALESCE(a.analysis, '') <> ''
		 ORDER BY (a.event_id = $2::uuid) DESC, COALESCE(a.updated_at, a.recorded_at) DESC LIMIT 1`,
		accountId, eventId, primary, string(AnalysisTypeRemediation)); err != nil {
		return "", fmt.Errorf("NewestPlanOfEvent: %w", err)
	}
	if len(plans) == 0 {
		return "", nil
	}
	return plans[0], nil
}

// refireField is the value of one string field of e's nb_refire label (a JSON object written as a label string),
// read with a pattern rather than a jsonb cast: a malformed label then matches nothing instead of failing the
// whole statement. pattern is the value's own shape.
func refireField(field, pattern string) string {
	return `substring(COALESCE(e.labels->>'nb_refire', '') from '"` + field + `"\s*:\s*"(` + pattern + `)"')`
}

const (
	uuidPattern    = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`
	versionPattern = `[0-9a-f]{64}`
)

// RefireToCompare is the first firing of a new chain that triage labelled a re-fire ("unknown") of an alert the
// loop resolved, with the plan now stored for it and the resolve label of the run the re-fire names.
type RefireToCompare struct {
	EventId   string `db:"event_id"`
	AccountId string `db:"account_id"`
	// PreviousRunId is the run whose verdict resolved the old chain.
	PreviousRunId string `db:"previous_run_id"`
	// Plan is the newest plan stored for this event, and PlanVersion its version (PlanVersion of its text).
	Plan        string `db:"plan"`
	PlanVersion string `db:"plan_version"`
	// ResolvedBy is the old chain's nb_resolved_by label (a JSON string), or "".
	ResolvedBy string `db:"resolved_by"`
}

// RefiresToCompare are at most limit events created in the last 24 h, with the loop on, newest first, whose
// nb_refire label is still "unknown" and whose plan is not the one last compared (the label's compared_plan): the
// re-fires whose cause can be compared with the old chain's. Once compared a re-fire is not read again until its
// plan changes, so re-fires that stay "unknown" do not crowd out newer ones.
func RefiresToCompare(ctx context.Context, db *sqlx.DB, limit int) ([]RefireToCompare, error) {
	var out []RefireToCompare
	err := db.SelectContext(ctx, &out, `SELECT e.id::text AS event_id, e.cloud_account_id::text AS account_id,
		       COALESCE(`+refireField("previous_run_id", uuidPattern)+`, '') AS previous_run_id,
		       a.analysis AS plan, a.version AS plan_version,
		       COALESCE((SELECT p.labels->>'nb_resolved_by' FROM event_resolution er JOIN events p ON p.id = er.event_id
		          WHERE er.id = `+refireField("previous_run_id", uuidPattern)+`::uuid AND p.cloud_account_id = e.cloud_account_id
		            AND jsonb_typeof(p.labels) = 'object' AND p.labels ? 'nb_resolved_by'), '') AS resolved_by
		  FROM feature_flag ff
		  JOIN events e ON e.tenant = ff.tenant_id AND e.created_at > LOCALTIMESTAMP - interval '24 hours'
		  JOIN LATERAL (SELECT x.analysis, encode(sha256(convert_to(x.analysis, 'UTF8')), 'hex') AS version
		        FROM event_log_analysis x
		        WHERE x.event_id = e.id AND x.cloud_account_id = e.cloud_account_id AND x.analysis_type = $2
		          AND COALESCE(x.analysis, '') <> ''
		        ORDER BY COALESCE(x.updated_at, x.recorded_at) DESC LIMIT 1) a ON true
		 WHERE ff.feature_id = '`+RemediationLoopFeature+`' AND ff.account_id IS NULL AND ff.status = 'enabled'
		   AND jsonb_typeof(e.labels) = 'object' AND e.labels ? 'nb_refire'
		   AND `+refireField("kind", "[a-z_]+")+` = 'unknown'
		   AND `+refireField("compared_plan", versionPattern)+` IS DISTINCT FROM a.version
		 ORDER BY e.created_at DESC LIMIT $1`, limit, string(AnalysisTypeRemediation))
	if err != nil {
		return nil, fmt.Errorf("RefiresToCompare: %w", err)
	}
	return out, nil
}
