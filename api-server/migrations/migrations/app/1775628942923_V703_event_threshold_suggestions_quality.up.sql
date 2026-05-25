-- Add alert quality score, suggestion method, and expression type columns
ALTER TABLE event_threshold_suggestions
  ADD COLUMN IF NOT EXISTS alert_quality JSONB,
  ADD COLUMN IF NOT EXISTS method TEXT,
  ADD COLUMN IF NOT EXISTS expression_type TEXT;
