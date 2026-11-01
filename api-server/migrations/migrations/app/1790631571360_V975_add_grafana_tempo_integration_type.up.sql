-- Register Grafana Tempo as an observability platform (traces only), so it can be
-- created from Admin -> Integrations. Issue #39485.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('grafana_tempo', 'observability_platform', 'Grafana Tempo distributed traces')
ON CONFLICT ("name") DO NOTHING;
