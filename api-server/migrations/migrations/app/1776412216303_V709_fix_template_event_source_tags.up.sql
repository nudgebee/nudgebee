-- Fix event_sources tags in workflow templates to match actual event source values in the events table.
-- The original seed used generic names (e.g. "cloudwatch", "pagerduty") but the actual source column
-- values are "AWS_CloudWatch_Alarm", "pagerduty_webhook", etc.
--
-- NOTE: Generic forwarders (pagerduty_webhook, opsgenie_webhook) are NOT added to specific templates.
-- Alerts forwarded through PagerDuty/OpsGenie should match via alert_names, not event_sources.
-- Only the universal "Create Ticket from Event" template includes forwarder sources.

-- K8s templates: fix to actual source values
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["prometheus", "kubernetes_api_server", "alertmanager"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["prometheus"]'::jsonb
  AND tags->'event_sources' @> '["kubernetes"]'::jsonb
  AND NOT (tags->'event_sources' @> '["cloudwatch"]'::jsonb);

-- AWS templates: fix to actual source values
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["AWS_CloudWatch_Alarm", "AWS_EventBridge"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["cloudwatch"]'::jsonb
  AND tags->'event_sources' @> '["aws"]'::jsonb;

-- Azure templates: fix to actual source values
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["Azure_Monitor_Alert", "azure_monitor_webhook"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["azure_monitor"]'::jsonb
  AND tags->'event_sources' @> '["azure"]'::jsonb;

-- GCP templates: fix to actual source values
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["GCP_Metric_Alert", "gcp_monitoring_webhook"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND tags->'event_sources' @> '["gcp_monitoring"]'::jsonb
  AND tags->'event_sources' @> '["gcp"]'::jsonb;

-- Create Ticket from Event: universal template — match all known event sources including forwarders
UPDATE workflow_templates
SET tags = jsonb_set(
  tags,
  '{event_sources}',
  '["prometheus", "kubernetes_api_server", "alertmanager", "AWS_CloudWatch_Alarm", "AWS_EventBridge", "Azure_Monitor_Alert", "azure_monitor_webhook", "GCP_Metric_Alert", "gcp_monitoring_webhook", "pagerduty_webhook", "opsgenie_webhook", "datadog_webhook", "custom_webhook"]'::jsonb
)
WHERE is_system = true
  AND tenant_id IS NULL
  AND name = 'Create Ticket from Event';
