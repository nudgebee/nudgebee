-- prometheus_user: alert rules an account writes to its OWN Prometheus-compatible
-- ruler (Mimir / Cortex / Grafana Cloud) over a direct connection — the
-- prometheus:user integration from #37536 — as opposed to `prometheus`, which is
-- the in-cluster agent's PrometheusRule path. Same split as chronosphere /
-- chronosphere_user (V700). Lookup-table insert only; no lock of note.
INSERT INTO "public"."event_rule_source"("value") VALUES ('prometheus_user')
ON CONFLICT DO NOTHING;
