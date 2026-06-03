
alter table "public"."upgrade_plan_audit" drop constraint "upgrade_plan_audit_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."upgrade_plan_audit" add column "account_id" uuid
--  not null;

DROP TABLE "public"."upgrade_plan_audit";
