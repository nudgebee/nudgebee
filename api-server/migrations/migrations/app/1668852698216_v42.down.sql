

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."cloud_resourses" add column "tags" json
--  null;

alter table "public"."cloud_resourses" alter column "tags" drop not null;
alter table "public"."cloud_resourses" add column "tags" text;


alter table "public"."recommendation__state" rename to "state_enum";

alter table "public"."recommendation_severity" rename to "severity_enum";


DELETE FROM "public"."account_purpose_type" WHERE "value" = 'UAT';

DELETE FROM "public"."account_purpose_type" WHERE "value" = 'QA';

DELETE FROM "public"."account_purpose_type" WHERE "value" = 'PROD';

DELETE FROM "public"."account_purpose_type" WHERE "value" = 'DEV';

DROP TABLE "public"."account_purpose_type";
