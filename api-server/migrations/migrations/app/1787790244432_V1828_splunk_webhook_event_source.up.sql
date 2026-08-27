-- Register splunk_webhook as a valid event source and event rule source.
--
-- The integration_types row has existed since V675, so the integration could
-- always be created — but neither enum row was ever added, because the handler
-- only ever emitted events and never upserted an event rule. Phase 2 adds that
-- upsert (createSplunkEventRule writes Source = 'splunk_webhook'), which
-- violates event_rules_source_fkey without the event_rule_source row.
--
-- event_source is added alongside it to match the two most recent webhook
-- integrations (openobserve_webhook V851, elasticsearch_webhook V870), both of
-- which register the pair. Harmless if unused; required if enforced.
INSERT INTO event_source(value) VALUES ('splunk_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('splunk_webhook') ON CONFLICT DO NOTHING;
