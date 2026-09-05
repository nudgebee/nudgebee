-- RESET restores the pre-V909 state (recommendation had no reloptions before
-- this). The out-of-band VACUUM/ANALYZE from the up migration isn't reverted.

SET LOCAL lock_timeout = '5s';

DROP INDEX IF EXISTS idx_rec_sec_open_counts;

ALTER TABLE recommendation RESET (
  autovacuum_vacuum_insert_scale_factor,
  autovacuum_vacuum_insert_threshold,
  autovacuum_vacuum_scale_factor,
  autovacuum_analyze_scale_factor
);
