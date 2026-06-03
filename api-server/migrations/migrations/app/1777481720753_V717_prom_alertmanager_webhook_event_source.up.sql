-- Register prometheus_alertmanager_webhook as a valid event source and event rule source
INSERT INTO event_source(value) VALUES ('prometheus_alertmanager_webhook') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source(value) VALUES ('prometheus_alertmanager_webhook') ON CONFLICT DO NOTHING;
