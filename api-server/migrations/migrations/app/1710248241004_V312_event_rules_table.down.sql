
alter table "public"."event_rules" drop constraint "event_rules_source_fkey";

alter table "public"."event_rules" drop constraint "event_rules_severity_fkey";

DELETE FROM "public"."event_rule_severity" WHERE "value" = 'critical';

DELETE FROM "public"."event_rule_severity" WHERE "value" = 'warning';

DROP TABLE "public"."event_rule_severity";

DELETE FROM "public"."event_rule_source" WHERE "value" = 'nudgebee';

DELETE FROM "public"."event_rule_source" WHERE "value" = 'prometheus';

DROP TABLE "public"."event_rule_source";


-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_rules" add column "is_editable" boolean
--  null default 'true';

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_rules" add column "updated_by" uuid
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."event_rules" add column "created_by" uuid
--  null;

DROP TABLE "public"."event_rules";
