CREATE INDEX IF NOT EXISTS idx_workflow_templates_event_sources_gin
  ON workflow_templates USING GIN ((tags->'event_sources'));

CREATE INDEX IF NOT EXISTS idx_workflow_templates_alert_names_gin
  ON workflow_templates USING GIN ((tags->'alert_names'));
