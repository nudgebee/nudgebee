-- Close the agent alerts that were left open on Kubernetes resources that no
-- longer exist (issue #38238).
--
-- When discovery reported a resource as deleted, the collector deactivated it
-- and then looked for its alerts by `service_key` -- a key discovery writes as
-- `<ns>/<Kind>/<name>` while the agent's own findings write `<ns>/<name>`. The
-- two never matched, so nothing was closed. The code fix (PR #38244) matches on
-- cloud_resource_id instead, but it only takes effect at the moment a deletion
-- is reported: nothing ever revisits a resource that is already inactive, so the
-- alerts that leaked before that fix stay open forever. This closes them once.
--
-- Measured on production before this ran: 2,401 open alerts against 1,613
-- resources that no longer exist, across 16 accounts -- 1,393 of them from Jobs,
-- whose objects disappear by design once they finish.
--
-- SCOPE, and why each clause is here:
--
--   source = 'kubernetes_api_server' + finding_type = 'issue'
--       Only the agent's own findings. Every other source (prometheus, datadog,
--       pagerduty, cloud alarms) delivers its own resolve and owns its lifecycle;
--       closing those here would overrule the producer. `issue` excludes
--       configuration_change, which describes a moment that has already passed.
--
--   created_at < now() - interval '24 hours'
--       Leaves anything recent alone. 388 of these resources received a new
--       alert in the last 24h (a Job that failed moments before discovery
--       noticed it was gone, or an hourly re-fire landing after deactivation).
--       Closing a row while its successor is still arriving would race the
--       collector for no benefit.
--
--   status <> 'CLOSED'
--       Idempotent: re-running closes nothing a second time.
--
-- Deliberately does NOT touch nb_status. The code path this backfills does not
-- set it either, and the point of a backfill is to leave the rows in exactly the
-- state the fixed code would have produced -- not a state no code path creates.
--
-- ends_at is set to when the resource was LAST SEEN, not to now(). These alerts
-- ended when their resource disappeared, days or weeks ago. Stamping now() would
-- tell every incident-duration report that 2,401 incidents ran until the day this
-- migration was deployed. GREATEST guards a last_seen that predates the alert;
-- LEAST guards a clock skew putting it in the future.
--
-- LOCK WINDOW: one UPDATE of ~2,400 rows plus the matching event_history inserts,
-- in a single transaction. The read is driven from the events side through
-- idx_events_tenant_account_source (~135k candidate rows on production) and
-- memoized primary-key lookups into cloud_resourses, so it does not scan the
-- 15.5M inactive resource rows. Expected to run in seconds; row locks are taken
-- only on the events actually closed. The `evidences` column is never selected,
-- so none of the TOASTed alert payloads are read.
--
-- Timestamps use `now() AT TIME ZONE 'utc'` for the naive `timestamp` columns:
-- a bare now() is timestamptz and would be converted through the session's
-- TimeZone on the way in, silently writing local time.

WITH target AS (
    SELECT e.id,
           e.status                                   AS old_status,
           e.tenant,
           e.cloud_account_id,
           e.ends_at                                  AS prev_ends_at,
           LEAST(
               now() AT TIME ZONE 'utc',
               GREATEST(cr.last_seen, e.starts_at)
           )                                          AS resolved_at
      FROM events e
      JOIN cloud_resourses cr ON cr.id = e.cloud_resource_id
     WHERE cr.is_active = false
       AND e.source = 'kubernetes_api_server'
       AND e.finding_type = 'issue'
       AND e.status <> 'CLOSED'
       AND e.created_at < (now() AT TIME ZONE 'utc') - interval '24 hours'
       -- event_history.tenant_id / cloud_account_id are NOT NULL; a row missing
       -- either cannot get a history entry, and one such row would abort the
       -- whole migration.
       AND e.tenant IS NOT NULL
       AND e.cloud_account_id IS NOT NULL
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
    -- Deterministic id, so a re-run inserts nothing new. md5 (32 hex chars) is a
    -- valid uuid literal and needs no extension; the application's own history
    -- ids use sha256[:32] of the same shape, so the two cannot collide -- which
    -- is fine, because a row closed here is never closed again by the app.
    md5(c.id::text || '|status|' || c.old_status || '|CLOSED')::uuid,
    c.id,
    c.tenant,
    c.cloud_account_id,
    'status',
    to_jsonb(c.old_status),
    '"CLOSED"'::jsonb,
    'resource_inactive_backfill',
    jsonb_build_object(
        'method',        'migration',
        'migration',     'V921',
        'trigger',       'resource_inactive_or_deleted',
        'issue',         '38238',
        -- Recorded so the down migration can restore the exact previous value.
        'prev_ends_at',  c.prev_ends_at
    )
  FROM closed c
    ON CONFLICT (id) DO NOTHING;
