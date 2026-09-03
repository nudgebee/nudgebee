DELETE FROM event_rule_source WHERE value = 'incidentio_webhook';

DELETE FROM event_source WHERE value = 'incidentio_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'incidentio_webhook';
