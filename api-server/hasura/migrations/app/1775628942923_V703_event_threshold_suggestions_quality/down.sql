ALTER TABLE event_threshold_suggestions
  DROP COLUMN IF EXISTS alert_quality,
  DROP COLUMN IF EXISTS method,
  DROP COLUMN IF EXISTS expression_type;
