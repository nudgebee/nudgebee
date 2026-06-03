
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_audit" add column "comments" text
--  null;

ALTER TABLE "public"."upgrade_plan_audit" DROP COLUMN "comments";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."notification_rules" add column "severity" text
--  null;

ALTER TABLE "public"."notification_rules" DROP COLUMN "severity";

alter table "public"."notification_rules" alter column "severity_levels" drop not null;
alter table "public"."notification_rules" add column "severity_levels" json;


DELETE FROM "public"."feature" WHERE "value" = 'GENERATE_RCA';


DELETE FROM "public"."feature" WHERE "value" = 'UPGRADE_PLANNER';

DELETE FROM "public"."notification_source_type" WHERE "value" = 'cloud';
