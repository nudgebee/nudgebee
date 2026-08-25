-- Trigram indexes for the resource-search lookup:
--   WHERE account = $1 AND cloud_provider = 'K8s' AND is_active = true
--     AND (name ILIKE ANY($2) OR resourse_id ILIKE ANY($2))
--
-- The leading-wildcard ILIKE is unindexable by btree, so the planner previously
-- fetched every live row for the account and filtered in memory. On a 1.6M-row
-- account that meant 18,929 heap blocks for ~937 rows — 123ms when those pages
-- were cached and 10,316ms when they were not, which is where the tool's latency
-- variance came from. With both indexes: 90 heap blocks, 4.2ms.
--
-- BOTH columns need one. Postgres can only use an index for an OR when every
-- branch is indexed (it builds a BitmapOr); one unindexed branch forces the full
-- scan regardless of the other index. Measured on dev: 2876ms with neither,
-- 264ms with `name` only (index ignored), 4.2ms with both.
--
-- The partial predicate is `is_active = true` written exactly as the query
-- writes it — a partial index only applies when the query's predicate matches,
-- and the equivalent-looking `IS NOT FALSE` does not match. Only ~8% of the
-- table is live inventory, so this also keeps GIN write amplification off the
-- 92% that is never searched.
--
-- NOT CONCURRENTLY: the migrations-lint gate rejects CONCURRENTLY outright, and
-- the fresh-DB smoke test runs each migration in a transaction. Per that gate's
-- own instructions, the indexes are pre-applied CONCURRENTLY by hand against
-- each live database *before* this merges, which makes the statements below a
-- no-op there. On an empty or small database the plain form is a quick locked
-- statement.
--
-- ALREADY APPLIED CONCURRENTLY ON: dev (2026-08-13).
-- STILL REQUIRED BEFORE MERGE: test, prod — run
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cloud_resourses_name_trgm
--       ON cloud_resourses USING gin (name gin_trgm_ops) WHERE is_active = true;
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cloud_resourses_resourseid_trgm
--       ON cloud_resourses USING gin (resourse_id gin_trgm_ops) WHERE is_active = true;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_cloud_resourses_name_trgm
    ON cloud_resourses USING gin (name gin_trgm_ops)
    WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_cloud_resourses_resourseid_trgm
    ON cloud_resourses USING gin (resourse_id gin_trgm_ops)
    WHERE is_active = true;
