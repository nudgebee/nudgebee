-- Reverse order of the up migration.
DELETE FROM event_rule_source WHERE value = 'splunk_webhook';

DELETE FROM event_source WHERE value = 'splunk_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'splunk_enterprise';
