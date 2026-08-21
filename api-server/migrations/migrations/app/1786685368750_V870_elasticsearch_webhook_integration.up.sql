-- Register Elasticsearch/Kibana alerting webhook as an incident_webhook integration type.
-- Without this row, integrations.type violates integrations_type_fkey and the
-- integration cannot be created from the UI at all.
INSERT INTO integration_types(name, category, description) VALUES
  ('elasticsearch_webhook', 'incident_webhook', 'Elasticsearch/Kibana Alerting Webhook')
ON CONFLICT (name) DO NOTHING;

-- Register elasticsearch_webhook as a valid event source and event rule source.
-- event_rule_source is required by the handler's CreateEventRule upsert, which
-- writes Source = 'elasticsearch_webhook'.
INSERT INTO event_source(value) VALUES ('elasticsearch_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('elasticsearch_webhook') ON CONFLICT DO NOTHING;
