


alter table "public"."compliance_check" drop constraint "compliance_check_check_type_fkey";

DELETE FROM "public"."compliance_check_type" WHERE "text" = 'Cloud Custodian';

DROP TABLE "public"."compliance_check_type";

alter table "public"."compliance_check" drop constraint "compliance_check_cloud_provider_fkey";

DELETE FROM "public"."cloud_provider" WHERE "value" = 'Azure';

DELETE FROM "public"."cloud_provider" WHERE "value" = 'GCP';

DELETE FROM "public"."cloud_provider" WHERE "value" = 'AWS';

DROP TABLE "public"."cloud_provider";

alter table "public"."compliance_check_findings" rename column "description" to "description_";

alter table "public"."compliance_check_findings" alter column "description_" set default 'json'::text;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check_findings" add column "description_" text
--  null default 'json';

alter table "public"."compliance_check_findings" alter column "description" drop not null;
alter table "public"."compliance_check_findings" add column "description" varchar;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check_findings" add column "description" varchar
--  null;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."compliance_check" add column "policy" text
--  not null;

ALTER TABLE "public"."compliance_check_findings" ALTER COLUMN "updated_at" TYPE timestamp with time zone;

ALTER TABLE "public"."compliance_check_findings" ALTER COLUMN "created_at" TYPE timestamp with time zone;
