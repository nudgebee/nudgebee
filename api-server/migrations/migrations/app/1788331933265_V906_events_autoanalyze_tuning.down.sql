-- Drop the per-table autoanalyze overrides; `events` falls back to the global
-- autovacuum_analyze_scale_factor / autovacuum_analyze_threshold. RESET removes
-- the keys entirely (it does not restore a prior per-table value) -- which is
-- exact here, since `events` had no reloptions before V906.
--
-- The ANALYZE run by the up migration is not reverted: a statistics refresh
-- cannot be undone, and would not be desirable to undo.

SET LOCAL lock_timeout = '5s';

ALTER TABLE events RESET (
  autovacuum_analyze_scale_factor,
  autovacuum_analyze_threshold
);
