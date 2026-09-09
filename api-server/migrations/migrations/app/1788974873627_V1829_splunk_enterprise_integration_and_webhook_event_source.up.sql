-- Splunk Enterprise: register the integration type and the webhook event source.
--
-- 1. integration_types — registers splunk_enterprise as an observability platform
--    integration so it can be created from Admin -> Integrations. Deliberately a
--    separate row from splunk_observability_platform: that is Splunk Observability
--    Cloud (SignalFx), a different product reached through a different API. This
--    row is Splunk Enterprise / Splunk Cloud Platform, queried with SPL over the
--    search REST API.
--
-- 2. event_source / event_rule_source — registers splunk_webhook as a valid event
--    source and event rule source. The splunk_webhook integration_types row has
--    existed since V675, so the integration could always be created, but neither
--    enum row was ever added because the handler only emitted events and never
--    upserted an event rule. Phase 2 adds that upsert (createSplunkEventRule
--    writes Source = 'splunk_webhook'), which violates event_rules_source_fkey
--    without the event_rule_source row. event_source is added alongside it to
--    match the two most recent webhook integrations (openobserve_webhook V851,
--    elasticsearch_webhook V870), both of which register the pair. Harmless if
--    unused; required if enforced.

INSERT INTO "public"."integration_types"("name", "category", "description")
VALUES ('splunk_enterprise', 'observability_platform', 'Splunk Enterprise / Splunk Cloud Platform')
ON CONFLICT ("name") DO NOTHING;

INSERT INTO event_source(value) VALUES ('splunk_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('splunk_webhook') ON CONFLICT DO NOTHING;
