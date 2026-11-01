DELETE FROM event_rule_source WHERE value = 'event_webhook';
DELETE FROM event_source WHERE value = 'event_webhook';
DELETE FROM integration_types WHERE name = 'event_webhook';
