-- Revert event_sources tags to original generic values

-- K8s templates
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["prometheus", "alertmanager", "kubernetes"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["kubernetes_api_server"]'::jsonb
  AND NOT (tags->'event_sources' @> '["AWS_CloudWatch_Alarm"]'::jsonb)
  AND name != 'Create Ticket from Event';

-- AWS templates
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["cloudwatch", "aws"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["AWS_CloudWatch_Alarm"]'::jsonb
  AND name != 'Create Ticket from Event';

-- Azure templates
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["azure_monitor", "azure"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["Azure_Monitor_Alert"]'::jsonb
  AND name != 'Create Ticket from Event';

-- GCP templates
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["gcp_monitoring", "gcp"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["GCP_Metric_Alert"]'::jsonb
  AND name != 'Create Ticket from Event';

-- Create Ticket from Event
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["prometheus", "alertmanager", "cloudwatch", "azure_monitor", "gcp_monitoring", "datadog", "pagerduty", "opsgenie", "kubernetes", "aws", "azure", "gcp"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND name = 'Create Ticket from Event';
