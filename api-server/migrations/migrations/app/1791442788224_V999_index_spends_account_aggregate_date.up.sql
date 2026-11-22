-- An account's spend total (the cost screens and the per-account spend and
-- currency summary) filters spends on tenant, cloud_account, exclude_aggregate
-- and a date range, and reads amount and unit. No index carries all of those:
-- spends_account_date_exclude has no tenant, so the planner walks the unique
-- (tenant, cloud_account, cloud_resource_id, date) index instead and fetches
-- every row of the account in the period just to discard the per-resource
-- ones. Measured on a 1.9M-row table: 247k rows fetched to add up 10k, 6.5 s
-- cold. With the filter columns leading and amount and unit included, the same
-- query is an index-only range scan of the rows it adds up. Part of #39739.
--
-- Pre-build on every live database BEFORE this migration reaches it, then
-- confirm the build left a valid index (a failed concurrent build leaves an
-- INVALID one, and IF NOT EXISTS below would then silently keep it):
--   SET lock_timeout = '120s'; SET statement_timeout = 0;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_spends_tenant_account_aggregate_date
--     ON public.spends (tenant, cloud_account, exclude_aggregate, date) INCLUDE (amount, unit);
--   SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_spends_tenant_account_aggregate_date'::regclass;
-- Where it was pre-built this is a no-op. Elsewhere it is a plain build that
-- blocks writes to spends for one pass over the table.
CREATE INDEX IF NOT EXISTS idx_spends_tenant_account_aggregate_date
    ON public.spends (tenant, cloud_account, exclude_aggregate, date)
    INCLUDE (amount, unit);
