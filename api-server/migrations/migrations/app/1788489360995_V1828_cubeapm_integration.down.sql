DELETE FROM event_rule_source WHERE value = 'cubeapm';

DELETE FROM event_rule_source WHERE value = 'cubeapm_webhook';

DELETE FROM event_source WHERE value = 'cubeapm_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'cubeapm_webhook';

DELETE FROM "public"."integration_types" WHERE "name" = 'cubeapm';
