
DROP TABLE "public"."alert_history";

alter table "public"."alert_rules" rename to "alert_rule";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."alert_rule" add column "evaluate_at" text
--  not null;

alter table "public"."alert_rule" drop constraint "alert_rule_updated_by_fkey";

alter table "public"."alert_rule" drop constraint "alert_rule_created_by_fkey";

alter table "public"."alert_rule" drop constraint "alert_rule_state_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."alert_rule" add column "state" text
--  not null default 'ok';

DELETE FROM "public"."alert_rule_state" WHERE "value" = 'evaluating';

DELETE FROM "public"."alert_rule_state" WHERE "value" = 'ok';

DELETE FROM "public"."alert_rule_state" WHERE "value" = 'firing';

alter table "public"."alert_rule_state" rename to "alert_status";

DROP TABLE "public"."alert_status";

alter table "public"."alert_rule" drop constraint "alert_rule_source_fkey";

DELETE FROM "public"."alert_rule_source" WHERE "value" = 'matrices';

DELETE FROM "public"."alert_rule_source" WHERE "value" = 'spend';

DROP TABLE "public"."alert_rule_source";

alter table "public"."alert_rule" drop constraint "alert_rule_status_fkey";

DELETE FROM "public"."alert_rule_status" WHERE "value" = 'suspended';

DELETE FROM "public"."alert_rule_status" WHERE "value" = 'active';

DROP TABLE "public"."alert_rule_status";

DROP TABLE "public"."alert_rule";
