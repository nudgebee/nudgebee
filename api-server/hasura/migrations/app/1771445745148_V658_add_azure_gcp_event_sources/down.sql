-- Remove Azure_Monitor_Alert and GCP_Metric_Alert from event_source and event_rule_source enum tables

DELETE FROM event_rule_source WHERE value IN ('Azure_Monitor_Alert', 'GCP_Metric_Alert');
DELETE FROM event_source WHERE value IN ('Azure_Monitor_Alert', 'GCP_Metric_Alert');
