
alter table "public"."compliance_check" drop constraint "compliance_check_status_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check" add column "status" text
--  not null default 'active';

DELETE FROM "public"."compliance_check_status_type" WHERE "value" = 'disabled';

DELETE FROM "public"."compliance_check_status_type" WHERE "value" = 'active';

DROP TABLE "public"."compliance_check_status_type";
