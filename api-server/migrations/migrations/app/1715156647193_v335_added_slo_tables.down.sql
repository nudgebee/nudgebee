
alter table "public"."slo_report" drop constraint "slo_report_status_fkey";

DELETE FROM "public"."slo_status" WHERE "value" = 'OK';

DELETE FROM "public"."slo_status" WHERE "value" = 'FIRING';

alter table "public"."slo_config" alter column "histogram_query" set not null;

alter table "public"."slo_config" alter column "schedule" set not null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."slo_config" add column "window" numeric
--  null;

DROP TABLE "public"."slo_status";

DROP TABLE "public"."slo_report";
