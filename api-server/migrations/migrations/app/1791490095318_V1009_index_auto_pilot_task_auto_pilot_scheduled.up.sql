-- The task lists and counts on the auto-optimize pages go through the query
-- layer (auto_pilot_task_v2 and auto_pilot_task_groupings_v2 in
-- api-server/services/query/metadata.go): auto_pilot_task joined to auto_pilot,
-- filtered by one auto-pilot or by account and category, usually with
-- scheduled_time >= a date, newest first. auto_pilot_task has only its primary
-- key, so each of these read the whole table (measured: 57k buffers per call).
-- Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then check
-- that the index is valid and has this definition (a failed concurrent build
-- leaves an INVALID index, and IF NOT EXISTS below would silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_auto_pilot_task_auto_pilot_scheduled
--     ON public.auto_pilot_task (auto_pilot_id, scheduled_time DESC);
--   SELECT indisvalid, pg_get_indexdef(indexrelid) FROM pg_index
--    WHERE indexrelid = 'idx_auto_pilot_task_auto_pilot_scheduled'::regclass;
-- Where it was pre-built this only takes a brief lock on auto_pilot_task. Elsewhere it
-- is a plain build that blocks writes to auto_pilot_task for one pass over the table
-- (about a second at 410k rows). If the lock cannot be taken within 5 seconds
-- the migration fails without changing anything; run the upgrade again.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_auto_pilot_task_auto_pilot_scheduled
    ON public.auto_pilot_task (auto_pilot_id, scheduled_time DESC);
