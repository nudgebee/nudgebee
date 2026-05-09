ALTER TABLE event_threshold_suggestions ADD COLUMN IF NOT EXISTS event_aggregation_key TEXT;

-- Backfill existing 'ok' rows so the Recent Events tab works for previously-computed
-- suggestions before the next batch run. Picks the most recent matching event's
-- aggregation_key per (alert_rule_key, cloud_account_id), using the same per-source
-- identity expression as triage/threshold_suggestion_batch.go::processNoisyAlert.
UPDATE event_threshold_suggestions s
SET event_aggregation_key = sub.aggregation_key
FROM (
    SELECT DISTINCT ON (s2.tenant_id, s2.alert_rule_key, s2.cloud_account_id)
        s2.tenant_id,
        s2.alert_rule_key,
        s2.cloud_account_id,
        e.aggregation_key
    FROM event_threshold_suggestions s2
    JOIN events e
      ON e.tenant = s2.tenant_id
     AND e.cloud_account_id = s2.cloud_account_id
     AND e.source = s2.source
     AND CASE s2.source
            WHEN 'prometheus' THEN e.labels->>'alertname'
            WHEN 'pagerduty_webhook' THEN COALESCE(e.labels->>'nb_alert_name', e.labels->>'alertname')
            WHEN 'AWS_CloudWatch_Alarm' THEN e.labels->>'aws_event_arn'
            WHEN 'azure_monitor_webhook' THEN COALESCE(e.labels->>'azure_alert_name', e.labels->>'alertname')
            WHEN 'Azure_Monitor_Alert' THEN COALESCE(e.labels->>'azure_alert_name', e.labels->>'alertname')
            WHEN 'GCP_Metric_Alert' THEN e.labels->>'gcp_policy_id'
         END = s2.alert_rule_key
    WHERE s2.status = 'ok'
      AND s2.event_aggregation_key IS NULL
      AND e.starts_at > NOW() - INTERVAL '30 days'
    ORDER BY s2.tenant_id, s2.alert_rule_key, s2.cloud_account_id, e.starts_at DESC
) sub
WHERE s.tenant_id = sub.tenant_id
  AND s.alert_rule_key = sub.alert_rule_key
  AND s.cloud_account_id = sub.cloud_account_id
  AND s.event_aggregation_key IS NULL;
