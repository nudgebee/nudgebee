ALTER TABLE event_threshold_suggestions ALTER COLUMN reason SET NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN suggested_threshold SET NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN current_threshold SET NOT NULL;
ALTER TABLE event_threshold_suggestions ALTER COLUMN metric_name SET NOT NULL;
ALTER TABLE event_threshold_suggestions DROP COLUMN IF EXISTS status;
