
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."funding_sources" add column "start_date" timestamp
--  not null;

alter table "public"."funding_sources" alter column "start_date" drop not null;
alter table "public"."funding_sources" add column "start_date" time;

DROP TABLE "public"."cloud_resourses";
