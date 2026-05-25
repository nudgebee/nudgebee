
alter table "public"."insight" drop constraint "insight_severity_fkey";

DELETE FROM "public"."insight_severity" WHERE "value" = 'Critical';

DELETE FROM "public"."insight_severity" WHERE "value" = 'High';

DELETE FROM "public"."insight_severity" WHERE "value" = 'Medium';

DELETE FROM "public"."insight_severity" WHERE "value" = 'Low';

DROP TABLE "public"."insight_severity";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."insight" add column "severity" text
--  null;
