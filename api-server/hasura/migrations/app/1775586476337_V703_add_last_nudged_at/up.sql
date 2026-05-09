ALTER TABLE recommendation
  ADD COLUMN IF NOT EXISTS last_nudged_at TIMESTAMPTZ;

-- Backfill existing open recommendations so they are not treated as
-- never-nudged and flood notifications on first cron run.
UPDATE recommendation
  SET last_nudged_at = now()
  WHERE status = 'Open'
    AND last_nudged_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_recommendation_last_nudged_at
  ON recommendation (last_nudged_at) WHERE status = 'Open';
