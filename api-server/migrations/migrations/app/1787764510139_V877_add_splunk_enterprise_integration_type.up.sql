-- Register splunk_enterprise as an observability platform integration type so it can be
-- created from Admin -> Integrations.
--
-- Deliberately a separate row from splunk_observability_platform: that is Splunk
-- Observability Cloud (SignalFx), a different product reached through a different API.
-- This row is Splunk Enterprise / Splunk Cloud Platform, queried with SPL over the
-- search REST API.
INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('splunk_enterprise', 'observability_platform', 'Splunk Enterprise / Splunk Cloud Platform')
ON CONFLICT ("name") DO NOTHING;
