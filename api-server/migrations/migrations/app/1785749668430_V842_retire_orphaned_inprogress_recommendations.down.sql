-- Not reversible: the pre-migration status of each row (InProgress) is not recoverable
-- per-row, and restoring it would recreate the stuck state this migration exists to clear.
-- Rolling the code back is enough — the api-server cron simply stops reopening orphans.
SELECT 1;
