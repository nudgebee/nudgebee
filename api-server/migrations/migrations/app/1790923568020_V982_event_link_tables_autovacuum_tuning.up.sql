-- event_duplicates and event_correlations are probed once per listed event by the events
-- list and the grouped events view, mostly through index-only scans. An index-only scan
-- can skip the table only for pages marked all-visible, and only VACUUM marks them.
--
-- Both tables grow by steady inserts and are trimmed by a daily cleanup. With the default
-- thresholds (20% of the table inserted, or 20% dead) autovacuum reaches them days apart,
-- so in between most of those probes fall back to fetching the row from the table and the
-- rows the cleanup removed stay behind as dead space.
--
-- Lowering the per-table thresholds to 2% lets Postgres's own autovacuum keep both tables
-- clean on every install. No manual VACUUM is needed; the first run starts on its own once
-- this applies. ALTER TABLE ... SET takes a SHARE UPDATE EXCLUSIVE lock: reads and writes
-- continue, and it completes immediately.
ALTER TABLE public.event_duplicates SET (autovacuum_vacuum_scale_factor = 0.02);
ALTER TABLE public.event_correlations SET (autovacuum_vacuum_scale_factor = 0.02);

-- Insert-driven vacuum (which also marks newly inserted pages all-visible) exists only on
-- PostgreSQL 13+. The bundled chart ships 16, but external databases may be older.
DO $$
BEGIN
  IF current_setting('server_version_num')::int >= 130000 THEN
    EXECUTE 'ALTER TABLE public.event_duplicates SET (autovacuum_vacuum_insert_scale_factor = 0.02)';
    EXECUTE 'ALTER TABLE public.event_correlations SET (autovacuum_vacuum_insert_scale_factor = 0.02)';
  END IF;
END $$;
