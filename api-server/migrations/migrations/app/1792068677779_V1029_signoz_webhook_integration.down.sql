DELETE FROM event_rule_source WHERE value = 'signoz_webhook';

DELETE FROM event_source WHERE value = 'signoz_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'signoz_webhook';
