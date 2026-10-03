-- security_tab_counts (recommendation_groupings_v2, group_by rule_name) on
-- /optimise Security intermittently takes 12s. Root cause, measured on dev
-- (tenant 890cad87..., 304528 rows): the index-only scan on
-- idx_recommendation_tenant_account_status shows Heap Fetches: 100688 because
-- `recommendation` has never been vacuumed (last_vacuum: NULL) and the 0.2
-- global autovacuum scale factor lets its visibility map go stale under the
-- scan orchestrator's insert bursts. Cold, those heap fetches are ~100k random
-- reads (effective_io_concurrency=1, no prefetch) = the 12s; warm = ~150ms,
-- hence the intermittency.
--
-- Fix: lower the insert-vacuum trigger so a burst gets its visibility map set
-- within ~45k rows instead of ~183k (one-off VACUUM on dev: Heap Fetches
-- 100688 -> 0). Dead-tuple trigger only halved, not matched, to avoid
-- over-vacuuming the TOAST (same reasoning as V906/events). Plus a partial
-- index on the exact badge predicate: 2.7 MB vs the 723 MB general index, so
-- a cold load stays sub-second (633 buffers vs 63906).
--
-- OPERATOR NOTE -- run once per environment, out of band, around merge
-- (migrations-lint.yaml rejects CONCURRENTLY in migration SQL):
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rec_sec_open_counts
--     ON recommendation (tenant_id, cloud_account_id, rule_name)
--     WHERE category = 'Security' AND status IN ('Open','InProgress');
--   VACUUM (ANALYZE) recommendation;
-- dev already has both applied. On-prem self-heals via the reloptions below
-- even without the VACUUM.

SET LOCAL lock_timeout = '5s';

ALTER TABLE recommendation SET (
  autovacuum_vacuum_insert_scale_factor = 0.05,
  autovacuum_vacuum_insert_threshold    = 1000,
  autovacuum_vacuum_scale_factor        = 0.1,
  autovacuum_analyze_scale_factor       = 0.05
);

CREATE INDEX IF NOT EXISTS idx_rec_sec_open_counts
  ON recommendation (tenant_id, cloud_account_id, rule_name)
  WHERE category = 'Security' AND status IN ('Open', 'InProgress');

ANALYZE recommendation;
