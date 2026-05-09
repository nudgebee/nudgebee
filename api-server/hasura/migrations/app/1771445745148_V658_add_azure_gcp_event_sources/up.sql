-- Add Azure_Monitor_Alert and GCP_Metric_Alert to event_source and event_rule_source enum tables
-- These are used by cloud-collector when syncing Azure/GCP monitor alerts as events

INSERT INTO event_source (value) VALUES ('Azure_Monitor_Alert') ON CONFLICT DO NOTHING;
INSERT INTO event_source (value) VALUES ('GCP_Metric_Alert') ON CONFLICT DO NOTHING;

INSERT INTO event_rule_source (value) VALUES ('Azure_Monitor_Alert') ON CONFLICT DO NOTHING;
INSERT INTO event_rule_source (value) VALUES ('GCP_Metric_Alert') ON CONFLICT DO NOTHING;
