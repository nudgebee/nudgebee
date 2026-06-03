
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."application_profile" add column "profile_tool" text
--  null;

alter table "public"."application_profile" rename column "output_type" to "profile_tool";
