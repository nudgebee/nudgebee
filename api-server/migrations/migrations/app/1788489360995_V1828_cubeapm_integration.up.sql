-- Register the CubeAPM observability platform integration (logs, traces, metrics)
-- and its incident webhook, so both can be created from Admin -> Integrations.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('cubeapm', 'observability_platform', 'CubeAPM logs, traces and metrics')
ON CONFLICT ("name") DO NOTHING;

INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('cubeapm_webhook', 'incident_webhook', 'CubeAPM alert notifications')
ON CONFLICT ("name") DO NOTHING;

-- Register cubeapm_webhook as a valid event source and event rule source so
-- events parsed from the webhook, and the event rules it upserts, both validate.
INSERT INTO event_source(value) VALUES ('cubeapm_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('cubeapm_webhook') ON CONFLICT DO NOTHING;

-- 'cubeapm' (no _webhook suffix) is the source stamped on rules NudgeBee creates
-- IN CubeAPM through the admin alert-rules API, mirroring the provider values
-- V700 registered for the other push-capable providers. Distinct from
-- 'cubeapm_webhook', which marks rules discovered from ingested alerts.
INSERT INTO event_rule_source(value) VALUES ('cubeapm') ON CONFLICT DO NOTHING;
