DELETE FROM event_rule_source WHERE value = 'elasticsearch_webhook';
DELETE FROM event_source WHERE value = 'elasticsearch_webhook';
DELETE FROM integration_types WHERE name = 'elasticsearch_webhook';
