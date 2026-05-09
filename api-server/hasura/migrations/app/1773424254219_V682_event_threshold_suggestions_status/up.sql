ALTER TABLE event_threshold_suggestions
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'ok'
    CHECK (status IN ('ok', 'skipped', 'error'));

-- Relax NOT NULL on columns that won't have values for skipped/error entries
ALTER TABLE event_threshold_suggestions ALTER COLUMN metric_name DROP NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN current_threshold DROP NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN suggested_threshold DROP NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN reason DROP NOT NULL;
