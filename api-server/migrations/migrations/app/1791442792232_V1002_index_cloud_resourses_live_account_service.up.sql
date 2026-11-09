-- When a cloud service sync no longer sees a resource, the cloud collector
-- marks the service's remaining live rows in that account inactive:
--   UPDATE cloud_resourses SET is_active = false, status = 'Deleted'
--   WHERE account = $1 AND lower(service_name) = $2 AND (is_active IS NOT FALSE) ...
-- No index matches lower(service_name), and most of an account's rows are
-- already inactive, so every run read the account's whole history to find the
-- handful still live (measured: 172,810 rows fetched to find 43, 46 s cold).
--
-- This index holds live rows only and is keyed the way that UPDATE looks them
-- up. "Live" is is_active IS NOT FALSE, NULL included: the convention of
-- idx_cloud_resourses_active_notfalse_account_type (V752). Postgres uses a
-- partial index only when the query implies its predicate, so this predicate
-- and the collector's notYetArchived guard
-- (collector-server/cloud-collector/account/etl_resources.go) must stay the
-- same expression; archive_guard_test.go there checks it. Part of #38283.
--
-- Pre-build on every live database BEFORE this migration reaches it, then
-- confirm the build left a valid index (a failed concurrent build leaves an
-- INVALID one, and IF NOT EXISTS below would then silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cloud_resourses_live_account_service
--     ON public.cloud_resourses (account, lower(service_name)) WHERE is_active IS NOT FALSE;
--   SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_cloud_resourses_live_account_service'::regclass;
-- Where it was pre-built this is a no-op. Elsewhere it is a plain build that
-- blocks writes to cloud_resourses for one pass over the table; the index
-- itself holds only the live rows.
CREATE INDEX IF NOT EXISTS idx_cloud_resourses_live_account_service
    ON public.cloud_resourses (account, lower(service_name))
    WHERE is_active IS NOT FALSE;
