-- What prompt caching saved on each AI Gateway request, snapshotted at write time like
-- cost_usd: cache reads priced at the gap to the full input rate, minus the premium paid
-- to write the cache. No backfill: rows written before this hold 0.
--
-- A constant default is a metadata-only change in Postgres 11+ (no table rewrite), so
-- this takes only a brief lock.
ALTER TABLE llm_gateway_usage
    ADD COLUMN IF NOT EXISTS cache_savings_usd double precision NOT NULL DEFAULT 0;
