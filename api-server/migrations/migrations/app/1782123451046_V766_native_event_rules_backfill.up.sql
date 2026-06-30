-- Make every event type (aggregation_key) selectable as a workflow event-trigger
-- "Event Type" by registering it in event_rules. event_rules previously only held
-- prometheus/webhook/user-created alerts, so Nudgebee-native types (Anomaly, the
-- Kubernetes-API-failure enrichers pod_oom_killer_enricher /
-- image_pull_backoff_reporter / job_failure, …) — which are emitted directly as
-- events — never appeared. This backfills them carrying their real event source so
-- the Event Rules table stays filterable by source; new types are registered going
-- forward by the api-server post-process forward-fill (registerNativeEventTypeRule).

-- 1. Register every event source seen recently as a valid event_rule_source so the
--    backfilled rows can carry their real source (e.g. 'kubernetes_api_server').
INSERT INTO "public"."event_rule_source" ("value")
SELECT DISTINCT source
FROM "public"."events"
WHERE source IS NOT NULL AND source <> ''
  AND created_at >= now() - interval '90 days'
ON CONFLICT ("value") DO NOTHING;

-- 2. Backfill one rule per (account, tenant, aggregation_key) seen on events in the
--    last 90 days, carrying the event's real source + category. The bounded window
--    keeps the migration's events scan cheap; older/rare types re-register on their
--    next occurrence via the forward-fill. Column values mirror the runtime path
--    (eventrule.CreateEventRule via registerNativeEventTypeRule): empty expr,
--    severity=warning, alert_type=log, default is_editable. ON CONFLICT DO NOTHING
--    preserves any existing real prometheus/webhook rule for the alert.
INSERT INTO "public"."event_rules"
  (id, account_id, tenant_id, alert, annotations, expr, labels, source, category, severity, enabled, alert_type)
SELECT gen_random_uuid(), e.cloud_account_id, e.tenant, e.aggregation_key,
       '{}'::jsonb, '', '{"severity": "warning"}'::jsonb, e.source, e.category, 'warning', true, 'log'
FROM (
  SELECT DISTINCT ON (cloud_account_id, tenant, aggregation_key)
         cloud_account_id, tenant, aggregation_key,
         COALESCE(NULLIF(source, ''), 'nudgebee') AS source,
         COALESCE(NULLIF(category, ''), COALESCE(NULLIF(source, ''), 'nudgebee')) AS category
  FROM "public"."events"
  WHERE aggregation_key IS NOT NULL AND aggregation_key <> ''
    AND cloud_account_id IS NOT NULL AND tenant IS NOT NULL
    AND created_at >= now() - interval '90 days'
  ORDER BY cloud_account_id, tenant, aggregation_key, created_at DESC
) e
ON CONFLICT ("account_id", "tenant_id", "alert") DO NOTHING;
