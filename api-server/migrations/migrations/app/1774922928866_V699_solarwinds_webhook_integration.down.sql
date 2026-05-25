DELETE FROM event_rule_source WHERE value = 'solarwinds_webhook';
DELETE FROM event_source WHERE value = 'solarwinds_webhook';
DELETE FROM integration_types WHERE name = 'solarwinds_webhook';
