-- Fix: Missing index on event_duplicates.previous_event_id
--
-- This FK references events(id) with ON DELETE CASCADE but has no index.
-- Every event deletion triggers a sequential scan of event_duplicates
-- (462K rows, 317MB) to find matching previous_event_id values.
-- pg_stat shows 3.97M seq scans reading 2.97 trillion tuples on this table.
CREATE INDEX IF NOT EXISTS idx_event_dup_previous_event_id
  ON event_duplicates (previous_event_id);
