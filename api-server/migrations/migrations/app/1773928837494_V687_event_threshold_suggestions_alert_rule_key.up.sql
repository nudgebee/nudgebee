-- Add alert_rule_key column for stable alert identity grouping
ALTER TABLE event_threshold_suggestions ADD COLUMN IF NOT EXISTS alert_rule_key TEXT;

-- Backfill from existing data
UPDATE event_threshold_suggestions SET alert_rule_key = alert_name WHERE alert_name IS NOT NULL AND alert_name != '' AND alert_rule_key IS NULL;
UPDATE event_threshold_suggestions SET alert_rule_key = fingerprint WHERE alert_rule_key IS NULL;

-- De-duplicate before applying new unique constraint.
-- Keep only the most recently computed suggestion per (alert_rule_key, cloud_account_id).
DELETE FROM event_threshold_suggestions
WHERE ctid IN (
  SELECT ctid FROM (
    SELECT ctid, row_number() OVER (PARTITION BY alert_rule_key, cloud_account_id ORDER BY computed_at DESC, id ASC) as rn
    FROM event_threshold_suggestions
    WHERE alert_rule_key IS NOT NULL
  ) t
  WHERE rn > 1
);

-- Enforce NOT NULL now that all rows have been backfilled
ALTER TABLE event_threshold_suggestions ALTER COLUMN alert_rule_key SET NOT NULL;

-- Drop old unique constraint and add new one keyed on alert_rule_key
ALTER TABLE event_threshold_suggestions DROP CONSTRAINT IF EXISTS event_threshold_suggestions_fingerprint_cloud_account_id_key;
ALTER TABLE event_threshold_suggestions ADD CONSTRAINT event_threshold_suggestions_alert_rule_key_cloud_account_id_key UNIQUE(alert_rule_key, cloud_account_id);

-- Index for cache lookups
CREATE INDEX IF NOT EXISTS idx_event_threshold_suggestions_alert_rule_key ON event_threshold_suggestions(alert_rule_key, cloud_account_id, status);
