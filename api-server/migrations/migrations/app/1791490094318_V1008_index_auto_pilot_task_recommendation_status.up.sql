-- Before an optimizer run schedules work it asks which recommendations already
-- have a task in flight (GetActiveTasksForRecommendations in runbook-server,
-- internal/storage/optimizer_dao.go):
--   WHERE recommendation_id = ANY($1) AND status = $2 AND created_at > $3
-- auto_pilot_task has only its primary key, so every call read the whole table
-- (measured: 57k buffers to return no rows). status is in the key because a
-- recommendation accumulates many finished tasks and the query wants only the
-- few still scheduled. Rows without a recommendation are left out; they are
-- most of the table and this lookup never matches them. Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then check
-- that the index is valid and has this definition (a failed concurrent build
-- leaves an INVALID index, and IF NOT EXISTS below would silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_auto_pilot_task_recommendation_status
--     ON public.auto_pilot_task (recommendation_id, status)
--     WHERE recommendation_id IS NOT NULL;
--   SELECT indisvalid, pg_get_indexdef(indexrelid) FROM pg_index
--    WHERE indexrelid = 'idx_auto_pilot_task_recommendation_status'::regclass;
-- Where it was pre-built this only takes a brief lock on auto_pilot_task. Elsewhere it
-- is a plain build that blocks writes to auto_pilot_task for one pass over the table
-- (about a second at 410k rows). If the lock cannot be taken within 5 seconds
-- the migration fails without changing anything; run the upgrade again.

SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_auto_pilot_task_recommendation_status
    ON public.auto_pilot_task (recommendation_id, status)
    WHERE recommendation_id IS NOT NULL;
