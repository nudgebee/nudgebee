DELETE FROM event_rule_source WHERE value = 'openobserve_webhook';

DELETE FROM event_source WHERE value = 'openobserve_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'openobserve_webhook';
