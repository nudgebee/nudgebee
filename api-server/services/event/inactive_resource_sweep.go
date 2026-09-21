package event

import (
	"fmt"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// The agent's own findings have no resolve delivery: report_crash_loop,
// pod_oom_killed, job_failure and friends fire and forget, so the only thing
// that ever closes them is the resource they describe going away. Two paths do
// that at the moment the resource disappears -- the discovery reconcile and
// process_deleted_resources in the k8s-collector -- and both share a blind
// spot: they close the events that EXIST when the deletion is processed.
//
// An alert that arrives afterwards is never revisited, because nothing looks at
// an already-inactive resource again. That is not an edge case, it is the
// common case for Jobs: the Job fails, the object is garbage-collected, and the
// alert lands 2-6 seconds later. Measured on production, 106 of 108 alerts that
// leaked in six hours were Jobs, arriving a median 3 seconds after the resource
// was last seen -- 300-450 a day, which is why a one-off backfill (V921) closed
// 2,054 rows and had 193 back within a day.
//
// This sweep is the counterpart to that backfill: the same predicate, run on a
// schedule, so the class is closed rather than the instance. It deliberately
// does NOT try to detect the race at ingestion -- ordering between the agent's
// findings and discovery's snapshots is not something either side controls, so
// a periodic reconciliation is the honest shape for it.

// inactiveResourceSweepMinAge keeps the sweep away from alerts that are still
// arriving. The collector can deactivate a resource and receive one more alert
// for it moments later; closing that while its successor is still in flight
// would race the collector for no benefit. Measured on production, 388 resources
// received a new alert within 24h of going inactive.
const inactiveResourceSweepMinAge = 24 * time.Hour

// inactiveResourceSweepBatchSize bounds one run. The steady state is a few
// hundred rows a day, but an environment that has never run this (or one whose
// discovery has just reconciled a large cluster teardown) can present thousands
// at once, and `events` is a 200GB+ table whose evidences column is TOASTed.
// Closing in bounded batches keeps any single transaction short; the next run an
// hour later picks up the remainder.
const inactiveResourceSweepBatchSize = 2000

// closeEventsForInactiveResourcesSQL closes, records history for, and reports
// the count of agent alerts whose Kubernetes resource no longer exists.
//
// One statement so the close and its audit row commit together: a closed event
// with no history entry is indistinguishable from one a human closed.
//
// ends_at is the resource's last-seen time, not now(). These alerts ended when
// their resource disappeared, in some cases weeks ago; stamping now() would tell
// every incident-duration report that they ran until the sweep happened to fire.
// GREATEST guards a last_seen that predates the alert, LEAST guards clock skew.
//
// Deliberately does not touch nb_status, and deliberately publishes no resolved
// notification: this is a reconciliation of state we already failed to record,
// not news. The equivalent closers in the k8s-collector behave the same way.
const closeEventsForInactiveResourcesSQL = `
WITH target AS (
    SELECT e.id,
           e.status        AS old_status,
           e.tenant,
           e.cloud_account_id,
           e.ends_at       AS prev_ends_at,
           LEAST(
               now() AT TIME ZONE 'utc',
               GREATEST(cr.last_seen, e.starts_at)
           )               AS resolved_at
      FROM events e
      JOIN cloud_resourses cr ON cr.id = e.cloud_resource_id
     WHERE cr.is_active = false
       AND e.source = 'kubernetes_api_server'
       AND e.finding_type = 'issue'
       AND e.status <> 'CLOSED'
       -- make_interval(secs => $1) rather than casting a Go duration string:
       -- time.Duration.String() renders "24h0m0s", which Postgres' interval
       -- parser happens to accept, but that is an implicit contract between
       -- Go's formatter and the parser. Passing the number keeps it explicit.
       AND e.created_at < (now() AT TIME ZONE 'utc') - make_interval(secs => $1)
       -- event_history.tenant_id / cloud_account_id are NOT NULL; a row missing
       -- either cannot get a history entry, and would fail the whole statement.
       AND e.tenant IS NOT NULL
       AND e.cloud_account_id IS NOT NULL
     LIMIT $2
),
closed AS (
    UPDATE events e
       SET status     = 'CLOSED',
           ends_at    = COALESCE(e.ends_at, t.resolved_at),
           updated_at = now() AT TIME ZONE 'utc'
      FROM target t
     WHERE e.id = t.id
    RETURNING e.id, t.old_status, t.tenant, t.cloud_account_id, t.prev_ends_at
)
INSERT INTO event_history (
    id, event_id, tenant_id, cloud_account_id,
    change_type, old_value, new_value, change_reason, metadata
)
SELECT
    -- Deterministic, so a retry of the same close writes no second row. md5 is
    -- 32 hex characters, which is a valid uuid literal, and needs no extension.
    md5(c.id::text || '|status|' || c.old_status || '|CLOSED')::uuid,
    c.id,
    c.tenant,
    c.cloud_account_id,
    'status',
    to_jsonb(c.old_status),
    '"CLOSED"'::jsonb,
    'resource_inactive_sweep',
    jsonb_build_object(
        'method',       'cron',
        'trigger',      'resource_inactive_or_deleted',
        'issue',        '38238',
        'prev_ends_at', c.prev_ends_at
    )
  FROM closed c
    ON CONFLICT (id) DO NOTHING
`

// CloseEventsForInactiveResources closes agent-raised issue alerts whose
// Kubernetes resource no longer exists and which are past the minimum age.
// Returns the number of alerts closed. Safe to run repeatedly: an alert it has
// already closed no longer matches.
func CloseEventsForInactiveResources(ctx *security.RequestContext) (int64, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, fmt.Errorf("event: inactive-resource sweep could not get db manager: %w", err)
	}

	res, err := dbms.Db.ExecContext(
		ctx.GetContext(),
		closeEventsForInactiveResourcesSQL,
		int64(inactiveResourceSweepMinAge.Seconds()),
		inactiveResourceSweepBatchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("event: inactive-resource sweep failed: %w", err)
	}

	// RowsAffected reports the history rows inserted, which is the count of
	// alerts actually transitioned (ON CONFLICT DO NOTHING only suppresses a
	// duplicate of a close we already recorded).
	closed, err := res.RowsAffected()
	if err != nil {
		// The work committed; only the count is unavailable.
		ctx.GetLogger().Warn("event: inactive-resource sweep ran but row count unavailable", "error", err)
		return 0, nil
	}
	return closed, nil
}
