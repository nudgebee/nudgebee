
alter table "public"."compliance_check" drop constraint "compliance_check_severity_fkey";

ALTER TABLE "public"."compliance_severity" ALTER COLUMN "comment" TYPE character varying;

ALTER TABLE "public"."compliance_severity" ALTER COLUMN "value" TYPE character varying;

alter table "public"."compliance_check_findings" drop constraint "compliance_check_findings_severity_fkey";

DELETE FROM "public"."compliance_severity" WHERE "value" = 'Low';

DELETE FROM "public"."compliance_severity" WHERE "value" = 'Informational';

DELETE FROM "public"."compliance_severity" WHERE "value" = 'Medium';

DELETE FROM "public"."compliance_severity" WHERE "value" = 'Critical';

DELETE FROM "public"."compliance_severity" WHERE "value" = 'High';

DROP TABLE "public"."compliance_severity";

DROP TABLE "public"."compliance_check_findings";

DROP TABLE "public"."compliance_standard_check_mappings";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check" add column "last_executed" timestamp without time zone
--  null;

alter table "public"."compliance_standard" alter column "last_executed" drop not null;
alter table "public"."compliance_standard" add column "last_executed" time;

ALTER TABLE "public"."compliance_check" ALTER COLUMN "created_at" TYPE timestamp with time zone;

ALTER TABLE "public"."compliance_check" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

ALTER TABLE "public"."compliance_standard" ALTER COLUMN "created_at" TYPE timestamp with time zone;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_standard" add column "last_executed" time without time zone
--  null;

ALTER TABLE "public"."compliance_standard" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

DROP TABLE "public"."compliance_standard";

DROP TABLE "public"."compliance_check";
