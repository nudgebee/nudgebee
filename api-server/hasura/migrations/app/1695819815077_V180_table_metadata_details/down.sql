
alter table "public"."table_metadata_details" drop constraint "table_metadata_details_db_type_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."table_metadata_details" add column "db_type" text
--  not null;

DELETE FROM "public"."db_type" WHERE "value" = 'snowflake';

DELETE FROM "public"."db_type" WHERE "value" = 'redshit';

DELETE FROM "public"."db_type" WHERE "value" = 'postgres';

DROP TABLE "public"."db_type";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."db_type";

DROP TABLE "public"."table_metadata_details";
